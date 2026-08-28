package oneshot

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// maxChatNameRunes bounds a generated chat name. The server allows 200
// (the chat ent schema); a name derived from a prompt only has to be
// recognizable in a listing, and `wi chats` truncates its own column at 60.
const maxChatNameRunes = 60

// FoldPrompt combines the -p argument with whatever arrived on stdin.
//
// The blank line between them is deliberate: `git diff | wi -p "review this"`
// has to read to the model as an instruction followed by a document, and a
// diff jammed onto the end of a sentence reads as neither. Either half may be
// empty — `wi -p ""` with piped input sends just the input, and a -p with no
// pipe sends just the prompt — so the separator only appears when there are
// in fact two things to separate.
func FoldPrompt(prompt, stdin string) string {
	prompt = strings.TrimSpace(prompt)
	stdin = strings.TrimRight(stdin, "\n")

	switch {
	case prompt == "":
		return strings.TrimSpace(stdin)
	case strings.TrimSpace(stdin) == "":
		return prompt
	default:
		return prompt + "\n\n" + stdin
	}
}

// ChatNameForPrompt names the chat created for a turn that did not target
// one.
//
// It is the first line of the prompt rather than a bare timestamp because
// this name is what the user will later scan for in `wi chats` — "review this
// migration" is findable, "wi 2026-08-28 14:03" is not. The timestamp is the
// fallback for a prompt with no usable text at all (piped binary-ish input,
// say), which still needs a name because the server requires one.
func ChatNameForPrompt(prompt string, now time.Time) string {
	line := prompt
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	// Same stripping the chats table applies to server-supplied text: this
	// name round-trips through the API and back into a terminal, so control
	// characters must not survive the trip.
	line = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, strings.Join(strings.Fields(line), " "))

	if line == "" {
		return fmt.Sprintf("wi %s", now.Format("2006-01-02 15:04"))
	}
	if runes := []rune(line); len(runes) > maxChatNameRunes {
		return strings.TrimSpace(string(runes[:maxChatNameRunes])) + "…"
	}
	return line
}
