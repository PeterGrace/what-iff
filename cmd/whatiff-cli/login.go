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
	if username == "" {
		return fmt.Errorf("username is required")
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
	fmt.Print(prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
