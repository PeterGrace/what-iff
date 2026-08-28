# WhatIff CLI/TUI Client — Design

**Date:** 2026-08-27
**Status:** Approved for planning
**Scope:** Phase 1 — a terminal client for the existing WhatIff backend

## Summary

`wi` is a terminal client for WhatIff, modeled on the interaction style of
Claude Code and Cline: an inline conversation that writes into normal terminal
scrollback, a persistent input box, slash commands, and full-screen overlays for
browsing chats, models, personalities, memories, and rituals.

It is a **pure client**: it consumes only endpoints that already exist, so it
neither touches `openapi.yaml` nor triggers the e2e SDK regeneration rule in
`AGENTS.md`. The assistant continues to run server-side; the CLI renders and
drives it. (One optional backend improvement is identified under Known gaps, but
nothing in this design depends on it.)

## Goals

- A terminal client that feels as immediate as the web app for everyday chat.
- Access to WhatIff's differentiators from the terminal: multi-provider model
  switching, personalities, rituals, and long-term memory.
- Scriptability: one-shot prompts, stdin piping, and `--json` output, so `wi`
  composes with other tools.
- A clean seam (`Engine`) so a future local-agent or local-tool-bridge mode can
  be added without restructuring the client.

## Non-goals (phase 1)

- **Local tool execution.** The assistant cannot read your files or run your
  commands. WhatIff's tools run server-side, and MCP servers are remote URLs
  (`internal/models/mcpserver.go:16`) dialed by the *model vendor*, not by the
  WhatIff backend. Giving the agent local access needs a new protocol; that is a
  later phase.
- **A local agent loop.** No provider calls from the CLI. Deferred deliberately:
  it would require re-implementing context assembly
  (`internal/agent/message_context_builder.go`) client-side, creating a second
  implementation that drifts from the server's.
- **External-auth deployments.** `auth.ExternalAuthenticatorProvider`
  (`internal/auth/external.go:32`) is nil in an unmodified build; v1 targets
  native JWT login only.
- Agent-job management, chat import/export, and mood/expression management.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| End state | Phase 1 only; build the seam, defer the rest | Avoids speculative design for a mode not yet chosen |
| Interaction shape | Inline scrollback + alt-screen overlays | Preserves scrollback, tmux copy, and piping; overlays give WhatIff's many resources a real browser |
| Placement | `cmd/whatiff-cli/` in this repo, fork-only for now | Can import `internal/models`, so client and server DTOs cannot drift |
| Framework | Bubble Tea + Bubbles + Lipgloss | Overlay model, widgets, and `teatest` for CI; hand-rolling costs weeks |
| Config format | TOML | `github.com/BurntSushi/toml` is already a direct dependency (`go.mod:7`) |
| Credential store | `0600` file, not OS keyring | Headless servers and containers are a primary CLI use case; keyring backends fail there |

## Architecture

```
cmd/whatiff-cli/main.go     flag parsing, subcommand dispatch, wiring
internal/cli/
  config/    profiles, config file, credential store
  client/    typed WhatIff REST client over internal/models
  engine/    Engine interface + RemoteEngine
  session/   current-chat state, history, slash-command dispatch
  tui/       Bubble Tea inline chat model
    render/  markdown -> ANSI, message formatting
    overlay/ alt-screen overlay programs
  oneshot/   non-interactive / pipe mode
```

Each package carries a `_PACKAGE_SUMMARY.md` per the anti-drift rule in
`docs/ARCHITECTURE_SUMMARY.md`. `client/` is the only package that speaks HTTP —
the same single-chokepoint discipline `web/app/e2e/sdk/client.ts` uses for the
e2e suite.

### The Engine seam

Everything above `engine/` is agnostic to where turns come from:

```go
type Engine interface {
    Send(ctx context.Context, chatID uuid.UUID, req TurnRequest) (*Turn, error)
    Resume(ctx context.Context, chatID, messageID uuid.UUID) (*Turn, error)
}

type Turn struct {
    Events <-chan Event   // Delta | Phase | Attachment | Done | Error
    Cancel func() error
}
```

`RemoteEngine` is the only implementation in phase 1. A `LocalEngine` can be
added later without touching the TUI.

Shipped in issue #4 as `internal/cli/engine`; see that package's
`_PACKAGE_SUMMARY.md` for the decisions the interface alone does not show.
Two additions to the sketch above: `Turn` also carries `ChatID`,
`UserMessageID` and `JobID` (a consumer needs the message id to `Resume`
later, and the job id for `--json`), and `Resume` returns `ErrNoActiveTurn`
when the turn it was asked about has already finished.

