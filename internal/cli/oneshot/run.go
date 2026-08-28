package oneshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/engine"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// ErrInterrupted is returned when the turn was cancelled. The partial reply
// has already been written by then, and the server saved it too — this is the
// non-zero exit status, not a report that anything was lost.
var ErrInterrupted = errors.New("interrupted — the partial reply was saved")

// Options is one non-interactive turn.
type Options struct {
	// Prompt is the -p argument; Stdin is whatever was piped in. Either may
	// be empty, but not both.
	Prompt string
	Stdin  string

	// ChatRef is --chat: a chat UUID, a chat name, or empty to create one.
	ChatRef string

	// Stream renders deltas as they arrive instead of buffering the reply.
	// The caller sets it from whether stdout is a terminal: a human watches
	// text appear, a pipe wants one clean write.
	Stream bool
	// JSON emits a single structured object instead of prose.
	JSON bool
	// Quiet suppresses everything that is not the reply itself.
	Quiet bool

	// Timezone is an IANA name forwarded to the server so the assistant
	// reasons in the user's day, not the server's.
	Timezone string

	// Now names a generated chat when the prompt has no usable text. It
	// defaults to time.Now.
	Now func() time.Time
}

// Runner executes one turn and renders it.
type Runner struct {
	Engine engine.Engine
	Chats  Chats

	// Out receives the reply. Err receives everything else — the chat that
	// was created, attachment notices, the interrupted marker — so that
	// `wi -p ... > answer.txt` captures the answer and nothing else.
	Out io.Writer
	Err io.Writer
}

// result is what --json emits, and what the prose renderer falls back to.
type result struct {
	ChatID  uuid.UUID `json:"chat_id"`
	Message string    `json:"message"`
	Model   string    `json:"model,omitempty"`
	// Tokens is the assistant turn's total context estimate. It is omitted
	// rather than zeroed when the server did not capture a breakdown for the
	// turn, because 0 would read as "this turn cost nothing".
	Tokens int    `json:"tokens,omitempty"`
	JobID  string `json:"job_id"`
}

// Run sends one turn and renders it to completion.
//
// The returned error is also the exit status: nil for a turn that completed,
// ErrInterrupted for one that was cancelled, and the engine's own error for
// one that failed. The reply itself has already been written in every case
// where there was one, including the cancelled one.
func (r *Runner) Run(ctx context.Context, opts Options) error {
	prompt := FoldPrompt(opts.Prompt, opts.Stdin)
	if prompt == "" {
		return errors.New("nothing to send: pass a prompt with -p, or pipe text in")
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := r.session(opts)
	chatID, created, err := resolveChat(ctx, r.Chats, opts.ChatRef, ChatNameForPrompt(prompt, now()))
	if err != nil {
		return err
	}
	if created {
		// Not decoration: without the id, a turn that started a chat leaves
		// the user no way to continue it. It goes to the notes stream so it
		// never contaminates a redirected or piped reply.
		s.note("Started chat %s\n", chatID)
	}

	turn, err := r.Engine.Send(ctx, chatID, engine.TurnRequest{
		Message:        prompt,
		ClientTimezone: opts.Timezone,
	})
	if err != nil {
		return err
	}

	return s.render(turn)
}

// session is one invocation's rendering state: where the reply goes, where
// advisory lines go, and what the caller asked for.
//
// It exists so --quiet is enforced in exactly one place — by pointing the
// notes stream at io.Discard — rather than by every caller of note
// remembering to check a flag.
type session struct {
	out   io.Writer
	notes io.Writer
	opts  Options
}

func (r *Runner) session(opts Options) *session {
	notes := r.Err
	if opts.Quiet || notes == nil {
		notes = io.Discard
	}
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	return &session{out: out, notes: notes, opts: opts}
}

// render consumes the turn's events and writes the reply.
//
// Streaming and buffering differ only in when text reaches Out. Both keep the
// accumulated delta text, because it is the fallback when reconciliation
// could not fetch the persisted message — the reply the user watched arrive
// must not vanish from a --json payload just because a metadata fetch failed.
func (s *session) render(turn *engine.Turn) error {
	streaming := s.opts.Stream && !s.opts.JSON

	var streamed strings.Builder
	var final *models.ChatMessage
	status := models.JobStatusPending
	var turnErr error

	for event := range turn.Events {
		switch ev := event.(type) {
		case engine.Delta:
			streamed.WriteString(ev.Text)
			if streaming {
				fmt.Fprint(s.out, ev.Text)
			}
		case engine.Phase:
			status = ev.Status
		case engine.Attachment:
			// Downloading attachments arrives with milestone 5; naming them
			// is what this milestone can honestly do, and silence would be
			// worse than a line saying the reply produced a file.
			s.note("Attachment: %s (%s)\n", ev.File.Name, ev.File.FileType)
		case engine.Done:
			status = ev.Status
			final = ev.Message
		case engine.Error:
			turnErr = ev.Err
		}
	}

	if turnErr != nil {
		// A failed turn may still have streamed text before it broke. Close
		// the line so the error does not land mid-sentence.
		if streaming && streamed.Len() > 0 {
			s.endLine()
		}
		return turnErr
	}

	text := streamed.String()
	if final != nil && final.Message != "" {
		// The persisted message wins over the accumulated deltas: it is what
		// the conversation actually contains, and a turn that finished
		// inside a single poll never produced deltas at all.
		text = final.Message
	}

	switch {
	case s.opts.JSON:
		if err := s.writeJSON(turn, text, final); err != nil {
			return err
		}
	case streaming:
		if streamed.Len() > 0 {
			s.endLine()
		} else {
			// Nothing streamed — a fast backend can finish a turn inside the
			// first poll, before a single delta is persisted.
			s.writeBlock(text)
		}
	default:
		s.writeBlock(text)
	}

	if status == models.JobStatusCancelled {
		return ErrInterrupted
	}
	return nil
}

func (s *session) writeJSON(turn *engine.Turn, text string, final *models.ChatMessage) error {
	payload := result{
		ChatID:  turn.ChatID,
		Message: text,
		JobID:   turn.JobID.String(),
	}
	if final != nil {
		payload.Model = final.GenerationModel
		if final.ContextBreakdown != nil {
			payload.Tokens = final.ContextBreakdown.TotalTokens
		}
	}
	enc := json.NewEncoder(s.out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// writeBlock writes a whole reply with exactly one trailing newline, so
// `wi -p ... | wc -l` counts the reply and not the renderer's habits.
func (s *session) writeBlock(text string) {
	if text == "" {
		return
	}
	fmt.Fprint(s.out, strings.TrimRight(text, "\n"), "\n")
}

func (s *session) endLine() {
	fmt.Fprintln(s.out)
}

// note writes an advisory line to the notes stream, which --quiet has already
// pointed at io.Discard if the user asked for the reply and nothing else.
func (s *session) note(format string, args ...any) {
	fmt.Fprintf(s.notes, format, args...)
}
