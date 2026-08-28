# Package: `internal/cli/client`

## Role

The only place the `wi` CLI speaks HTTP to a WhatIff server.

## Responsibilities

- Issue authenticated JSON requests and decode responses into
  `internal/models` types.
- Convert non-2xx responses into an `*APIError` carrying the server's own
  message, or a sanitized snippet when the body isn't the server's JSON error
  envelope.
- Transparently refresh an expired access token and retry a request once.
- Resource calls: `Login` (`auth.go`), `ListChats`/`CreateChat` (`chat.go`),
  `CreateChatMessage`/`GetChatMessage`/`GetActiveChatMessageJob`/
  `ListChatMessages` (`chatmessage.go`), `GetJob`/`CancelJob` (`job.go`).

## Key types and entry points

- `Client`, `New(baseURL, tokens) *Client`, `Client.Tokens()`, `APIError`.
- `Client.do` — the method every resource call after `Login` uses; owns the
  401-refresh-and-retry behavior.
- `Client.doJSON` — one request, no refresh handling; used by `Login` (no
  token to refresh yet) and internally by `refreshTokens` (see below).
- `Client.Login`, `Client.ListChats`, `Client.CreateChat`, `ListChatsOptions`.
- `Client.CreateChatMessage`, `CreateMessageRequest`, `Client.GetChatMessage`,
  `Client.GetActiveChatMessageJob`, `Client.ListChatMessages`,
  `ListChatMessagesOptions`.
- `Client.GetJob`, `Client.CancelJob`.
- `Page[T]` and its aliases `ChatPage`, `ChatMessagePage` (`page.go`).

## Dependencies

- **Inbound:** `cmd/whatiff-cli`, `internal/cli/engine`,
  `internal/cli/oneshot`.
- **Outbound:** `internal/models`, `github.com/google/uuid`; otherwise stdlib
  `net/http`, `encoding/json`.

## Non-obvious decisions

- **Decoding into `internal/models` is why the CLI lives in this repo:** the
  client and server share the exact same request/response Go types, so they
  cannot silently disagree about a payload shape the way a separately
  vendored client could.
- **`Page[T]` is the one documented exception to that rule.** The server
  wraps a listing in `models.PaginatedResponse`, whose `Results []any` can't
  decode into anything usable — so `Page` hand-mirrors that envelope's wire
  shape instead, generic over the element type its aliases pin down
  (`ChatPage`, `ChatMessagePage`). Being a hand-maintained mirror rather than
  the real type means a json-tag change on `PaginatedResponse` would not be a
  compile error here; it's guarded instead by
  `TestPageMatchesPaginatedResponseEnvelope` (`page_test.go`), which marshals
  a real `models.PaginatedResponse` and unmarshals it into a `Page`, once per
  aliased element type, so the two decode compatibly rather than merely
  looking alike.
- **`do` vs. `doJSON`, and why `refreshTokens` must call `doJSON`.**
  `refreshTokens` (`auth.go`) is invoked from `do` on a 401. If it went
  through `do` instead of `doJSON` to hit `/user/refresh`, a refresh-token
  exchange that itself came back 401 would recurse into `refreshTokens`
  again — `doJSON` has no refresh logic, so this can't happen.
- **Single-flight refresh keyed on the stale access token.** `refreshTokens`
  takes the token the caller saw fail; if `c.tokens.Access` has already moved
  on by the time it acquires the lock, another goroutine refreshed first and
  it returns immediately. A concurrent second caller blocks on the leader's
  `done` channel instead of issuing its own `/user/refresh` call — a burst of
  concurrent 401s produces exactly one refresh request.
- **`refreshTokens` returns `(refreshed bool, err error)`**, not just `err`,
  because a failed *persist* (the `OnRefresh` write to the credential file —
  read-only filesystem, full disk) must not fail a request that already has
  usable tokens in memory. The exchange succeeding and the persist failing
  are reported as `(true, wrappedErr)`; `do` retries the request regardless.
- **A waiter infers the leader's outcome by re-reading the token,** not from
  a shared error value: comparing `c.tokens.Access` against the stale token
  it started with is enough to tell success from failure, without adding a
  second piece of state that would itself need synchronizing.
