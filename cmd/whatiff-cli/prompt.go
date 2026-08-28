package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/engine"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/oneshot"
	"golang.org/x/term"
)

// maxStdinBytes bounds what a pipe can push into a single turn. The server
// rejects an over-long message anyway (handlerutils.TextLimitHardMax), and
// reading an unbounded pipe first would mean a `cat /dev/zero | wi -p` that
// dies of memory exhaustion instead of an error.
const maxStdinBytes = 8 << 20 // 8MB

// runPrompt is `wi -p "..."`: one turn, no TUI, then exit.
func runPrompt(ctx context.Context, profileName string, opts oneshot.Options) error {
	stdin, err := readPipedStdin(os.Stdin)
	if err != nil {
		return err
	}
	opts.Stdin = stdin
	// A human watching text appear wants it streamed; a pipe wants one clean
	// write. Deciding from the terminal-ness of stdout is what makes
	// `wi -p x` and `wi -p x | tee log` both behave the way they look.
	opts.Stream = term.IsTerminal(int(os.Stdout.Fd()))
	opts.Timezone = localTimezone()

	sess, err := newSession(profileName)
	if err != nil {
		return err
	}

	runner := &oneshot.Runner{
		Engine: engine.NewRemote(sess.client),
		Chats:  sess.client,
		Out:    os.Stdout,
		Err:    os.Stderr,
	}
	return runner.Run(ctx, opts)
}

// readPipedStdin returns whatever was piped in, or "" when stdin is a
// terminal.
//
// The terminal check is what keeps an interactive `wi -p "hello"` from
// hanging forever waiting for an EOF the user has no reason to think they
// owe it.
func readPipedStdin(in *os.File) (string, error) {
	info, err := in.Stat()
	if err != nil {
		// Statting stdin should not fail, but if it does, treating it as a
		// terminal is the safe guess: the cost is ignoring piped input, not
		// hanging on a pipe that will never close.
		return "", nil
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}

	raw, err := io.ReadAll(io.LimitReader(in, maxStdinBytes))
	if err != nil {
		return "", fmt.Errorf("reading piped input: %w", err)
	}
	return string(raw), nil
}

// localTimezone reports the IANA name of the machine's timezone, or "" when
// it cannot be named.
//
// $TZ wins because it is the one place a user can state the answer
// explicitly. time.Local's own name is the fallback, and it is deliberately
// not trusted blindly: Go names a zone loaded from /etc/localtime "Local",
// which is not an IANA name and would be worse than sending nothing.
func localTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	name := time.Local.String()
	if name == "" || name == "Local" {
		return ""
	}
	return name
}

// promptExitError maps a finished turn to what main should print and exit
// with. An interrupted turn is not a crash — the partial reply is on screen
// and saved server-side — so it gets a plain line rather than a wrapped error.
func promptExitError(err error) error {
	if errors.Is(err, oneshot.ErrInterrupted) {
		fmt.Fprintln(os.Stderr, "wi: interrupted — the partial reply was saved")
		return errSilentFailure
	}
	return err
}
