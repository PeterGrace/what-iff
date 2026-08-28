package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TestSendStreamsDeltasInChunks is the ordinary happy path: text arrives a
// few chunks at a time and the consumer sees each chunk exactly once, in
// order.
func TestSendStreamsDeltasInChunks(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusPending},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"The "}},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"The ", "deploy "}},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"The ", "deploy ", "is fine."}},
			models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID},
		),
		messageFn: func(id uuid.UUID) (models.ChatMessage, error) {
			return models.ChatMessage{ID: id, Message: "The deploy is fine.", Origin: models.MessageOriginAssistant}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "how is the deploy"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if got := deltaText(events); got != "The deploy is fine." {
		t.Errorf("streamed text = %q, want %q", got, "The deploy is fine.")
	}
	done, ok := last(t, events).(Done)
	if !ok {
		t.Fatalf("last event = %T, want Done; all events: %s", last(t, events), describe(events))
	}
	if done.Message == nil || done.Message.Message != "The deploy is fine." {
		t.Errorf("done.Message = %+v, want the reconciled assistant reply", done.Message)
	}
}

// TestCumulativeDeltasEmitOnlyTheTail is the regression test for the one trap
// in the protocol: draft_deltas is the whole reply on every poll, not the
// new chunks. Emitting it wholesale reprints everything each poll, which
// looks like a stutter bug rather than streaming.
//
// The script deliberately re-serves the identical cumulative array several
// times — a real client polls faster than the model produces tokens, so most
// polls see no growth at all.
func TestCumulativeDeltasEmitOnlyTheTail(t *testing.T) {
	full := []string{"one ", "two ", "three"}
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: full[:1]},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: full[:1]},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: full[:2]},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: full[:3]},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: full[:3]},
			models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID},
		),
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "count"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if got := deltaText(events); got != "one two three" {
		t.Errorf("streamed text = %q, want %q (each chunk exactly once)", got, "one two three")
	}
	var deltas int
	for _, ev := range events {
		if _, ok := ev.(Delta); ok {
			deltas++
		}
	}
	if deltas != 3 {
		t.Errorf("emitted %d Delta events, want 3 — one per chunk, not one per poll", deltas)
	}
}

// TestDeltasClearedAtInferenceCompleteAreNotReplayed pins the other half of
// the cumulative-array handling. The server empties draft_deltas when it
// marks the job inference_complete (internal/agent/job_phase.go), so the
// array shrinks past the rendered index. A loop that resynced the index to
// the array length would rewind to zero and replay the whole reply as the
// turn ended.
func TestDeltasClearedAtInferenceCompleteAreNotReplayed(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"partial "}},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"partial ", "reply"}},
			// Same turn, deltas cleared, terminal-ish status.
			models.Job{Status: models.JobStatusInferenceComplete, DraftDeltas: nil, ResultID: &resultID},
		),
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if got := deltaText(events); got != "partial reply" {
		t.Errorf("streamed text = %q, want %q with no replay", got, "partial reply")
	}
}

// TestPhaseTransitionsAreReportedOnChange pins that a consumer learns the
// starting phase and every move after it, without a Phase per poll.
func TestPhaseTransitionsAreReportedOnChange(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusPending},
			models.Job{Status: models.JobStatusPending},
			models.Job{Status: models.JobStatusProcessing},
			models.Job{Status: models.JobStatusProcessing},
			models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID},
		),
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	want := []models.JobStatus{
		models.JobStatusPending,
		models.JobStatusProcessing,
		models.JobStatusInferenceComplete,
	}
	got := phases(events)
	if len(got) != len(want) {
		t.Fatalf("phases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("phases = %v, want %v", got, want)
		}
	}
}

