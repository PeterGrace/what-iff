# Package: `cmd/whatiff-cli`

## Role

`main` package for `wi`, the WhatIff terminal client. Milestone 1 wires
`login` and `chats`; conversation handling arrives with the turn engine in
milestone 2.

## Responsibilities

- Top-level flag/subcommand dispatch (`--profile`, `--json`, `login`,
  `chats`, `help`) with a help/usage-error exit-code split that matches
  `flag`'s own convention (`main.go`).
- Build a `session` — resolved profile plus an authenticated
  `internal/cli/client.Client` wired to persist a refreshed token pair back
  to `internal/cli/config`.
- `login`: interactive username/password prompt, calls the client, stores the
  resulting tokens (`login.go`).
- `chats`: lists chats as a table or `--json`, with an advisory truncation
  notice (`chats.go`).
- Platform-specific terminal input handling for the password prompt
  (`tty_bsd.go`, `tty_other.go`, `tty_tcflsh.go`).

## Key types and entry points

- `main`, `run(ctx, command, args, profileName, asJSON)`.
- `session`, `newSession(profileName)`, `loadProfile(profileName)`.
- `runLogin(ctx, profileName, asJSON, args)`, `runChats(ctx, profileName, asJSON, args)`.
- `parseSubFlags`, `errHelpRequested`, `errFlagUsage` — shared subcommand
  flag-parsing that keeps `wi --help` and `wi chats --help` on the same
  stdout/exit-0 vs. stderr/exit-2 convention.
- `flushPendingInput(fd) (bool, error)` — one function, three platform
  implementations.

## Dependencies

- **Inbound:** none (this is a binary's `main` package).
- **Outbound:** `internal/cli/client`, `internal/cli/config`,
  `internal/models`; `golang.org/x/term`, `golang.org/x/sys/unix`.

## Non-obvious decisions

- **`--profile` and `--json` are registered twice: once on main's top-level
  FlagSet, once on each subcommand's.** `flag.FlagSet.Parse` stops at the
  first non-flag argument, so the top-level FlagSet alone can only ever see
  a global flag that precedes the subcommand name — `wi chats --json` failed
  with "flag provided but not defined" despite being the design spec's own
  documented usage (`docs/superpowers/specs/2026-08-27-whatiff-cli-design.md`).
  Each subcommand's `fs.StringVar(&profileName, ...)`/`fs.BoolVar(&asJSON,
  ...)` binds to the same variables the top-level parse already populated,
  so a flag given after the subcommand simply overwrites that value — last
  one given wins, regardless of which side of the subcommand name it's on.
  `login` accepts `--json` even though it has no JSON output mode, purely so
  both global flags behave identically after every subcommand.
- **Interactive password entry only** (`login.go`) — no `--password` flag, no
  environment variable, matching `cmd/create-superuser`. A flag would put the
  password in shell history and in every process listing on the machine.
- **A tty input flush runs immediately before the password prompt.** Pasting
  `alice\nhunter2\n` arrives at the terminal driver as one write and gets
  echoed to the screen as it lands, because local echo is still on until
  `term.ReadPassword` turns it off — by which point the password half has
  already been echoed into scrollback. `flushPendingInput` (the sudo trick)
  discards whatever is already queued by the kernel so that leftover line
  can't be silently *read back* as the password once echo is off — but it
  **cannot undo the echo that already happened**, which is why
  `promptPassword` prints an explicit warning to stderr when it discards
  something, rather than pretending the paste never reached the screen.
- **The per-platform ioctl split and the `tty_linux.go` trap.**
  `flushPendingInput` needs `TCFLSH` (linux/aix/solaris, `tty_tcflsh.go`) or
  `TIOCFLUSH` (the BSD family, `tty_bsd.go`) depending on what
  `golang.org/x/sys/unix` actually exports for the target GOOS — confirmed by
  cross-compiling, not by trusting a header reference. The file implementing
  the `linux || aix || solaris` build tag is deliberately **not** named
  `tty_linux.go`: a filename ending `_<goos>.go` carries an *implicit* build
  constraint for that GOOS, ANDed with any explicit `//go:build` line, so
  `tty_linux.go` with that tag would silently build for linux only and drop
  aix/solaris despite the tag naming them. `tty_other.go` is the no-op
  fallback for every remaining GOOS (Windows, Plan 9, js/wasm, Hurd); the
  three files' build tags are each other's exact negation.
- **`defaultChatLimit = 100`** (`chats.go`) overrides the server's own
  default page size of 10 (`internal/handlers/chat/chat.go`). Inheriting the
  server default would silently truncate a listing and look like a chat
  account with almost no chats.
- **Advisory output goes to stderr, not stdout.** The truncation notice
  (`"Showing N of M chats..."`) and the paste-discard warning are both
  printed to stderr so a pipe consuming stdout (`wi chats | grep`, a script
  parsing the table) never has to special-case a trailing sentence mixed into
  its data. `--json` mode never reaches the truncation notice at all —
  `TotalCount` is already in that payload for a script to check itself.
- **`sanitizeCell` mirrors the *stripping* half of `internal/cli/client`'s
  `snippet()`**, not the whole function, rather than importing it: both strip
  non-printable characters and collapse whitespace from server-controlled
  text (a chat name/model name here, an HTTP error body there) before it
  reaches a terminal, defending against the same ANSI/OSC terminal-injection
  risk. They're reimplemented separately because `internal/cli/client` is a
  thin HTTP transport with no terminal-rendering concern of its own. The two
  deliberately do NOT share a truncation width: `snippet()` caps at 200 runes
  (enough to recognize a captive-portal page), `sanitizeCell` at 60
  (`maxCellWidth`) — a table cell has to stay scannable, not just bounded, and
  chat/model names are already bounded server-side (`MaxLen(200)` in the chat
  ent schema) but that bound is still far too wide for a tabwriter column. See
  `chats.go:sanitizeCell`'s doc comment for what keeps the two in step if one
  changes.

## Testing

- `main_test.go` — profile resolution fallback and error-wrapping in
  `loadProfile`, `newSession`'s `OnRefresh` closure preserving the stored
  username across a refresh.
- `flags_test.go` — `parseSubFlags` (help sentinel, usage-error sentinel,
  clean parse), `printSubUsage`, top-level `usage`, and `run`'s dispatch to
  the right subcommand FlagSet (including the unknown-command case) — all
  pure functions over a `*flag.FlagSet`/`io.Writer` with no network or
  terminal dependency, at 100% coverage.
- `login_test.go` — `promptLine`'s shared-reader requirement (piped/heredoc
  input, not just an interactive terminal), `validateUsername`, and
  `runLogin`'s `--help`/unexpected-argument guard paths (both return before
  `loadProfile`, so they're reachable without a network or a terminal).
- `chats_test.go` — `truncationNotice`, `humanizeSince`, `sanitizeCell`,
  `normalizeResultsForJSON` (nil-vs-empty `results`).
- No test exercises `runLogin`'s interactive prompt/network path or
  `runChats` end to end — both talk to a real network and a real terminal,
  which is exactly what the milestone's live end-to-end check
  (`docs/superpowers/plans/2026-08-27-whatiff-cli-foundation.md`, Task 11)
  validates that automated tests can't.
- These behaviors were mutation-tested during milestone 1 review.

## Related

- [Architecture summary](../../docs/ARCHITECTURE_SUMMARY.md) — CLI as a
  second client (Frontend section).
