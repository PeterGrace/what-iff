package oneshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/engine"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/modeltypes"
)

// harness wires a Runner to buffers and a scripted engine.
type harness struct {
	runner *Runner
	engine *fakeEngine
	chats  *fakeChats
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

func newHarness(events ...engine.Event) *harness {
	h := &harness{
		engine: &fakeEngine{events: events},
		chats:  &fakeChats{},
		out:    &bytes.Buffer{},
		errOut: &bytes.Buffer{},
	}
	h.runner = &Runner{Engine: h.engine, Chats: h.chats, Out: h.out, Err: h.errOut}
	return h
}

func (h *harness) run(t *testing.T, opts Options) error {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 8, 28, 14, 3, 0, 0, time.UTC) }
	}
	return h.runner.Run(context.Background(), opts)
}

func TestRunStreamsToATerminal(t *testing.T) {
	h := newHarness(
		engine.Phase{Status: models.JobStatusProcessing},
		engine.Delta{Text: "The deploy "},
		engine.Delta{Text: "is fine."},
		doneWith(&models.ChatMessage{Message: "The deploy is fine."}),
	)

	if err := h.run(t, Options{Prompt: "how is the deploy", Stream: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if got := h.out.String(); got != "The deploy is fine.\n" {
		t.Errorf("stdout = %q, want the streamed reply with one trailing newline", got)
	}
}

// TestRunBuffersWhenPiped pins the other half of the same decision: a pipe
// gets one clean write of the persisted reply, not the chunk-by-chunk
// rendering a human watches.
func TestRunBuffersWhenPiped(t *testing.T) {
	h := newHarness(
		engine.Delta{Text: "The deploy "},
		engine.Delta{Text: "is fine."},
		doneWith(&models.ChatMessage{Message: "The deploy is fine."}),
	)

	if err := h.run(t, Options{Prompt: "how is the deploy", Stream: false}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if got := h.out.String(); got != "The deploy is fine.\n" {
		t.Errorf("stdout = %q, want the whole reply written once", got)
	}
}

// TestRunPrefersThePersistedReply pins which text wins. A turn that finished
// inside a single poll never produced deltas at all, and the deltas that do
// exist are a rendering aid — the conversation contains the persisted
// message.
func TestRunPrefersThePersistedReply(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "the whole reply, never streamed"}))

	if err := h.run(t, Options{Prompt: "hi", Stream: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if got := h.out.String(); got != "the whole reply, never streamed\n" {
		t.Errorf("stdout = %q, want the persisted reply", got)
	}
}

// TestRunFallsBackToStreamedTextWhenReconcileFailed is the other side of
// that: the engine hands back a nil Message when it could not fetch the
// persisted one, and the reply the user watched arrive must not vanish.
func TestRunFallsBackToStreamedTextWhenReconcileFailed(t *testing.T) {
	h := newHarness(
		engine.Delta{Text: "visible text"},
		engine.Done{Status: models.JobStatusComplete, Message: nil},
	)

	if err := h.run(t, Options{Prompt: "hi", Stream: false}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if got := h.out.String(); got != "visible text\n" {
		t.Errorf("stdout = %q, want the streamed text preserved", got)
	}
}

func TestRunJSONPayload(t *testing.T) {
	h := newHarness(
		engine.Delta{Text: "ignored in favour of the persisted message"},
		doneWith(&models.ChatMessage{
			Message:          "The deploy is fine.",
			GenerationModel:  "gpt-test",
			ContextBreakdown: &modeltypes.ContextBreakdown{TotalTokens: 1234},
		}),
	)
	h.engine.chatID = uuid.New()
	h.engine.jobID = uuid.New()

	if err := h.run(t, Options{Prompt: "hi", JSON: true, Stream: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}

	var payload struct {
		ChatID  string `json:"chat_id"`
		Message string `json:"message"`
		Model   string `json:"model"`
		Tokens  int    `json:"tokens"`
		JobID   string `json:"job_id"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout was not JSON: %v (%s)", err, h.out.String())
	}
	if payload.ChatID != h.engine.chatID.String() {
		t.Errorf("chat_id = %q, want %q", payload.ChatID, h.engine.chatID)
	}
	if payload.JobID != h.engine.jobID.String() {
		t.Errorf("job_id = %q, want %q", payload.JobID, h.engine.jobID)
	}
	if payload.Message != "The deploy is fine." {
		t.Errorf("message = %q, want the persisted reply", payload.Message)
	}
	if payload.Model != "gpt-test" {
		t.Errorf("model = %q, want gpt-test", payload.Model)
	}
	if payload.Tokens != 1234 {
		t.Errorf("tokens = %d, want 1234", payload.Tokens)
	}
}

// TestRunJSONDoesNotStream pins that --json wins over a TTY: a structured
// payload with the reply also dribbled out ahead of it is not parseable.
func TestRunJSONDoesNotStream(t *testing.T) {
	h := newHarness(
		engine.Delta{Text: "chunk one "},
		engine.Delta{Text: "chunk two"},
		doneWith(&models.ChatMessage{Message: "chunk one chunk two"}),
	)

	if err := h.run(t, Options{Prompt: "hi", JSON: true, Stream: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout was not parseable as a single JSON object: %v (%s)", err, h.out.String())
	}
}

// TestRunJSONOmitsUnknownTokens pins that a turn whose breakdown the server
// did not capture reports no token count rather than 0, which would read as
// "this turn cost nothing".
func TestRunJSONOmitsUnknownTokens(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "hi", GenerationModel: "gpt-test"}))

	if err := h.run(t, Options{Prompt: "hi", JSON: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout was not JSON: %v", err)
	}
	if _, present := payload["tokens"]; present {
		t.Errorf("tokens = %v, want the key omitted when the server captured no breakdown", payload["tokens"])
	}
}

// TestRunNotesTheCreatedChatOnStderr pins where the "Started chat" line goes.
// Without the id a turn that started a chat leaves no way to continue it;
// on stdout it would corrupt `wi -p ... > answer.txt`.
func TestRunNotesTheCreatedChatOnStderr(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "hi"}))

	if err := h.run(t, Options{Prompt: "hello"}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !strings.Contains(h.errOut.String(), h.chats.createdID.String()) {
		t.Errorf("stderr = %q, want it to name the created chat %s", h.errOut.String(), h.chats.createdID)
	}
	if strings.Contains(h.out.String(), "Started chat") {
		t.Errorf("stdout = %q, want the advisory line kept off stdout", h.out.String())
	}
}

// TestRunQuietSuppressesNotes pins --quiet as "the reply and nothing else",
// including the notice about a chat that was created.
func TestRunQuietSuppressesNotes(t *testing.T) {
	h := newHarness(
		engine.Attachment{File: models.FileAttachment{Name: "diagram.png", FileType: "image/png"}},
		doneWith(&models.ChatMessage{Message: "here it is"}),
	)

	if err := h.run(t, Options{Prompt: "draw me a diagram", Quiet: true}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if h.errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing under --quiet", h.errOut.String())
	}
	if got := h.out.String(); got != "here it is\n" {
		t.Errorf("stdout = %q, want the reply alone", got)
	}
}

func TestRunNotesAttachments(t *testing.T) {
	h := newHarness(
		engine.Attachment{File: models.FileAttachment{Name: "diagram.png", FileType: "image/png"}},
		doneWith(&models.ChatMessage{Message: "here it is"}),
	)

	if err := h.run(t, Options{Prompt: "draw me a diagram"}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !strings.Contains(h.errOut.String(), "diagram.png") {
		t.Errorf("stderr = %q, want it to name the attachment", h.errOut.String())
	}
}

// TestRunCancelledTurnKeepsThePartialAndFails pins both halves of an
// interrupt: the partial reply is written (the server saved it too), and the
// exit status is still non-zero so a script does not mistake it for a
// complete answer.
func TestRunCancelledTurnKeepsThePartialAndFails(t *testing.T) {
	h := newHarness(
		engine.Delta{Text: "half a "},
		engine.Done{Status: models.JobStatusCancelled, Message: &models.ChatMessage{Message: "half a "}},
	)

	err := h.run(t, Options{Prompt: "hi", Stream: false})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Run error = %v, want ErrInterrupted", err)
	}
	// The trailing space is the model's, not the renderer's: writeBlock trims
	// trailing newlines so line counts stay honest, and nothing else.
	if got := h.out.String(); got != "half a \n" {
		t.Errorf("stdout = %q, want the partial reply written anyway", got)
	}
}

// TestRunFailedTurnReturnsTheEngineError keeps the server's own wording,
// which is the only thing that tells a user why a turn failed.
func TestRunFailedTurnReturnsTheEngineError(t *testing.T) {
	cause := &engine.JobFailedError{
		JobID:   uuid.New(),
		Status:  models.JobStatusFailed,
		Message: "provider refused the request",
	}
	h := newHarness(engine.Error{Err: cause})

	err := h.run(t, Options{Prompt: "hi"})

	var failed *engine.JobFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Run error = %v, want the engine's *JobFailedError", err)
	}
	if !strings.Contains(err.Error(), "provider refused the request") {
		t.Errorf("Run error = %q, want the server's wording", err)
	}
}

func TestRunRejectsAnEmptyPrompt(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "unreachable"}))

	err := h.run(t, Options{Prompt: "   ", Stdin: "\n"})
	if err == nil {
		t.Fatal("Run returned nil error with nothing to send")
	}
	if h.engine.sends != 0 {
		t.Errorf("Send called %d times, want 0", h.engine.sends)
	}
	if h.chats.creates != 0 {
		t.Errorf("CreateChat called %d times, want 0 — an empty prompt must not leave a stray chat behind", h.chats.creates)
	}
}

// TestRunFoldsStdinIntoTheTurn pins the pipe case end to end: the folded
// prompt, not just the -p argument, is what reaches the engine.
func TestRunFoldsStdinIntoTheTurn(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "looks fine"}))

	if err := h.run(t, Options{
		Prompt:   "review this",
		Stdin:    "diff --git a/x b/x\n",
		Timezone: "Europe/Berlin",
	}); err != nil {
		t.Fatalf("Run returned %v", err)
	}

	want := "review this\n\ndiff --git a/x b/x"
	if h.engine.sentReq.Message != want {
		t.Errorf("sent message = %q, want %q", h.engine.sentReq.Message, want)
	}
	if h.engine.sentReq.ClientTimezone != "Europe/Berlin" {
		t.Errorf("sent timezone = %q, want Europe/Berlin", h.engine.sentReq.ClientTimezone)
	}
}

// TestRunTargetsTheResolvedChat pins that --chat actually steers the turn.
func TestRunTargetsTheResolvedChat(t *testing.T) {
	want := uuid.New()
	h := newHarness(doneWith(&models.ChatMessage{Message: "hi"}))
	h.chats.listed = []models.Chat{{ID: want, Name: "deploy plan"}}

	if err := h.run(t, Options{Prompt: "hi", ChatRef: "deploy plan"}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if h.engine.sentTo != want {
		t.Errorf("sent to chat %s, want %s", h.engine.sentTo, want)
	}
	if h.chats.creates != 0 {
		t.Errorf("CreateChat called %d times, want 0 when --chat names an existing chat", h.chats.creates)
	}
}

// TestRunNamesANewChatAfterThePrompt pins the naming choice: the chat this
// turn created has to be findable in `wi chats` later.
func TestRunNamesANewChatAfterThePrompt(t *testing.T) {
	h := newHarness(doneWith(&models.ChatMessage{Message: "hi"}))

	if err := h.run(t, Options{Prompt: "review this migration\n\nmore detail"}); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if h.chats.createdName != "review this migration" {
		t.Errorf("created chat name = %q, want %q", h.chats.createdName, "review this migration")
	}
}