// TestReturnsAtInferenceCompleteWithoutWaitingForComplete is the latency
// decision made explicit: the reply text is final at inference_complete, and
// the phases after it (expression picking, checkpointing) are the server's
// business. Blocking until `complete` would make the terminal feel slower
// than the browser for nothing the reader can see.
func TestReturnsAtInferenceCompleteWithoutWaitingForComplete(t *testing.T) {
	resultID := uuid.New()
	var polled int32
	api := &fakeAPI{
		jobFn: func(poll int) (models.Job, error) {
			atomic.StoreInt32(&polled, int32(poll)+1)
			if poll == 0 {
				return models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"done"}}, nil
			}
			if poll == 1 {
				return models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID}, nil
			}
			t.Errorf("poll %d: the loop kept polling past inference_complete", poll)
			return models.Job{Status: models.JobStatusComplete, ResultID: &resultID}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	done, ok := last(t, events).(Done)
	if !ok {
		t.Fatalf("last event = %T, want Done", last(t, events))
	}
	if done.Status != models.JobStatusInferenceComplete {
		t.Errorf("done.Status = %q, want %q", done.Status, models.JobStatusInferenceComplete)
	}
	if got := api.pollCount(); got != 2 {
		t.Errorf("polled %d times, want 2 — the turn should end at inference_complete", got)
	}
}

// TestJobFailureEndsTurnWithTypedError pins that a failed job is reported as
// a JobFailedError carrying the server's own wording, not as a generic
// transport failure, so a caller can tell "the model failed" apart from "the
// network failed".
func TestJobFailureEndsTurnWithTypedError(t *testing.T) {
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"almost"}},
			models.Job{Status: models.JobStatusFailed, Error: "provider refused the request"},
		),
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	errEvent, ok := last(t, events).(Error)
	if !ok {
		t.Fatalf("last event = %T, want Error; all events: %s", last(t, events), describe(events))
	}
	var failed *JobFailedError
	if !errors.As(errEvent, &failed) {
		t.Fatalf("Error.Err = %v, want a *JobFailedError", errEvent.Err)
	}
	if failed.Status != models.JobStatusFailed {
		t.Errorf("failed.Status = %q, want %q", failed.Status, models.JobStatusFailed)
	}
	if !strings.Contains(failed.Error(), "provider refused the request") {
		t.Errorf("failed.Error() = %q, want it to carry the server's message", failed.Error())
	}
}

// TestCancelKeepsThePartialReply pins the behavior inherited from the
// backend: cancelling promotes a non-empty draft to a real assistant message,
// so the turn ends with a Done carrying the partial text rather than an Error
// throwing it away.
func TestCancelKeepsThePartialReply(t *testing.T) {
	resultID := uuid.New()
	var cancelled atomic.Bool
	api := &fakeAPI{
		jobFn: func(poll int) (models.Job, error) {
			if cancelled.Load() {
				return models.Job{Status: models.JobStatusCancelled, ResultID: &resultID}, nil
			}
			return models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"half a "}}, nil
		},
		cancelFn: func(uuid.UUID) (models.Job, error) {
			cancelled.Store(true)
			return models.Job{Status: models.JobStatusCancelled, ResultID: &resultID}, nil
		},
		messageFn: func(id uuid.UUID) (models.ChatMessage, error) {
			return models.ChatMessage{ID: id, Message: "half a ", Origin: models.MessageOriginAssistant}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	// Wait until the turn is actually streaming before interrupting it, so
	// the test exercises cancel-with-partial rather than cancel-before-start.
	first, ok := <-turn.Events
	if !ok {
		t.Fatal("turn ended before emitting anything")
	}
	if err := turn.Cancel(); err != nil {
		t.Fatalf("Cancel returned %v", err)
	}

	events := append([]Event{first}, collect(t, turn)...)
	done, ok := last(t, events).(Done)
	if !ok {
		t.Fatalf("last event = %T, want Done — a cancel must not discard the partial reply", last(t, events))
	}
	if done.Status != models.JobStatusCancelled {
		t.Errorf("done.Status = %q, want %q", done.Status, models.JobStatusCancelled)
	}
	if done.Message == nil || done.Message.Message != "half a " {
		t.Errorf("done.Message = %+v, want the promoted partial reply", done.Message)
	}
	if api.cancelCount() != 1 {
		t.Errorf("CancelJob called %d times, want 1", api.cancelCount())
	}
}

