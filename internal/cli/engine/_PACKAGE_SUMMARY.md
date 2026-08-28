# Package: `internal/cli/engine`

## Role

The seam between "a turn happened" and "where the turn came from". Everything
above it — the one-shot renderer today, the TUI from milestone 3 on — consumes
a `Turn`'s event stream and never learns whether the reply came from a remote
WhatIff server.

## Responsibilities

- Define the `Engine` interface, the `Turn` handle, and the closed `Event`
  union (`Delta`, `Phase`, `Attachment`, `Done`, `Error`).
- `RemoteEngine`: post a user turn, poll the job that carries the reply, and
  translate job snapshots into events.
- De-duplicate the server's cumulative draft-delta array into incremental
  `Delta` events.
- Reconcile the persisted assistant message once the reply is final.
- Tolerate transient poll failures; fail fast on permanent ones.
- Interrupt a turn (`Turn.Cancel`) and re-attach to one (`Engine.Resume`).

## Key types and entry points

- `Engine`, `TurnRequest`, `Turn`, `Event` (`Delta` / `Phase` / `Attachment` /
  `Done` / `Error`), `JobFailedError`, `ErrNoActiveTurn`.
- `NewRemote(API) *RemoteEngine`, `RemoteEngine.Send`, `RemoteEngine.Resume`.
- `IsTerminal(models.JobStatus)`, `ReplyIsFinal(models.JobStatus)`.
- `API` — the slice of `internal/cli/client` the engine needs, declared here
  so the poll loop is testable against a scripted fake instead of an HTTP
  server.

## Dependencies

- **Inbound:** `internal/cli/oneshot`, `cmd/whatiff-cli`.
- **Outbound:** `internal/cli/client`, `internal/models`, `github.com/google/uuid`,
  stdlib.

## Non-obvious decisions

- **`draft_deltas` is cumulative, not incremental.** Every poll returns the
  entire reply so far. `watcher.consumeDeltas` tracks a rendered index and
  emits only the tail; emitting the array wholesale reprints the whole reply
  on every poll and reads as a stutter bug. The browser does exactly this at
  `web/app/src/app/features/chat/chat-session.service.ts`
  (`handleJobProgressSnapshot`).
- **The index advances only when the array *grew* (`>`, not `>=`).** At
  `inference_complete` the server clears `draft_deltas`
  (`internal/agent/job_phase.go`, `persistInferencePhase`), so the array
  shrinks past the rendered index. Resyncing the index to the array length
  would rewind it to zero and replay the entire reply as the turn ended.
- **Status and deltas are both consumed on every snapshot, never `||`.** The
  opening poll of a turn routinely carries a status change *and* the first
  text; short-circuiting dropped the text on exactly those polls. Pinned by
  `TestFirstSnapshotEmitsBothPhaseAndDeltas`.
- **The turn ends at `inference_complete`, not `complete`.** The reply text is
  final there; `expression_complete` → `compaction_complete` → `complete` are
  expression picking and checkpointing, which the browser also lets finish in
  the background. Waiting for `complete` would make the terminal feel slower
  than the browser for nothing a reader can see. `ReplyIsFinal` is the single
  definition of that line, distinct from `IsTerminal`.
- **Reconciliation anchors on the job's `result_id`, not a search of recent
  messages.** The server sets it atomically with `inference_complete`, and
  again when a cancelled turn's partial draft is promoted to a real message
  (`internal/datastore/job.go`). That is exact even when two turns in the same
  chat overlap, which a "newest assistant message" heuristic cannot be. This
  is a deliberate improvement on the design doc, which specified reconciling
  through `GET /chat/{id}/chat-message?limit=N`.
- **A nil `result_id` means no message, not "look harder".** A turn cancelled
  before the first token produced nothing; reconciling to some older message
  would be worse than `Done{Message: nil}`.
- **A failed reconcile does not fail the turn.** The caller has already
  streamed the reply's text; losing a visible answer over a hiccup fetching
  its metadata is the wrong trade. `Done.Message` is nil and the caller falls
  back to the deltas it accumulated.
- **Poll interval is adaptive and backs off only after a few quiet polls.**
  Backing off on the first quiet poll would add up to a second to
  time-to-first-token on every turn, which is the one latency a terminal user
  feels.
- **Transient vs. permanent is decided by status.** 5xx/408/425/429 and any
  non-API error (dial failure, reset connection, a proxy's HTML page) are
  retried; 4xx is not, because it will stay wrong. A 401 has already survived
  the client's transparent token refresh by the time it arrives here, so it
  means the session is genuinely dead. This is the lesson
  `scripts/mock-e2e.sh`'s `poll_job` encodes.
- **`Turn.Cancel` uses `context.WithoutCancel`.** It exists to be called from
  a Ctrl-C handler, by which point the turn's own context is already
  cancelled — inheriting it would make the cancel request fail exactly when it
  is needed.
- **Cancel is non-destructive and is not an `Error`.** The server promotes a
  non-empty draft to a real assistant message, so a cancelled turn ends with
  `Done{Status: cancelled}` carrying the partial reply.
- **Poll intervals are struct fields, not constants,** so a test can compress
  a turn that would take seconds into one that takes milliseconds. A zero
  value falls back to the production default rather than to zero.

## Testing

`engine/` is pure logic driven by a scripted fake job sequence (`fake_test.go`),
which is what makes it the highest-value test surface in the CLI.

- `poll_test.go` — deltas arriving in chunks, the cumulative-array trap, the
  post-`inference_complete` clear, phase transitions, the early return at
  `inference_complete`, job failure, cancel-with-partial, cancel-before-first-
  token, transient-failure tolerance and its bound, fail-fast on a 404,
  context cancellation, attachments, reconcile failure, timezone forwarding.
- `resume_test.go` — re-attach and whole-draft replay, the 204 "nothing in
  flight" answer, and a lookup failure surfacing as itself.

## Related

- [Architecture summary](../../../docs/ARCHITECTURE_SUMMARY.md)
- [`internal/cli/client`](../client/_PACKAGE_SUMMARY.md) — the HTTP chokepoint
- [`internal/cli/oneshot`](../oneshot/_PACKAGE_SUMMARY.md) — the first consumer
- CLI design spec: `docs/superpowers/specs/2026-08-27-whatiff-cli-design.md`
