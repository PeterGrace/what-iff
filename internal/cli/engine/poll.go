package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// watch polls jobID until the reply is final, translating job snapshots into
// events, and closes out on the way of every exit path.
func (e *RemoteEngine) watch(ctx context.Context, jobID uuid.UUID, out chan<- Event) {
	defer close(out)

	w := &watcher{engine: e, ctx: ctx, jobID: jobID, out: out}
	w.run()
}

// watcher is one turn's polling state. It exists so the loop's five pieces of
// state — how much text has been rendered, the last status reported, how many
// polls have been quiet, how many have failed, and whether anything has been
// emitted — are named rather than threaded through arguments.
type watcher struct {
	engine *RemoteEngine
	ctx    context.Context
	jobID  uuid.UUID
	out    chan<- Event

	// rendered is the number of draft_deltas chunks already emitted as
	// Delta events. It is an index into a cumulative array, never a count of
	// bytes — see consumeDeltas.
	rendered int
	// lastStatus is the status of the previous snapshot, so Phase is emitted
	// on change rather than on every poll.
	lastStatus models.JobStatus
	// quiet counts consecutive polls that produced neither new text nor a
	// status change; it drives the backoff.
	quiet int
	// failures counts consecutive unusable poll responses.
	failures int
}

func (w *watcher) run() {
	// The first poll happens immediately: a fast backend (the in-process mock
	// especially) can finish a turn inside one interval, and sleeping first
	// would add that interval to every single turn for nothing.
	for first := true; ; first = false {
		if !first {
			if err := sleepCtx(w.ctx, w.interval()); err != nil {
				w.emit(Error{Err: err})
				return
			}
		}

		job, err := w.engine.api.GetJob(w.ctx, w.jobID)
		if err != nil {
			if !w.tolerate(err) {
				return
			}
			continue
		}
		w.failures = 0

		// Both, and never `||`: a single snapshot routinely carries a status
		// change AND new text (the first poll of a turn always does), and
		// short-circuiting would silently drop the text on exactly those
		// polls — leaving the rendered index behind and the reply truncated.
		statusMoved := w.consumeStatus(job.Status)
		textArrived := w.consumeDeltas(job.DraftDeltas)
		if statusMoved || textArrived {
			w.quiet = 0
		} else {
			w.quiet++
		}

		switch {
		case job.Status == models.JobStatusFailed:
			w.emit(Error{Err: &JobFailedError{JobID: w.jobID, Status: job.Status, Message: job.Error}})
			return
		case ReplyIsFinal(job.Status):
			w.finish(job)
			return
		}
	}
}

// interval implements the adaptive poll rate: fast while a reply is arriving,
// backed off once the turn has been quiet for a few polls in a row.
func (w *watcher) interval() time.Duration {
	if w.quiet >= w.engine.quietPollsBeforeBackoff() {
		return w.engine.idleInterval()
	}
	return w.engine.activeInterval()
}

// consumeStatus emits a Phase when the job's status has moved, and reports
// whether it did. The first observed status always counts as a change, so a
// consumer learns the starting phase without a special case.
func (w *watcher) consumeStatus(status models.JobStatus) bool {
	if status == w.lastStatus {
		return false
	}
	w.lastStatus = status
	w.emit(Phase{Status: status})
	return true
}

// consumeDeltas emits the chunks of deltas that have not been emitted yet,
// and reports whether there were any.
//
// This is the one trap in the whole protocol. draft_deltas is CUMULATIVE: the
// server returns the entire array on every poll, not the chunks that arrived
// since the last one. Emitting all of it each time reprints the whole reply
// on every poll, which reads as a stutter bug rather than as streaming. The
// browser tracks a rendered index for exactly this reason
// (web/app/src/app/features/chat/chat-session.service.ts, handleJobProgressSnapshot).
//
// The strict `>` is load-bearing too. At inference_complete the server clears
// draft_deltas (internal/agent/job_phase.go, persistInferencePhase), so the
// array shrinks to empty while rendered still points past its end. Advancing
// the index unconditionally would rewind it to zero and replay the entire
// reply as the turn finished.
func (w *watcher) consumeDeltas(deltas []string) bool {
	if len(deltas) <= w.rendered {
		return false
	}
	fresh := deltas[w.rendered:]
	w.rendered = len(deltas)
	for _, chunk := range fresh {
		if chunk == "" {
			continue
		}
		w.emit(Delta{Text: chunk})
	}
	return true
}

