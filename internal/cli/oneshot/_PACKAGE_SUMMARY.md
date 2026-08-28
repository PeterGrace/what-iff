# Package: `internal/cli/oneshot`

## Role

Non-interactive mode: `wi -p "..."`, and the piped form
`git diff | wi -p "review this"`. One turn, no TUI, then exit.

## Responsibilities

- Fold the `-p` argument and piped stdin into a single prompt.
- Resolve `--chat` (a UUID or a name) to a chat, or create one.
- Consume a `Turn`'s event stream and render it: streamed to a terminal,
  buffered to a pipe, or as a single JSON object.
- Decide the process's exit status from the terminal job status.

## Key types and entry points

- `Runner{Engine, Chats, Out, Err}`, `Runner.Run(ctx, Options) error`.
- `Options` — `Prompt`, `Stdin`, `ChatRef`, `Stream`, `JSON`, `Quiet`,
  `Timezone`, `Now`.
- `FoldPrompt(prompt, stdin)`, `ChatNameForPrompt(prompt, now)`.
- `ErrInterrupted`, `AmbiguousChatError`.
- `Chats` — the slice of `internal/cli/client` needed to resolve a chat.

## Dependencies

- **Inbound:** `cmd/whatiff-cli`.
- **Outbound:** `internal/cli/engine`, `internal/cli/client`,
  `internal/models`, `github.com/google/uuid`, stdlib.

## Non-obvious decisions

- **The blank line in `FoldPrompt` is load-bearing.**
  `git diff | wi -p "review this"` has to read as an instruction followed by a
  document; a diff jammed onto the end of a sentence reads as neither. The
  separator appears only when there are in fact two things to separate.
- **A generated chat is named after the prompt's first line,** not a
  timestamp, because that name is what the user scans for in `wi chats`
  later. The timestamp is the fallback for a prompt with no printable text.
- **Generated names are control-character-stripped,** the same way
  `sanitizeCell` treats the chats table: the name round-trips through the API
  and back into a terminal.
- **A `--chat` value that parses as a UUID is used without a lookup.** It is
  unambiguous by construction, and confirming it would only turn a clear
  server-side 404 into an earlier, less obvious one.
- **A name is matched exactly (case-insensitively) against the search
  results.** The server's chat search also matches checkpoint summaries, so
  its results are a superset of what `--chat` meant; without the narrowing,
  `--chat deploy` could resolve to a chat that merely mentions a deploy.
- **An ambiguous name is an error listing the candidates, never a silent
  pick.** A silently-wrong target sends a message somewhere the user will not
  think to look.
- **Name lookup covers active chats only.** The error says so and points at
  passing an id, rather than quietly widening the search to archived threads.
- **The persisted message wins over the accumulated deltas.** A turn that
  finished inside a single poll never produced deltas at all; the deltas are a
  rendering aid, and the conversation contains the persisted message. The
  deltas are the fallback for when reconciliation failed.
- **Streaming vs. buffering is decided by the caller** (`Options.Stream`),
  which `cmd/whatiff-cli` sets from whether stdout is a terminal. `--json`
  always buffers: a payload with the reply also dribbled out ahead of it is
  not parseable.
- **`--quiet` is implemented by pointing the notes stream at `io.Discard`**
  (`Runner.session`), so the flag is enforced in one place instead of at
  every call site.
- **Advisory lines go to stderr, the reply to stdout.** `wi -p ... >
  answer.txt` must capture the answer and nothing else — including the
  "Started chat <id>" line, which is not decoration: without the id, a turn
  that created a chat leaves no way to continue it.
- **`tokens` is omitted, not zeroed, when the server captured no context
  breakdown.** A `0` would read as "this turn cost nothing".
- **An interrupted turn still writes the partial reply and still exits
  non-zero.** The server saved that partial too; the non-zero status is so a
  script does not mistake it for a complete answer.

## Testing

- `prompt_test.go` — `FoldPrompt`'s six shapes, `ChatNameForPrompt`'s
  derivation, truncation, and control-character stripping.
- `chat_test.go` — UUID passthrough without a lookup, exact and
  case-insensitive name matches, the checkpoint-summary superset narrowing,
  ambiguity, unknown names, and chat creation.
- `run_test.go` — streaming vs. buffered rendering, persisted-reply
  preference and delta fallback, the JSON payload and its omitted `tokens`,
  stderr routing, `--quiet`, attachments, cancelled and failed turns, the
  empty-prompt guard (which must not leave a stray chat behind), stdin
  folding, and chat targeting.

## Related

- [Architecture summary](../../../docs/ARCHITECTURE_SUMMARY.md)
- [`internal/cli/engine`](../engine/_PACKAGE_SUMMARY.md) — the turn machinery
- CLI design spec: `docs/superpowers/specs/2026-08-27-whatiff-cli-design.md`