// TestCancelBeforeAnyTokenHasNoMessage pins the other cancel case: a job
// cancelled before a single delta arrived has no result_id, so there is no
// assistant message to reconcile and Done carries nil rather than some older
// message picked up by a heuristic.
func TestCancelBeforeAnyTokenHasNoMessage(t *testing.T) {
	api := &fakeAPI{
		jobFn: script(models.Job{Status: models.JobStatusCancelled}),
		messageFn: func(uuid.UUID) (models.ChatMessage, error) {
			t.Error("reconcile fetched a message for a turn with no result_id")
			return models.ChatMessage{}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	done, ok := last(t, events).(Done)
	if !ok {
		t.Fatalf("last event = %T, want Done", last(t, events))
	}
	if done.Message != nil {
		t.Errorf("done.Message = %+v, want nil", done.Message)
	}
}

// TestTransientPollFailuresDoNotKillTheTurn is the lesson
// scripts/mock-e2e.sh's poll_job encodes: a 502 from a load balancer, or a
// proxy's HTML page where JSON was expected, is a blip. Dying on it loses a
// reply that the server went on to finish perfectly well.
func TestTransientPollFailuresDoNotKillTheTurn(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: func(poll int) (models.Job, error) {
			switch poll {
			case 0:
				return models.Job{}, &client.APIError{Status: http.StatusBadGateway}
			case 1:
				return models.Job{}, errors.New("dial tcp: connection reset by peer")
			case 2:
				return models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"survived"}}, nil
			default:
				return models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID}, nil
			}
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if got := deltaText(events); got != "survived" {
		t.Errorf("streamed text = %q, want %q", got, "survived")
	}
	if _, ok := last(t, events).(Done); !ok {
		t.Fatalf("last event = %T, want Done", last(t, events))
	}
}

// TestPersistentPollFailureGivesUp pins the other side of that tolerance: a
// server that is genuinely gone must end the turn rather than poll forever.
func TestPersistentPollFailureGivesUp(t *testing.T) {
	api := &fakeAPI{
		jobFn: func(int) (models.Job, error) {
			return models.Job{}, &client.APIError{Status: http.StatusBadGateway}
		},
	}
	engine := testEngine(api)
	engine.MaxTransientFailures = 3

	turn, err := engine.Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if _, ok := last(t, events).(Error); !ok {
		t.Fatalf("last event = %T, want Error after exhausting retries", last(t, events))
	}
	if got := api.pollCount(); got > 5 {
		t.Errorf("polled %d times, want it bounded near MaxTransientFailures (3)", got)
	}
}

// TestPermanentPollErrorFailsFast pins the classification: a 404 says the
// request itself is wrong and will stay wrong, so retrying it ten times just
// delays the error the user needs to see.
func TestPermanentPollErrorFailsFast(t *testing.T) {
	api := &fakeAPI{
		jobFn: func(int) (models.Job, error) {
			return models.Job{}, &client.APIError{Status: http.StatusNotFound, Message: "Job not found"}
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if _, ok := last(t, events).(Error); !ok {
		t.Fatalf("last event = %T, want Error", last(t, events))
	}
	if got := api.pollCount(); got != 1 {
		t.Errorf("polled %d times, want 1 — a 404 is not worth retrying", got)
	}
}

// TestContextCancellationEndsTheTurn pins that Ctrl-C actually stops the poll
// loop instead of leaking a goroutine that keeps hitting the server.
func TestContextCancellationEndsTheTurn(t *testing.T) {
	api := &fakeAPI{
		jobFn: script(models.Job{Status: models.JobStatusProcessing}),
	}
	engine := testEngine(api)
	engine.ActiveInterval = 20 * time.Millisecond
	engine.IdleInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	turn, err := engine.Send(ctx, uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	<-turn.Events
	cancel()

	events := collect(t, turn)
	if len(events) == 0 {
		return // the stream closed without a further event; the loop still stopped
	}
	if errEvent, ok := last(t, events).(Error); ok && !errors.Is(errEvent, context.Canceled) {
		t.Errorf("Error.Err = %v, want context.Canceled", errEvent.Err)
	}
}

// TestAttachmentsAreEmittedBeforeDone pins where attachments come from: the
// reconciled message, which is the first point they are persisted and
// addressable.
func TestAttachmentsAreEmittedBeforeDone(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID}),
		messageFn: func(id uuid.UUID) (models.ChatMessage, error) {
			return models.ChatMessage{
				ID:      id,
				Message: "here it is",
				Attachments: []*models.FileAttachment{
					{ID: uuid.New(), Name: "diagram.png", FileType: "image/png"},
					nil, // a nil entry must not panic the loop
				},
			}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "draw me a diagram"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	var attachments []Attachment
	for _, ev := range events {
		if a, ok := ev.(Attachment); ok {
			attachments = append(attachments, a)
		}
	}
	if len(attachments) != 1 {
		t.Fatalf("emitted %d Attachment events, want 1; all events: %s", len(attachments), describe(events))
	}
	if attachments[0].File.Name != "diagram.png" {
		t.Errorf("attachment name = %q, want diagram.png", attachments[0].File.Name)
	}
	if _, ok := last(t, events).(Done); !ok {
		t.Errorf("last event = %T, want Done after the attachments", last(t, events))
	}
}

// TestReconcileFailureStillCompletesTheTurn pins the deliberate trade: the
// user has already seen the reply stream past, so losing the metadata fetch
// must not retract it.
func TestReconcileFailureStillCompletesTheTurn(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: script(
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"visible text"}},
			models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID},
		),
		messageFn: func(uuid.UUID) (models.ChatMessage, error) {
			return models.ChatMessage{}, &client.APIError{Status: http.StatusForbidden, Message: "nope"}
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	done, ok := last(t, events).(Done)
	if !ok {
		t.Fatalf("last event = %T, want Done despite the failed reconcile", last(t, events))
	}
	if done.Message != nil {
		t.Errorf("done.Message = %+v, want nil when reconciliation failed", done.Message)
	}
	if got := deltaText(events); got != "visible text" {
		t.Errorf("streamed text = %q, want it preserved", got)
	}
}

