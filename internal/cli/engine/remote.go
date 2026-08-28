package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// ErrNoActiveTurn is returned by Resume when the user message it was asked to
// re-attach to has no job in flight — the turn already finished, or never
// started one. It is a normal outcome, not a failure: a restarted client asks
// this question about every unanswered turn it finds.
var ErrNoActiveTurn = errors.New("no turn is in flight for that message")

// API is the slice of the REST client RemoteEngine needs. Declaring it here
// rather than taking a *client.Client keeps the poll loop — the one piece of
// real logic in the CLI — testable against a scripted fake instead of an HTTP
// server.
type API interface {
	CreateChatMessage(ctx context.Context, chatID uuid.UUID, req client.CreateMessageRequest) (models.ChatMessageResponse, error)
	GetJob(ctx context.Context, jobID uuid.UUID) (models.Job, error)
	CancelJob(ctx context.Context, jobID uuid.UUID) (models.Job, error)
	GetActiveChatMessageJob(ctx context.Context, chatID, messageID uuid.UUID) (*models.ActiveChatMessageJobResponse, error)
	GetChatMessage(ctx context.Context, messageID uuid.UUID) (models.ChatMessage, error)
}

// Defaults for RemoteEngine's polling behavior. They are fields rather than
// constants so a test can compress a turn that would otherwise take seconds
// into one that takes milliseconds.
const (
	// DefaultActiveInterval is the gap between polls while a reply is
	// actively arriving. Fast enough that text appears to stream, slow
	// enough not to hammer the server.
	DefaultActiveInterval = 300 * time.Millisecond
	// DefaultIdleInterval is the backed-off gap used once a turn has gone
	// quiet — a long tool call, or a model still thinking.
	DefaultIdleInterval = time.Second
	// DefaultQuietPollsBeforeBackoff is how many consecutive polls may come
	// back with nothing new before the interval backs off. Backing off on
	// the very first quiet poll would add up to a second to time-to-first-
	// token on every single turn, which is the one latency a terminal user
	// actually feels.
	DefaultQuietPollsBeforeBackoff = 3
	// DefaultMaxTransientFailures bounds how many consecutive unusable poll
	// responses are tolerated before the turn is abandoned. A turn must
	// survive a proxy hiccup (scripts/mock-e2e.sh's poll_job learned this
	// the hard way) without surviving a server that is simply gone.
	DefaultMaxTransientFailures = 10
	// DefaultCancelTimeout bounds the cancel request itself. Cancel is
	// typically called from a Ctrl-C handler, when the turn's own context is
	// already cancelled, so it cannot inherit that deadline.
	DefaultCancelTimeout = 10 * time.Second

	// eventBuffer keeps the poll loop from blocking on a consumer that is
	// mid-render when a burst of deltas lands. It is a smoothing buffer, not
	// a queue: a consumer that stops reading entirely still stalls the loop
	// once it fills, and the loop then unblocks on context cancellation.
	eventBuffer = 32
)

// RemoteEngine runs turns against a WhatIff server by polling the job that
// carries the reply.
//
// There is no streaming transport to subscribe to: the server persists
// incremental assistant text on the job row and the browser polls for it, so
// the terminal does the same thing. See the poll loop in watch for the two
// non-obvious parts — the cumulative delta array, and returning at
// inference_complete rather than complete.
type RemoteEngine struct {
	api API

	ActiveInterval          time.Duration
	IdleInterval            time.Duration
	QuietPollsBeforeBackoff int
	MaxTransientFailures    int
	CancelTimeout           time.Duration
}

// NewRemote builds a RemoteEngine with the default polling behavior.
func NewRemote(api API) *RemoteEngine {
	return &RemoteEngine{
		api:                     api,
		ActiveInterval:          DefaultActiveInterval,
		IdleInterval:            DefaultIdleInterval,
		QuietPollsBeforeBackoff: DefaultQuietPollsBeforeBackoff,
		MaxTransientFailures:    DefaultMaxTransientFailures,
		CancelTimeout:           DefaultCancelTimeout,
	}
}

// Compile-time proof that RemoteEngine is what the seam promises.
var _ Engine = (*RemoteEngine)(nil)

// Send posts the user turn and starts watching the job that answers it.
//
// It returns as soon as the server accepts the message (202), so the caller
// has the user message ID and job ID before a single token exists — which is
// what makes a mid-turn crash recoverable via Resume.
func (e *RemoteEngine) Send(ctx context.Context, chatID uuid.UUID, req TurnRequest) (*Turn, error) {
	accepted, err := e.api.CreateChatMessage(ctx, chatID, client.CreateMessageRequest{
		Message:        req.Message,
		ClientTimezone: req.ClientTimezone,
	})
	if err != nil {
		return nil, err
	}
	jobID, err := uuid.Parse(accepted.JobID)
	if err != nil {
		return nil, fmt.Errorf("server accepted the message but returned an unusable job id %q: %w", accepted.JobID, err)
	}
	return e.start(ctx, chatID, accepted.ID, jobID), nil
}

// Resume re-attaches to the turn still generating a reply for messageID.
//
// The job's draft deltas are cumulative and are not consumed by being read,
// so a resumed turn replays every chunk produced so far before continuing
// live — a restarted terminal shows the whole reply, not just its tail.
func (e *RemoteEngine) Resume(ctx context.Context, chatID, messageID uuid.UUID) (*Turn, error) {
	active, err := e.api.GetActiveChatMessageJob(ctx, chatID, messageID)
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, ErrNoActiveTurn
	}
	return e.start(ctx, chatID, messageID, active.JobID), nil
}

func (e *RemoteEngine) start(ctx context.Context, chatID, messageID, jobID uuid.UUID) *Turn {
	events := make(chan Event, eventBuffer)
	go e.watch(ctx, jobID, events)

	return &Turn{
		ChatID:        chatID,
		UserMessageID: messageID,
		JobID:         jobID,
		Events:        events,
		Cancel: func() error {
			// WithoutCancel, not ctx: Cancel exists to be called from a
			// Ctrl-C handler, and by then ctx is usually already cancelled —
			// inheriting it would make the cancel request fail exactly when
			// it is needed. The timeout keeps that from being unbounded.
			cancelCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), e.cancelTimeout())
			defer stop()
			_, err := e.api.CancelJob(cancelCtx, jobID)
			return err
		},
	}
}
