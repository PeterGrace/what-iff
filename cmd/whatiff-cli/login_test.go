package main

import (
	"bufio"
	"strings"
	"testing"
)

// TestPromptLine_SharedReaderSeesBothLines is the regression test for
// Correction 2: a *bufio.Reader created fresh on every promptLine call can
// buffer bytes past the first newline and then discard them, since the next
// call's reader starts with an empty buffer of its own. It fails against a
// promptLine that does bufio.NewReader(r).ReadString('\n') internally on each
// call, and passes when a single reader is threaded through both prompts.
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
