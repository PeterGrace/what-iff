package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

// TestParseSubFlags_Help pins the stdout/exit-0 half of the convention
// parseSubFlags exists to enforce: -h/--help is a satisfied request, not a
// usage error, and is reported via errHelpRequested so main can exit 0.
func TestParseSubFlags_Help(t *testing.T) {
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	fs.Bool("foo", false, "an example flag")

	err := parseSubFlags(fs, []string{"--help"})
	if !errors.Is(err, errHelpRequested) {
		t.Errorf("parseSubFlags(--help) = %v, want errHelpRequested", err)
	}
}

// TestParseSubFlags_UnknownFlag pins the stderr/exit-2 half: a genuine
// mistake is reported via errFlagUsage, distinct from errHelpRequested, so
// main can exit 2 instead of 0.
func TestParseSubFlags_UnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	fs.Bool("foo", false, "an example flag")

	err := parseSubFlags(fs, []string{"--bar"})
	if !errors.Is(err, errFlagUsage) {
		t.Errorf("parseSubFlags(--bar) = %v, want errFlagUsage", err)
	}
}

// TestParseSubFlags_CleanParse pins the third outcome: a flag set that
// parses without incident returns nil, not one of the two sentinels.
func TestParseSubFlags_CleanParse(t *testing.T) {
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	foo := fs.Bool("foo", false, "an example flag")

	if err := parseSubFlags(fs, []string{"--foo"}); err != nil {
		t.Fatalf("parseSubFlags(--foo) = %v, want nil", err)
	}
	if !*foo {
		t.Error("--foo did not set the flag")
	}
}

// TestPrintSubUsage pins that printSubUsage writes flag.FlagSet's own usage
// format (the "Usage of <name>:" header plus each registered flag) to
// whatever writer it is given, independent of os.Stdout/os.Stderr.
func TestPrintSubUsage(t *testing.T) {
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	fs.Bool("foo", false, "an example flag")

	var buf bytes.Buffer
	printSubUsage(fs, &buf)

	out := buf.String()
	if !strings.Contains(out, "Usage of sub:") {
		t.Errorf("printSubUsage output = %q, want it to contain %q", out, "Usage of sub:")
	}
	if !strings.Contains(out, "-foo") {
		t.Errorf("printSubUsage output = %q, want it to mention -foo", out)
	}
}

// TestUsage pins the top-level usage text: it must name both shipped
// commands and both global flags, since it is the only thing `wi --help` and
// `wi` (no args) print.
func TestUsage(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)

	out := buf.String()
	for _, want := range []string{"login", "chats", "--profile", "--json"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage() output does not mention %q:\n%s", want, out)
		}
	}
}

