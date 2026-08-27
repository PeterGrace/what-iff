package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

// runLogin authenticates and stores the resulting token pair.
//
// Password entry is interactive only. There is deliberately no --password flag
// and no password environment variable, matching cmd/create-superuser: a flag
// would put the password in shell history and in every process listing on the
// machine.
func runLogin(ctx context.Context, profileName string) error {
	name, profile, err := loadProfile(profileName)
	if err != nil {
		return err
	}

	fmt.Printf("Logging in to %s (profile %q)\n", profile.APIURL, name)

	// One shared reader for the whole prompt sequence - see promptLine's doc
	// comment for why a fresh bufio.Reader per call is wrong.
	stdin := bufio.NewReader(os.Stdin)

	username, err := promptLine(stdin, "Username or email: ")
	if err != nil {
		return err
	}
	if err := validateUsername(username); err != nil {
		return err
	}

	password, err := promptPassword("Password: ")
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	c := client.New(profile.APIURL, client.Tokens{})
	user, err := c.Login(ctx, username, password)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	store, err := config.DefaultCredentialStore()
	if err != nil {
		return err
	}
	tokens := c.Tokens()
	if err := store.Save(name, config.Credentials{
		AccessToken:  tokens.Access,
		RefreshToken: tokens.Refresh,
		Username:     user.Username,
	}); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}

	fmt.Printf("Logged in as %s\n", user.Username)
	return nil
}

// validateUsername rejects an empty username before any network call is
// attempted.
//
// Pulled out of runLogin as its own function so this guard has a test
// pinning it directly (TestValidateUsername): runLogin itself talks to a
// real network and a real terminal, so it cannot be unit tested end to end,
// which would otherwise leave this check reachable only by a human running
// `wi login` and pressing enter at the first prompt.
func validateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}
	return nil
}

// promptLine prints prompt, then reads one line from r.
//
// r must be a single *bufio.Reader shared across every prompt in a sequence -
// never construct a fresh one per call. bufio.Reader reads ahead in chunks,
// so the first call can buffer bytes past its newline into its own internal
// buffer; a second, distinct bufio.Reader created for the next prompt starts
// with an empty buffer of its own and never sees those already-consumed
// bytes. Against a real interactive terminal this goes unnoticed because the
// terminal itself is line-buffered and delivers input one line at a time, so
// it happened to work "by luck" - but it silently drops input whenever stdin
// is piped or comes from a heredoc (exactly the shape of the regression test
// for this function). Do not "simplify" this back to bufio.NewReader(r) per
// call.
func promptLine(r *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// promptPassword reads without echoing. It requires a TTY: piping a password in
// would defeat the point of not having a flag for it.
func promptPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("password entry requires an interactive terminal")
	}

	// Discard anything already queued by the terminal driver before turning
	// off echo. A paste of "username\npassword\n" delivered as one write
	// leaves the password line sitting in the driver's input queue after the
	// username prompt above has consumed its own line - and that queued line
	// gets echoed to the screen the instant it arrives, before this function
	// ever runs, because echo is still on until term.ReadPassword below
	// turns it off. See flushPendingInput's doc comment (tty_tcflsh.go, with
	// tty_bsd.go and tty_other.go covering the rest of the build matrix) for
	// why this is the same defense sudo uses. The error return is ignored
	// deliberately: it is best-effort hardening, not a prerequisite for
	// reading a password, so a failing ioctl must not fail the whole login -
	// but discarded itself is still trustworthy even when flushPendingInput
	// also returned an error, since it reflects only what Poll observed, not
	// whether the flush that followed succeeded.
	discarded, _ := flushPendingInput(fd)
	if discarded {
		// This cannot undo the echo that already happened - only prevent the
		// stale bytes from being silently accepted as the password. Say so
		// plainly rather than letting a user assume the paste never reached
		// the screen.
		fmt.Fprintln(os.Stderr, "warning: discarded pending input before the password prompt - if you pasted your password it may be visible in your terminal history")
	}

	fmt.Print(prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
