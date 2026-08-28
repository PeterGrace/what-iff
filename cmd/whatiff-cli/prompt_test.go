package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlagWasSetDistinguishesEmptyFromAbsent is the reason flagWasSet exists
// at all: `echo hi | wi -p ""` sends a piped document with no instruction of
// its own, and a check on the flag's *value* would treat that as "no -p" and
// print the usage text instead.
func TestFlagWasSetDistinguishesEmptyFromAbsent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "absent", args: []string{"chats"}, want: false},
		{name: "given with a value", args: []string{"-p", "hello"}, want: true},
		{name: "given empty", args: []string{"-p", ""}, want: true},
		{name: "given by its long name", args: []string{"--prompt", "hello"}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("wi", flag.ContinueOnError)
			fs.SetOutput(os.NewFile(0, os.DevNull))
			var prompt string
			fs.StringVar(&prompt, "p", "", "")
			fs.StringVar(&prompt, "prompt", "", "")
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse(%v) = %v", tc.args, err)
			}

			if got := flagWasSet(fs, "p", "prompt"); got != tc.want {
				t.Errorf("flagWasSet(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestReadPipedStdinReadsAPipe pins the piped half of `git diff | wi -p`.
func TestReadPipedStdinReadsAPipe(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer read.Close()

	go func() {
		defer write.Close()
		write.WriteString("diff --git a/x b/x\n")
	}()

	got, err := readPipedStdin(read)
	if err != nil {
		t.Fatalf("readPipedStdin returned %v", err)
	}
	if got != "diff --git a/x b/x\n" {
		t.Errorf("readPipedStdin = %q, want the piped bytes", got)
	}
}

// TestReadPipedStdinReadsARedirectedFile covers the other non-terminal shape
// stdin takes — `wi -p "review" < patch.diff` — which is a regular file, not
// a pipe.
func TestReadPipedStdinReadsARedirectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.diff")
	if err := os.WriteFile(path, []byte("+added line\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	got, err := readPipedStdin(f)
	if err != nil {
		t.Fatalf("readPipedStdin returned %v", err)
	}
	if got != "+added line\n" {
		t.Errorf("readPipedStdin = %q, want the file's contents", got)
	}
}

// TestReadPipedStdinIgnoresATerminal is the hang this guard exists to
// prevent: an interactive `wi -p "hello"` must not sit waiting for an EOF the
// user has no reason to think they owe it.
//
// /dev/null is not a character device on every platform, so the check is that
// a non-pipe input is not blocked on rather than a strict terminal assertion;
// the pipe cases above pin the positive half.
func TestReadPipedStdinIgnoresATerminal(t *testing.T) {
	tty, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer tty.Close()

	got, err := readPipedStdin(tty)
	if err != nil {
		t.Fatalf("readPipedStdin returned %v", err)
	}
	if got != "" {
		t.Errorf("readPipedStdin = %q, want empty", got)
	}
}

// TestLocalTimezonePrefersTZ pins the one place a user can state the answer
// explicitly.
func TestLocalTimezonePrefersTZ(t *testing.T) {
	t.Setenv("TZ", "Europe/Berlin")
	if got := localTimezone(); got != "Europe/Berlin" {
		t.Errorf("localTimezone() = %q, want Europe/Berlin", got)
	}
}

// TestLocalTimezoneRejectsGoPlaceholder pins the guard: Go names a zone
// loaded from /etc/localtime "Local", which is not an IANA name and would be
// worse to send than nothing at all.
func TestLocalTimezoneRejectsGoPlaceholder(t *testing.T) {
	t.Setenv("TZ", "")
	got := localTimezone()
	if got == "Local" {
		t.Error(`localTimezone() = "Local", want "" — that is Go's placeholder, not an IANA name`)
	}
}

// TestUsageDocumentsOneShotMode keeps the entry point discoverable: -p is the
// only way to hold a conversation until the TUI lands, and `wi` with no args
// prints exactly this text.
func TestUsageDocumentsOneShotMode(t *testing.T) {
	var buf strings.Builder
	usage(&buf)

	out := buf.String()
	for _, want := range []string{"-p", "--chat", "--quiet", "git diff | wi -p"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output does not mention %q:\n%s", want, out)
		}
	}
}
