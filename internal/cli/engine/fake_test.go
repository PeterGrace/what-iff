package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// fakeAPI is a scripted stand-in for the REST client. Each hook is a function
// so a test can express the *sequence* a real turn produces — pending, then
// deltas, then a terminal status — which is the only thing the poll loop
// actually reasons about.
type fakeAPI struct {
	mu sync.Mutex

	createFn  func(chatID uuid.UUID, req client.CreateMessageRequest) (models.ChatMessageResponse, error)
	jobFn     func(poll int) (models.Job, error)
	messageFn func(id uuid.UUID) (models.ChatMessage, error)
	activeFn  func(chatID, messageID uuid.UUID) (*models.ActiveChatMessageJobResponse, error)
	cancelFn  func(jobID uuid.UUID) (models.Job, error)

	polls        int
	cancels      int
	messageCalls int
	sentTimezone string
}

func (f *fakeAPI) CreateChatMessage(_ context.Context, chatID uuid.UUID, req client.CreateMessageRequest) (models.ChatMessageResponse, error) {
	f.mu.Lock()
	f.sentTimezone = req.ClientTimezone
	f.mu.Unlock()
	if f.createFn != nil {
		return f.createFn(chatID, req)
	}
	return models.ChatMessageResponse{ID: uuid.New(), JobID: uuid.New().String()}, nil
}

func (f *fakeAPI) GetJob(_ context.Context, _ uuid.UUID) (models.Job, error) {
	f.mu.Lock()
	poll := f.polls
	f.polls++
	f.mu.Unlock()
	return f.jobFn(poll)
}

func (f *fakeAPI) CancelJob(_ context.Context, jobID uuid.UUID) (models.Job, error) {
	f.mu.Lock()
	f.cancels++
	f.mu.Unlock()
	if f.cancelFn != nil {
		return f.cancelFn(jobID)
	}
	return models.Job{Status: models.JobStatusCancelled}, nil
}

func (f *fakeAPI) GetActiveChatMessageJob(_ context.Context, chatID, messageID uuid.UUID) (*models.ActiveChatMessageJobResponse, error) {
	if f.activeFn != nil {
		return f.activeFn(chatID, messageID)
	}
	return nil, nil
}

func (f *fakeAPI) GetChatMessage(_ context.Context, id uuid.UUID) (models.ChatMessage, error) {
	f.mu.Lock()
	f.messageCalls++
	f.mu.Unlock()
	if f.messageFn != nil {
		return f.messageFn(id)
	}
	return models.ChatMessage{ID: id}, nil
}

func (f *fakeAPI) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func (f *fakeAPI) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancels
}

// script yields one job snapshot per poll, repeating the last one forever so
// a test never has to guess how many times the loop will look.
func script(jobs ...models.Job) func(int) (models.Job, error) {
	return func(poll int) (models.Job, error) {
		if poll >= len(jobs) {
			return jobs[len(jobs)-1], nil
		}
		return jobs[poll], nil
	}
}

// testEngine builds a RemoteEngine whose intervals are short enough that a
// whole turn runs inside a test, without setting them to zero — zero falls
// back to the production defaults by design (see activeInterval).
func testEngine(api API) *RemoteEngine {
	e := NewRemote(api)
	e.ActiveInterval = time.Microsecond
	e.IdleInterval = time.Microsecond
	e.CancelTimeout = time.Second
	return e
}

// collect drains a turn's events to completion. It fails the test rather than
// hanging forever if the stream never closes, since a wedged poll loop is
// exactly the kind of bug these tests exist to catch.
func collect(t *testing.T, turn *Turn) []Event {
	t.Helper()

	var events []Event
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-turn.Events:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("turn did not finish; events so far: %s", describe(events))
			return nil
		}
	}
}

// deltaText joins every Delta in order — what a consumer would have rendered.
func deltaText(events []Event) string {
	var out string
	for _, ev := range events {
		if d, ok := ev.(Delta); ok {
			out += d.Text
		}
	}
	return out
}

func phases(events []Event) []models.JobStatus {
	var out []models.JobStatus
	for _, ev := range events {
		if p, ok := ev.(Phase); ok {
			out = append(out, p.Status)
		}
	}
	return out
}

// last returns the turn's final event, which is always a Done or an Error.
func last(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("turn emitted no events at all; want a terminating Done or Error")
	}
	return events[len(events)-1]
}

func describe(events []Event) string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, fmt.Sprintf("%T%+v", ev, ev))
	}
	return fmt.Sprint(out)
}