// TestSendForwardsTimezone pins the field that decides whether the assistant
// can answer "what day is it" in the user's terms rather than the server's.
func TestSendForwardsTimezone(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{jobFn: script(models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID})}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{
		Message:        "what day is it",
		ClientTimezone: "Europe/Berlin",
	})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	collect(t, turn)

	if api.sentTimezone != "Europe/Berlin" {
		t.Errorf("sent client_timezone = %q, want Europe/Berlin", api.sentTimezone)
	}
}

// TestSendRejectsUnusableJobID pins the one thing Send itself validates: a
// 202 whose job_id will not parse means there is nothing to poll, and saying
// so immediately beats starting a loop that can only fail.
func TestSendRejectsUnusableJobID(t *testing.T) {
	api := &fakeAPI{
		createFn: func(uuid.UUID, client.CreateMessageRequest) (models.ChatMessageResponse, error) {
			return models.ChatMessageResponse{ID: uuid.New(), JobID: "not-a-uuid"}, nil
		},
		jobFn: func(int) (models.Job, error) {
			t.Error("polling started despite an unusable job id")
			return models.Job{}, nil
		},
	}

	if _, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"}); err == nil {
		t.Fatal("Send returned nil error for an unparseable job_id")
	}
}

// TestFirstSnapshotEmitsBothPhaseAndDeltas is the regression test for a
// short-circuit that cost a whole first chunk: the opening poll of a turn
// almost always carries a status change AND the first text at once, and
// evaluating the two as `statusMoved || textArrived` skipped the text on
// exactly those polls. Every other case in this file happened to see the same
// deltas again on a later poll, which hid it.
func TestFirstSnapshotEmitsBothPhaseAndDeltas(t *testing.T) {
	resultID := uuid.New()
	api := &fakeAPI{
		jobFn: func(poll int) (models.Job, error) {
			if poll == 0 {
				// One snapshot, one chance: a status the loop has not seen
				// plus text it has not rendered, never repeated.
				return models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"only chance"}}, nil
			}
			return models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID}, nil
		},
	}

	turn, err := testEngine(api).Send(context.Background(), uuid.New(), TurnRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("Send returned %v", err)
	}
	events := collect(t, turn)

	if got := deltaText(events); got != "only chance" {
		t.Errorf("streamed text = %q, want %q", got, "only chance")
	}
	if got := phases(events); len(got) == 0 || got[0] != models.JobStatusProcessing {
		t.Errorf("phases = %v, want processing first", got)
	}
}
