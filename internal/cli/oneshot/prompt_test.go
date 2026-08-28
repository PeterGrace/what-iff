package oneshot

import (
	"strings"
	"testing"
	"time"
)

func TestFoldPrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		stdin  string
		want   string
	}{
		{
			name:   "prompt only",
			prompt: "summarize the deploy decision",
			want:   "summarize the deploy decision",
		},
		{
			// `echo hi | wi -p ""` — the pipe carries the whole prompt.
			name:  "stdin only",
			stdin: "the whole prompt\n",
			want:  "the whole prompt",
		},
		{
			// The blank line is what makes `git diff | wi -p "review this"`
			// read as an instruction followed by a document.
			name:   "both are separated by a blank line",
			prompt: "review this",
			stdin:  "diff --git a/x b/x\n+added\n",
			want:   "review this\n\ndiff --git a/x b/x\n+added",
		},
		{
			name:   "whitespace-only stdin is not a document",
			prompt: "hello",
			stdin:  "\n\n  \n",
			want:   "hello",
		},
		{
			name: "both empty",
			want: "",
		},
		{
			// Interior blank lines belong to the piped document and must
			// survive; only the trailing newline a shell adds is trimmed.
			name:   "interior blank lines survive",
			prompt: "review",
			stdin:  "one\n\ntwo\n",
			want:   "review\n\none\n\ntwo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FoldPrompt(tc.prompt, tc.stdin); got != tc.want {
				t.Errorf("FoldPrompt(%q, %q) = %q, want %q", tc.prompt, tc.stdin, got, tc.want)
			}
		})
	}
}

func TestChatNameForPrompt(t *testing.T) {
	now := time.Date(2026, 8, 28, 14, 3, 0, 0, time.UTC)

	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "first line names the chat",
			prompt: "review this migration\n\ndiff --git a/x b/x",
			want:   "review this migration",
		},
		{
			name:   "whitespace is collapsed",
			prompt: "  review    this \t migration  ",
			want:   "review this migration",
		},
		{
			// A prompt with nothing printable still needs a name, because
			// the server requires one.
			name:   "unusable prompt falls back to a timestamp",
			prompt: "\x00\x01\x02",
			want:   "wi 2026-08-28 14:03",
		},
		{
			name:   "empty prompt falls back to a timestamp",
			prompt: "",
			want:   "wi 2026-08-28 14:03",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChatNameForPrompt(tc.prompt, now); got != tc.want {
				t.Errorf("ChatNameForPrompt(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// TestChatNameForPromptTruncates pins the bound. The name is scanned in a
// listing, so a 400-character first line has to be cut somewhere.
func TestChatNameForPromptTruncates(t *testing.T) {
	long := strings.Repeat("a", 400)

	got := ChatNameForPrompt(long, time.Now())
	if runes := []rune(got); len(runes) > maxChatNameRunes+1 {
		t.Errorf("len(name) = %d runes, want at most %d plus the ellipsis", len(runes), maxChatNameRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("name = %q, want a trailing ellipsis to show it was cut", got)
	}
}

// TestChatNameForPromptStripsControlCharacters keeps a name that round-trips
// through the API and back into a terminal from carrying escape sequences —
// the same protection sanitizeCell gives the chats table.
func TestChatNameForPromptStripsControlCharacters(t *testing.T) {
	got := ChatNameForPrompt("deploy \x1b[31mplan\x1b[0m", time.Now())
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("name = %q, want the escape sequences stripped", got)
	}
}