- **The leader's cleanup runs in a `defer` with `recover()`,** so a panic
  inside the refresh (e.g. a panicking `RoundTripper`) can't leave
  `c.inflight` set and `done` unclosed forever — which would wedge every
  future caller behind a channel nobody will ever close. The panic is
  re-raised after cleanup so it still propagates normally.
- **`close(done)` is ordered after token adoption**, not before: a waiter
  unblocks the instant `done` closes and immediately reads `c.tokens` to
  infer success. If the close could happen before the write, a waiter could
  observe the pre-refresh token and wrongly report failure.
- **A documented, deliberately-unfixed race:** `do` reads `stale :=
  c.accessToken()` before calling `doJSON`, which re-reads the token itself
  under the mutex. A refresh landing in that gap means `stale` may not be the
  token actually sent on the wire. Left alone because the worst case is one
  extra 401 round trip, and fixing it would mean threading an explicit token
  through `doJSON`, breaking its signature and existing tests for no
  user-visible gain.
- **`snippet` strips non-printable characters** from an error body before it
  reaches `APIError.Snippet`. The body is untrusted, server-controlled text
  (a proxy's HTML page, a captive portal) that gets printed straight to a
  terminal; without stripping, embedded ANSI/OSC escape sequences could forge
  output or rename the terminal tab.
- **Two different body caps for two different threat models:**
  `decodeAPIError` caps a non-2xx body at 64KB (just enough to recognize a
  captive portal or proxy page); `doJSON` caps a 2xx body at 32MB
  (`maxSuccessBody`) since a legitimate success body — a chat history, say —
  can legitimately be large, but an unbounded or infinite stream from a
  broken/hostile server still must not be allowed to force unbounded memory
  use.
- **`Page` carries `TotalCount` alongside `Results`** so a listing capped by
  the caller's `Limit` isn't mistaken for the complete set — see
  `page.go:Page` and `cmd/whatiff-cli/chats.go:truncationNotice`, which is the
  entire reason this type exists over a bare slice.
- **`doJSON` treats a 204 as "no body to decode", even with a non-nil `out`.**
  `GetActiveChatMessageJob` answers 204 for "no job is running for that turn",
  which is a normal answer; without the special case it surfaced as a bogus
  "response was not JSON: empty response body". The caller distinguishes the
  two by the zero `JobID` that a 204 leaves behind.
- **`CreateChatMessage` fills in `origin: "User"` itself** rather than
  exposing it on `CreateMessageRequest`. The server takes the origin from the
  request body, so leaving it to callers is a way to accidentally post a turn
  the transcript attributes to the assistant.
- **`CreateChat` sends only `{"name": ...}`,** not a marshaled `models.Chat`.
  The server falls back to the user's preferred model and personality when
  those fields are absent, but `models.Chat`'s `model_id`/`personality_id`
  tags carry no `omitempty`, so marshaling one would put explicit all-zero
  UUIDs on the wire where "unset" was meant.

## Testing

- `client_test.go` — success decode, error envelope, non-JSON error body,
  401-refresh-then-retry, no-double-retry.
- `auth_test.go` — login token storage, refresh persistence vs. in-memory
  success, single-flight behavior (run with `-race`).
- `chat_test.go` — pagination envelope unwrapping (including a null/missing
  `results` field) and query parameter construction.
- `page_test.go` — `TestPageMatchesPaginatedResponseEnvelope`, guarding
  `Page` against drift from `models.PaginatedResponse` for every aliased
  element type (see "Non-obvious decisions" above).
- `chatmessage_test.go` — the forced `origin: "User"`, the omitted empty
  timezone, hydrated-message decoding (including `context_breakdown`), the
  active-job 200 and 204 paths, message-listing limits, and `CreateChat`'s
  minimal body.
- `job_test.go` — `draft_deltas` and `result_id` decoding, a 404 surfacing as
  an `*APIError`, and `CancelJob` returning the job carrying the promoted
  partial reply.
- These behaviors were mutation-tested during milestone 1 review, particularly
  the refresh single-flight and retry-once logic.

## Related

- [Architecture summary](../../../docs/ARCHITECTURE_SUMMARY.md) — CLI as a
  second client (Frontend section).