// finish reconciles the persisted reply and emits the turn's last events.
func (w *watcher) finish(job models.Job) {
	message := w.reconcile(job)
	if message != nil {
		for _, attachment := range message.Attachments {
			if attachment == nil {
				continue
			}
			w.emit(Attachment{File: *attachment})
		}
	}
	w.emit(Done{Status: job.Status, Message: message})
}

// reconcile fetches the assistant message the turn produced.
//
// The job's ResultID is the anchor, not a search through the chat's recent
// messages: the server sets it atomically with inference_complete, and also
// when a cancelled turn's partial draft is promoted to a real message
// (internal/agent/job_phase.go and internal/datastore/job.go). That makes it
// exact even when two turns in the same chat overlap, which a "newest
// assistant message" heuristic cannot be. A nil ResultID means the turn
// genuinely produced no message — a cancel that landed before the first
// token — and reconciling to some older message would be worse than nil.
//
// A reconcile that cannot be completed returns nil rather than failing the
// turn: the caller has already streamed the reply's text from the deltas, and
// losing a whole visible answer over a hiccup fetching its metadata is the
// wrong trade.
func (w *watcher) reconcile(job models.Job) *models.ChatMessage {
	if job.ResultID == nil {
		return nil
	}
	for attempt := 0; ; attempt++ {
		message, err := w.engine.api.GetChatMessage(w.ctx, *job.ResultID)
		if err == nil {
			return &message
		}
		if !transient(w.ctx, err) || attempt >= w.engine.maxTransientFailures() {
			return nil
		}
		if sleepErr := sleepCtx(w.ctx, w.engine.idleInterval()); sleepErr != nil {
			return nil
		}
	}
}

// tolerate decides what a failed poll means. It reports whether the loop
// should keep going; when it returns false it has already emitted the Error
// that ends the turn.
func (w *watcher) tolerate(err error) bool {
	if !transient(w.ctx, err) {
		w.emit(Error{Err: err})
		return false
	}
	w.failures++
	if w.failures > w.engine.maxTransientFailures() {
		w.emit(Error{Err: fmt.Errorf("gave up polling job %s after %d consecutive failures: %w", w.jobID, w.failures, err)})
		return false
	}
	// A struggling server is the one case where polling harder is exactly
	// wrong, so a retry always waits the backed-off interval regardless of
	// how lively the turn looked a moment ago.
	if sleepErr := sleepCtx(w.ctx, w.engine.idleInterval()); sleepErr != nil {
		w.emit(Error{Err: sleepErr})
		return false
	}
	return true
}

// emit delivers ev, or gives up if the caller's context ends first. Without
// the context case, a consumer that stopped reading — a TUI that quit, a
// one-shot whose deadline expired — would wedge this goroutine forever on a
// full channel.
func (w *watcher) emit(ev Event) {
	select {
	case w.out <- ev:
	case <-w.ctx.Done():
	}
}

// transient reports whether err is worth retrying.
//
// A cancelled context never is: the user asked to stop. An API error is
// judged by status — 5xx, 408, 425 and 429 are the server saying "not now",
// while a 400/403/404 says the request itself is wrong and will stay wrong. A
// 401 has already survived the client's transparent token refresh by the time
// it reaches here, so it means the session is genuinely dead.
//
// Anything else — a dial failure, a reset connection, a proxy's HTML page
// failing to decode as JSON — is retried. That is the case
// scripts/mock-e2e.sh's poll_job exists to handle, and it is the reason a
// turn survives a load balancer blinking mid-reply.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return true
		default:
			return apiErr.Status >= 500
		}
	}
	return true
}

// sleepCtx waits for d, or returns the context's error if it ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		// Still a cancellation checkpoint: a zero interval (a test, or a
		// misconfigured field) must not turn the poll loop into something
		// Ctrl-C cannot interrupt.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *RemoteEngine) activeInterval() time.Duration {
	if e.ActiveInterval > 0 {
		return e.ActiveInterval
	}
	return DefaultActiveInterval
}

func (e *RemoteEngine) idleInterval() time.Duration {
	if e.IdleInterval > 0 {
		return e.IdleInterval
	}
	return DefaultIdleInterval
}

func (e *RemoteEngine) quietPollsBeforeBackoff() int {
	if e.QuietPollsBeforeBackoff > 0 {
		return e.QuietPollsBeforeBackoff
	}
	return DefaultQuietPollsBeforeBackoff
}

func (e *RemoteEngine) maxTransientFailures() int {
	if e.MaxTransientFailures > 0 {
		return e.MaxTransientFailures
	}
	return DefaultMaxTransientFailures
}

func (e *RemoteEngine) cancelTimeout() time.Duration {
	if e.CancelTimeout > 0 {
		return e.CancelTimeout
	}
	return DefaultCancelTimeout
}
