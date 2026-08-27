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
		switch {
		case errors.Is(err, errHelpRequested):
			// A subcommand's own FlagSet already printed its usage to stdout
			// (see parseSubFlags) - nothing left to do here but match the
			// exit code main's own --help handling above uses.
			os.Exit(0)
		case errors.Is(err, errFlagUsage):
			// A subcommand's own FlagSet already printed the parse error and
			// its usage to stderr (see parseSubFlags) - do not print err
			// again here, it would just duplicate that output.
			os.Exit(2)
		case errors.Is(err, context.Canceled):
			// Ctrl-C mid-request: the user asked for this, so it is not an
			// error worth a scary wrapped message - just say so and leave
			// with a non-zero status.
			fmt.Fprintln(os.Stderr, "wi: cancelled")
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "wi: %v\n", err)
			os.Exit(1)
		}
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

// errHelpRequested and errFlagUsage let a subcommand's FlagSet report the
// same two special outcomes main's own top-level flag parsing already
// handles above - a satisfied --help (exit 0, already printed to stdout) and
// a genuine bad-flag error (exit 2, already printed to stderr) - without
// falling through to run()'s generic "any error means exit 1". They carry no
// message of their own because parseSubFlags has already written whatever
// there was to write; main only needs to tell them apart from a normal error
// via errors.Is to pick the right exit code.
var (
	errHelpRequested = errors.New("help requested")
	errFlagUsage     = errors.New("flag usage error")
)

// parseSubFlags parses fs against args using exactly the stdout/exit-0
// (help) and stderr/exit-2 (bad flag) split main uses for its own top-level
// flags above. Every subcommand FlagSet must be parsed through this, not
// fs.Parse directly: fs.Parse alone prints to stderr and returns a plain
// error for BOTH -h/--help and a genuine mistake, which is what let
// `wi chats --help` print to stderr and exit 1 (via run()'s generic error
// handling) while `wi --help` printed to stdout and exited 0 twenty lines
// away in the same file - and let a bad flag print its message twice, once
// from flag's own default output and once from main's error wrapper.
func parseSubFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		printSubUsage(fs, os.Stdout)
		return errHelpRequested
	case err != nil:
		fmt.Fprintf(os.Stderr, "wi %s: %v\n", fs.Name(), err)
		printSubUsage(fs, os.Stderr)
		return errFlagUsage
	}
	return nil
}

// printSubUsage writes fs's usage to w, matching flag.FlagSet's own default
// usage format. It does not use fs.Usage()/fs.Usage - that field is nil
// unless a caller sets it, and fs.Parse's internal usage call already fired
// (harmlessly, into io.Discard) before parseSubFlags gets a chance to
// inspect the error and pick a destination stream.
func printSubUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, "Usage of %s:\n", fs.Name())
	fs.SetOutput(w)
	fs.PrintDefaults()
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
//
// A Resolve failure is wrapped with the config file path it was resolved
// against (even when that file does not exist yet - DefaultPath still names
// where it would live): without this, an error like `no profile named
// "nope" (available: none)` leaves a user with no config file wondering
// where "available" was even supposed to come from.
func loadProfile(profileName string) (string, config.Profile, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", config.Profile{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Profile{}, err
	}
	name, profile, err := cfg.Resolve(profileName)
	if err != nil {
		return "", config.Profile{}, fmt.Errorf("%w (config file: %s)", err, path)
	}
	return name, profile, nil
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
		if errors.Is(err, config.ErrNoCredentials) {
			return nil, fmt.Errorf("not logged in to profile %q at %s - run: wi --profile %s login", name, profile.APIURL, name)
		}
		return nil, err
	}

	c := client.New(profile.APIURL, client.Tokens{Access: creds.AccessToken, Refresh: creds.RefreshToken})
	// username is captured once, read-only, rather than closing over the
	// mutable creds variable and read-modify-writing it: two overlapping
	// refreshes (do's single-flight only dedupes within one Client, not
	// across separate goroutines racing this OnRefresh closure via
	// unrelated paths) would otherwise be mutating and reading the same
	// shared creds value with no synchronization. Building a fresh
	// config.Credentials value inside the closure means each call is
	// independent of every other call's timing.
	username := creds.Username
	c.OnRefresh = func(t client.Tokens) error {
		return store.Save(name, config.Credentials{
			AccessToken:  t.Access,
			RefreshToken: t.Refresh,
			Username:     username,
		})
	}
	return &session{profileName: name, profile: profile, client: c}, nil
}