## Data flow: one turn

1. `POST /api/chat/{id}/chat-message` `{message, origin:"User"}` → `202 {id, job_id}`
2. Poll `GET /api/job/{job_id}`; ~300ms while deltas arrive, backing off to 1s when idle.
3. `Job.draft_deltas` is **cumulative**, not incremental. Track a rendered index
   and emit only the tail. The web client does exactly this at
   `web/app/src/app/features/chat/chat-session.service.ts:669-680`; mirror that
   logic rather than reinventing it.
4. At `inference_complete`, return the prompt to the user immediately. The
   remaining phases (`expression_complete` → `compaction_complete` → `complete`)
   finish in the background behind a subtle indicator. Blocking until `complete`
   would make the CLI feel slower than the browser for no benefit.
5. The finished turn is reconciled against the persisted assistant message —
   tool calls, attachments, the model/persona actually used, and the per-turn
   token breakdown.

   > **Superseded (issue #4).** Shipped reconciling through the job's
   > `result_id` and `GET /api/chat/chat-message/{id}`, not a
   > `?limit=N` listing. The server sets `result_id` atomically with
   > `inference_complete`, and again when a cancelled turn's partial draft is
   > promoted to a real message, so it is exact even when two turns in the
   > same chat overlap — which "the newest assistant message in the last N"
   > cannot be. A nil `result_id` means the turn produced no message at all
   > (cancelled before the first token) rather than "look further back". See
   > `watcher.reconcile` in `internal/cli/engine/poll.go`.

Job status order is defined in `internal/models/job.go:13-22`.

### Behaviors inherited from the backend

- **Interrupt is non-destructive.** `POST /api/job/{id}/cancel`; cancellation
  atomically promotes non-empty drafts to an assistant message. The UI says
  "interrupted — partial reply saved," because it was.
- **Crash recovery already has an endpoint.**
  `GET /api/chat/{chatId}/chat-message/{messageId}/active-job`
  (`internal/handlers/chat/chatmessage.go:141`) re-attaches to a non-terminal
  job, so a restarted `wi` resumes rather than orphaning a turn.
- **Failures anchor to the user turn.** A failed job sets `last_error_message`
  on the user message, giving the retry affordance a natural place in the
  transcript.

## UX surface

### Inline rendering plus overlays

One Bubble Tea program in two modes:

- `View()` renders **only** the ephemeral region: input box, status line, and
  the in-flight reply as it streams.
- Finalized messages are emitted with `tea.Println`, which writes *above* the
  ephemeral region into real scrollback. The transcript becomes genuine terminal
  history — greppable, copyable, pipeable — while the input box stays pinned.
- Overlays issue `tea.EnterAltScreen`, delegate `Update`/`View` to an overlay
  model, then `tea.ExitAltScreen` and apply the result. Scrollback is untouched
  because the alt-screen is a separate buffer.

### Commands

| Slash | Keybind | Behavior |
|---|---|---|
| `/chat` | `^P` | chat switcher overlay (search, archived section) |
| `/new [name]` | `^N` | new chat |
| `/model` | | model picker from `GET /api/model` |
| `/persona` | | personality picker |
| `/memory [q]` | `^K` | memory browser: search, filter by scope/date, pin |
| `/rituals`, `/<name>` | per-ritual | invoke a ritual |
| `/attach <path>` | | upload; also `@path` inline in a message |
| `/context` | | token breakdown for the last turn |
| `/retry` | `^R` | retry a failed turn |
| `/help`, `/quit` | `^D` | |

`^C` interrupts the turn and keeps the partial reply; a second `^C` quits. `Esc`
closes an overlay. `Enter` sends, `Alt+Enter` inserts a newline.

Ritual shortcuts read from the existing `Ritual.Hotkeys` field
(`internal/models/ritual.go:18`), so a ritual's shortcut is the same in the
browser and the terminal.

`/context` renders `ContextBreakdown` from `GET /api/chat/{id}/context` as a bar
chart of per-segment token estimates — system prompt, checkpoint summary,
scratchpad, memories, history, tool definitions. The endpoint already exists, so
this is nearly free and is one of the more useful things a terminal can show.

### Non-interactive mode

```bash
wi -p "summarize the deploy decision"     # one-shot, streams to stdout
git diff | wi -p "review this"            # stdin folded into the prompt
wi -p "..." --chat deploy-plan --json     # target a chat, structured output
wi chats --json | jq '.[].name'
wi memory search "postgres" --json
```

`--chat` accepts a chat UUID or a name; a name matching more than one chat is an
error that lists the candidates rather than a silent pick. Omitting `--chat`
creates a new chat for the turn.

Streams when stdout is a TTY, buffers when piped. Exit code reflects terminal
job status. `--json` emits `{chat_id, message, model, tokens, job_id}`.

As shipped (issue #4): `--quiet` prints the reply and nothing else, suppressing
the advisory lines (the id of a chat the turn created, attachment names) that
otherwise go to stderr. `tokens` comes from the assistant message's
`context_breakdown.total_tokens` and is omitted, not zeroed, when the server
captured no breakdown for the turn. A chat created for a turn is named after
the prompt's first line so it is findable in `wi chats` afterwards.

### Attachments

Assistant-generated images download from `GET /api/image-gallery/{id}` into
`~/.local/share/whatiff/files/` (XDG-respecting, configurable) and the path
prints inline. Uploads use `POST /api/chat/{id}/file-attachment` (multipart).

Non-image attachments are **not** downloadable in v1 — see Known gaps.

## Config and auth

`~/.config/whatiff/config.toml`, XDG-respecting, with profiles so one binary can
address a local dev stack and a hosted instance:

```toml
default_profile = "local"
download_dir = "~/.local/share/whatiff/files"

[profiles.local]
api_url = "http://localhost:8080/api"

[profiles.hosted]
api_url = "https://whatiff.chat/api"
```

Selected via `--profile` or `WHATIFF_PROFILE`. Each profile may set a default
model and personality.

Credentials live separately in `~/.config/whatiff/credentials.json`, mode `0600`,
one entry per profile.

`wi login` follows the discipline `cmd/create-superuser` established: interactive
password entry through `golang.org/x/term`, **no `--password` flag and no
credential environment variable**, so passwords never land in shell history or
process listings.

Access tokens last 2h and refresh tokens 14d (`internal/auth/jwt.go:14`). The
client refreshes transparently on a 401, single-flighted so concurrent requests
do not stampede `POST /api/user/refresh`. An expired refresh token yields a clear
"run `wi login`" message.

## Error handling

- **Transient poll failures** (502s, proxy errors, non-JSON bodies): log at
  debug, retry, keep the turn alive. `scripts/mock-e2e.sh:221-245` is the
  reference implementation — one bad response must never kill an in-flight reply.
- **401 mid-turn**: refresh once, resume polling, no visible interruption.
- **Job `failed`**: render `last_error_message` as an inline error block with a
  `^R` retry affordance.
- **Job `cancelled`**: report the partial reply was saved.
- **Server unreachable at startup**: name the profile and the URL tried. "Which
  server am I pointed at" is the most common CLI support question.
- **Quota/metering rejections**: surface the server's message verbatim.

## Testing

Four layers, all runnable without provider keys:

1. **`client/`** — `httptest` servers, table-driven per endpoint.
2. **`engine/`** — the highest-value surface. Fake job sequences covering:
   deltas arriving in chunks, the cumulative-array index trap, phase
   transitions, early return at `inference_complete`, failure, cancel-with-
   partial, and `Resume` re-attach. Pure logic; deserves real coverage.
3. **`tui/`** — `teatest` golden files for the inline renderer and each overlay.
4. **End-to-end** — `make cli-e2e`: `make db-up` + `make run-mock`, driving the
   real binary. Mirrors `scripts/mock-e2e.sh`; hermetic, no provider egress. Per
   `AGENTS.md`, this belongs in the Makefile rather than inlined into CI.

All of it rides the existing `make pre-commit` gate
(`fmt vet tidy test build check-no-local-models`).

## Known gaps

| Gap | Impact | Plan |
|---|---|---|
| No download route for non-image attachments (`internal/handlers/fileattachment` exposes only list and delete) | Code-interpreter CSVs are unreachable by any client | Separate small backend issue |
| Polling has a latency floor (~300ms per delta batch) | Slightly less live than browser SSE would be | Acceptable; an SSE endpoint is a possible later backend issue |
| Bubble Tea adds ~5 direct dependencies to a dependency-light repo | Upstream review friction | Fork-only for now |
| Importing `internal/models` ties CLI releases to the repo version | No independent release cadence | Accepted — it is why we chose in-repo placement |

## Open questions

None blocking. Phase 2 direction (local tool bridge vs. local agent) is
deliberately deferred until the client exists and its ergonomics are known.