// TestRun_UnknownCommand pins run's default case: an unrecognized command
// name is a plain error (not one of the help/usage sentinels) naming what
// was asked for.
func TestRun_UnknownCommand(t *testing.T) {
	err := run(context.Background(), "bogus", nil, "", false)
	if err == nil {
		t.Fatal("run(bogus) = nil, want an error")
	}
	if errors.Is(err, errHelpRequested) || errors.Is(err, errFlagUsage) {
		t.Errorf("run(bogus) = %v, want a plain unknown-command error, not a sentinel", err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("run(bogus) error = %q, want it to mention %q", err.Error(), "bogus")
	}
}

// TestRun_DispatchesToLogin and TestRun_DispatchesToChats pin that run
// routes "login" and "chats" to their own FlagSets, not to each other's -
// each is exercised with a flag ("--archived") that only chats registers.
// Against login's flagset (which has none of its own) that is an unknown
// flag, errFlagUsage; against chats' flagset it parses cleanly and dispatch
// proceeds into newSession, which fails on the absence of stored
// credentials - a different, later error that could only be reached if
// --archived was accepted as a known flag. Neither path touches the network
// or a terminal, so both run without a live server.
func TestRun_DispatchesToLogin(t *testing.T) {
	err := run(context.Background(), "login", []string{"--archived"}, "", false)
	if !errors.Is(err, errFlagUsage) {
		t.Errorf("run(login, --archived) = %v, want errFlagUsage (login has no --archived flag)", err)
	}
}

func TestRun_DispatchesToChats(t *testing.T) {
	writeConfig(t, "")

	err := run(context.Background(), "chats", []string{"--archived"}, "", false)
	if errors.Is(err, errFlagUsage) || errors.Is(err, errHelpRequested) {
		t.Fatalf("run(chats, --archived) = %v, want --archived to be accepted as a known flag", err)
	}
	if err == nil {
		t.Fatal("run(chats, --archived) = nil, want an error (no stored credentials in this test's config dir)")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("run(chats, --archived) error = %q, want a not-logged-in error", err.Error())
	}
}

// TestRun_ChatsAcceptsProfileFlagAfterSubcommand pins the fix for `wi chats
// --profile foo` (and `wi chats --json`) previously failing with "flag
// provided but not defined": --profile is registered on chats' own FlagSet,
// not just main's top-level one, so a value given after "chats" is accepted
// at all — this test's substantive assertion is that run() doesn't return
// errFlagUsage, which is what "not defined" would have produced before the
// fix. loadProfile still fails past that point (no such profile in an empty
// config), which is expected and not what's under test here.
func TestRun_ChatsAcceptsProfileFlagAfterSubcommand(t *testing.T) {
	writeConfig(t, "")

	err := run(context.Background(), "chats", []string{"--profile", "nonesuch"}, "", false)
	if errors.Is(err, errFlagUsage) {
		t.Fatalf("run(chats, --profile nonesuch) = %v, want --profile accepted as a known flag on chats' own FlagSet", err)
	}
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Errorf("run(chats, --profile nonesuch) error = %v, want it to name the requested profile %q", err, "nonesuch")
	}
}

// TestRun_SubcommandProfileFlagOverridesTopLevel pins the "last one wins"
// resolution promised by chats.go's runChats doc comment: --profile is
// registered on both main's top-level FlagSet and chats' own, bound to the
// same variable, so a value given after "chats" must win over one given
// before it (they can't both apply - the variable holds one string). The
// resulting "no profile named" error names whichever profile actually took
// effect, so asserting which name appears in it is a direct test of which
// flag won.
func TestRun_SubcommandProfileFlagOverridesTopLevel(t *testing.T) {
	writeConfig(t, "")

	err := run(context.Background(), "chats", []string{"--profile", "from-subcommand"}, "from-top-level", false)
	if err == nil {
		t.Fatal("run(...) = nil, want an error naming the unknown profile")
	}
	if !strings.Contains(err.Error(), "from-subcommand") {
		t.Errorf("error = %q, want it to name %q (the subcommand-level flag, which must win)", err.Error(), "from-subcommand")
	}
	if strings.Contains(err.Error(), "from-top-level") {
		t.Errorf("error = %q, unexpectedly still names the top-level value %q, which the subcommand flag should have overridden", err.Error(), "from-top-level")
	}
}

// TestRun_TopLevelProfileFlagAppliesWhenSubcommandOmitsIt pins the other
// half of the same contract: when the subcommand doesn't repeat --profile,
// the value the top-level parse already set must survive untouched into
// runChats, not get reset to chats' own FlagSet default ("").
func TestRun_TopLevelProfileFlagAppliesWhenSubcommandOmitsIt(t *testing.T) {
	writeConfig(t, "")

	err := run(context.Background(), "chats", nil, "from-top-level", false)
	if err == nil {
		t.Fatal("run(...) = nil, want an error naming the unknown profile")
	}
	if !strings.Contains(err.Error(), "from-top-level") {
		t.Errorf("error = %q, want it to name %q (the top-level flag, unmodified by chats' own FlagSet)", err.Error(), "from-top-level")
	}
}
