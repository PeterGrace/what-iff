package main

import (
	"bufio"
	"strings"
	"testing"
)

// TestPromptLine_SharedReaderSeesBothLines exercises promptLine reading two
// sequential lines off one shared *bufio.Reader.
//
// This is NOT a mutation test for a "fresh bufio.Reader per call"
// regression, despite an earlier version of this comment claiming it was:
// bufio.NewReader(rd) special-cases an rd that is already a *bufio.Reader
// with a large enough internal buffer and returns it unchanged rather than
// wrapping it (see the "Is it already a Reader?" check in
// bufio.NewReaderSize). So even a promptLine that did
// bufio.NewReader(r).ReadString('\n') internally on every call would still
// pass this test - r would just come back as itself, unwrapped, and behave
// identically. The real fix for the original bug is the function signature:
// promptLine takes a *bufio.Reader, not an io.Reader, which forces every
// caller to construct exactly one reader for a whole prompt sequence instead
// of re-wrapping raw stdin (an io.Reader that is NOT already a *bufio.Reader,
// so the short-circuit above does not apply to it) fresh on each call - that
// mismatch is what the original bug depended on. The signature makes the bug
// impossible to reintroduce by construction; this test only pins ordinary
// two-line behavior, not that construction.
func TestPromptLine_SharedReaderSeesBothLines(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("alice\nswordfish\n"))

	first, err := promptLine(r, "Username: ")
	if err != nil {
		t.Fatalf("first promptLine: %v", err)
	}
	if first != "alice" {
		t.Errorf("first = %q, want %q", first, "alice")
	}

	second, err := promptLine(r, "Password: ")
	if err != nil {
		t.Fatalf("second promptLine: %v", err)
	}
	if second != "swordfish" {
		t.Errorf("second = %q, want %q", second, "swordfish")
	}
}

func TestPromptLine_TrimsWhitespace(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("  spaced out  \n"))
	got, err := promptLine(r, "")
	if err != nil {
		t.Fatalf("promptLine: %v", err)
	}
	if got != "spaced out" {
		t.Errorf("got %q, want %q", got, "spaced out")
	}
}

// TestPromptLine_NoTrailingNewline covers input with no final newline (e.g.
// the last line before EOF), which io.Reader.ReadString reports as an error
// (io.EOF) alongside whatever it did manage to read.
func TestPromptLine_NoTrailingNewline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("noeol"))
	got, err := promptLine(r, "")
	if err != nil {
		t.Fatalf("promptLine: %v", err)
	}
	if got != "noeol" {
		t.Errorf("got %q, want %q", got, "noeol")
	}
}

func TestPromptLine_EmptyInputErrors(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := promptLine(r, "")
	if err == nil {
		t.Fatal("expected an error reading from an empty/exhausted reader")
	}
}

// TestValidateUsername is the direct test for the guard runLogin applies
// before ever making a network call or prompting for a password - see
// validateUsername's doc comment for why it is unit tested on its own rather
// than only indirectly, by running the whole interactive login flow.
func TestValidateUsername(t *testing.T) {
	if err := validateUsername("alice"); err != nil {
		t.Errorf("validateUsername(%q) = %v, want nil", "alice", err)
	}
	if err := validateUsername(""); err == nil {
		t.Error("validateUsername(\"\") = nil, want an error rejecting the empty username")
	}
}
