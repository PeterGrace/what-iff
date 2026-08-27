// Command whatiff-cli (installed as `wi`) is a terminal client for WhatIff.
//
// Milestone 1 provides `login` and `chats`. Conversation handling arrives with
// the turn engine in milestone 2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

func main() {
	// Ctrl-C (and a container's SIGTERM) cancel ctx rather than killing the
	// process outright. The client package already propagates a canceled
	// context correctly through the token-refresh path (see
	// internal/cli/client/client.go's do/refreshTokens) - without this wiring
	// that plumbing had nothing to cancel it, so Ctrl-C just killed the
	// process mid-request instead of letting a request unwind cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A dedicated FlagSet, not the package-level flag.CommandLine, so -h/--help
	// can be routed to stdout+exit(0) below instead of flag's own default of
	// stderr+exit(2): that default is right for a genuine usage error but
	// wrong for someone deliberately asking for help. Output is discarded here
	// so flag never prints on our behalf; both branches below print exactly
	// once, to the stream the case calls for.
	fs := flag.NewFlagSet("wi", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", os.Getenv("WHATIFF_PROFILE"), "config profile to use")
	asJSON := fs.Bool("json", false, "emit JSON instead of human-readable output")

	switch err := fs.Parse(os.Args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		// -h / -help / --help (flag treats one and two leading dashes the
		// same): an explicit request for help, not a mistake. Exit 0.
		usage(os.Stdout)
		os.Exit(0)
	case err != nil:
		fmt.Fprintf(os.Stderr, "wi: %v\n", err)
		usage(os.Stderr)
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	if args[0] == "help" {
		usage(os.Stdout)
		os.Exit(0)
	}

	if err := run(ctx, args[0], args[1:], *profile, *asJSON); err != nil {
		if errors.Is(err, context.Canceled) {
			// Ctrl-C mid-request: the user asked for this, so it is not an
			// error worth a scary wrapped message - just say so and leave
			// with a non-zero status.
			fmt.Fprintln(os.Stderr, "wi: cancelled")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wi: %v\n", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `wi - WhatIff terminal client

Usage:
  wi [flags] <command> [args]

Commands:
  login    Authenticate against the configured profile
  chats    List your chats

Flags:
  --profile <name>   Config profile (default: $WHATIFF_PROFILE, then config)
  --json             Emit JSON instead of human-readable output
`)
}

func run(ctx context.Context, command string, args []string, profileName string, asJSON bool) error {
	switch command {
	case "login":
		return runLogin(ctx, profileName)
	case "chats":
		return runChats(ctx, profileName, asJSON, args)
	default:
		return fmt.Errorf("unknown command %q (try: login, chats)", command)
	}
}

// session bundles the resolved profile and an authenticated client.
type session struct {
	profileName string
	profile     config.Profile
	client      *client.Client
}

// loadProfile resolves configuration without requiring credentials. Used by
// login, which is how credentials come to exist in the first place.
func loadProfile(profileName string) (string, config.Profile, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", config.Profile{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Profile{}, err
	}
	return cfg.Resolve(profileName)
}

// newSession resolves configuration and builds a client carrying stored
// credentials, wired so a refreshed token pair is written back to disk.
func newSession(profileName string) (*session, error) {
	name, profile, err := loadProfile(profileName)
	if err != nil {
		return nil, err
	}
	store, err := config.DefaultCredentialStore()
	if err != nil {
		return nil, err
	}
	creds, err := store.Load(name)
	if err != nil {
		if err == config.ErrNoCredentials {
			return nil, fmt.Errorf("not logged in to profile %q at %s - run: wi --profile %s login", name, profile.APIURL, name)
		}
		return nil, err
	}

	c := client.New(profile.APIURL, client.Tokens{Access: creds.AccessToken, Refresh: creds.RefreshToken})
	c.OnRefresh = func(t client.Tokens) error {
		creds.AccessToken = t.Access
		creds.RefreshToken = t.Refresh
		return store.Save(name, creds)
	}
	return &session{profileName: name, profile: profile, client: c}, nil
}
