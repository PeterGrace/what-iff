// Package client is the only place the WhatIff CLI speaks HTTP.
//
// Responses decode into internal/models types so the CLI and the server cannot
// disagree about a payload shape — the same single-chokepoint discipline the
// Playwright e2e SDK uses (web/app/e2e/sdk/client.ts).
//
// The one documented exception is ChatPage (chat.go): the server wraps a
// listing in internal/models.PaginatedResponse, whose Results field is
// []any and so cannot decode into anything a caller could use directly.
// ChatPage is a hand-maintained mirror of that envelope's wire shape with
// Results typed as []models.Chat instead. See
// TestChatPageMatchesPaginatedResponseEnvelope (chat_test.go) for the test
// that guards the two from silently drifting apart.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// maxErrorBodySnippet caps how much of a non-envelope response body ends up
// in APIError.Snippet: enough to recognize a captive portal or proxy page,
// short enough not to dump a full HTML document into a terminal.
const maxErrorBodySnippet = 200

// maxSuccessBody caps how much of a 2xx response doJSON will read before
// decoding. A JSON API response is orders of magnitude smaller than this; the
// cap exists only to bound how much memory a broken or hostile server —
// answering 200 with an unbounded or infinite stream — can force the CLI to
// allocate. The error path has its own, much smaller cap for the same reason
// (see decodeAPIError); this one is larger because a legitimate success body
// can legitimately be sizable (a chat history, say).
const maxSuccessBody = 32 << 20 // 32MB

// APIError is a non-2xx response. Message is the server's own wording, which
// the CLI surfaces verbatim rather than inventing its own.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Snippet is a whitespace-collapsed, truncated view of the raw response
	// body. It is populated only when neither Message nor the deprecated
	// Error field yielded anything — e.g. a captive portal's HTML page, or a
	// proxy's plain-text error — so a CLI user without a network tab still
	// sees a clue about what actually came back instead of a bare status.
	Snippet string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	if e.Snippet != "" {
		return fmt.Sprintf("server returned %d: %s", e.Status, e.Snippet)
	}
	return fmt.Sprintf("server returned %d", e.Status)
}

// Tokens is a token pair held in memory by a Client.
type Tokens struct {
	Access  string
	Refresh string
}

// Client talks to one WhatIff server.
//
// New is the intended constructor: it trims a trailing slash from BaseURL so
// callers can pass either form, and sets a bounded HTTP client. Both fields
// are exported, so a struct literal like &Client{BaseURL: x} compiles but
// skips that normalization; doJSON falls back to http.DefaultClient rather
// than panicking when HTTP is left nil that way.
type Client struct {
	BaseURL string
	HTTP    *http.Client

	// OnRefresh persists a newly refreshed token pair. Optional.
	OnRefresh func(Tokens) error

	mu       sync.Mutex
	tokens   Tokens
	inflight chan struct{}
}

// New builds a client for baseURL (for example http://localhost:8080/api).
func New(baseURL string, t Tokens) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		// Timeout is a hard ceiling over the whole request — dial through
		// body read — and it outranks whatever deadline a caller's ctx
		// carries into doJSON; a longer per-call context does not extend it.
		// That will matter once a call streams a large body (e.g. milestone
		// 5's image downloads); HTTP is exported so a caller needing more
		// time can replace it with its own *http.Client.
		HTTP:   &http.Client{Timeout: 60 * time.Second},
		tokens: t,
	}
}

// Tokens returns the client's current token pair.
func (c *Client) Tokens() Tokens {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens
}

func (c *Client) accessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens.Access
}

// httpClient returns c.HTTP, falling back to http.DefaultClient for a Client
// built as a struct literal rather than via New — otherwise c.HTTP.Do panics
// on a nil receiver even though both fields it would take to construct one
// that way are exported and look like a legitimate way to build a Client.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// doJSON performs one request with no refresh handling. Callers that need
// transparent re-auth use do, below — Login and refreshTokens are the only
// exceptions, since neither has a token worth retrying yet.
//
// doJSON JSON-unmarshals the entire response body, so it cannot be used for a
// large binary payload such as milestone 5's image downloads — those need a
// separate streaming method.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	// A caller that passes "chat" instead of "/chat" would otherwise silently
	// build c.BaseURL + path into a wrong URL (e.g. ".../apichat"); normalize
	// so a missing leading slash can't produce a wrong-but-valid request.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.accessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s: %w", method, path, decodeAPIError(resp))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	// Read the full body rather than streaming it straight into the decoder:
	// json.Decoder.Decode can stop as soon as it has one complete value,
	// leaving a non-JSON body only partially drained and forcing the
	// transport to close the connection instead of reusing it. Reading first
	// also lets a decode failure be diagnosed against the raw bytes — a
	// captive portal's HTML page otherwise decodes as a bare JSON syntax
	// error with no hint that HTML came back at all.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSuccessBody))
	if err != nil {
		return fmt.Errorf("reading %s %s response: %w", method, path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if !looksLikeJSON(raw) {
			snip := snippet(raw)
			if snip == "" {
				snip = "empty response body"
			}
			return fmt.Errorf("%s %s: response was not JSON: %s: %w", method, path, snip, err)
		}
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// looksLikeJSON reports whether raw starts, after leading whitespace, with a
// JSON object or array. It tells "the server sent JSON that doesn't match our
// struct" apart from "the server didn't send JSON at all" (an HTML captive
// portal page, a proxy's plain-text error) so doJSON's error can name which
// one happened instead of surfacing a bare decode error either way.
func looksLikeJSON(raw []byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// decodeAPIError turns a non-2xx response into an *APIError. A body that is not
// the server's JSON error envelope (a proxy's HTML 502, say) still yields a
// useful error carrying the status and a snippet of what actually came back.
func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{Status: resp.StatusCode}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	// The body may exceed the 64KB cap; drain the remainder so the
	// connection's transport can be reused instead of forcing a close.
	_, _ = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return apiErr
	}
	var envelope models.ErrorResponse
	if json.Unmarshal(raw, &envelope) == nil {
		apiErr.Message = envelope.Message
		apiErr.Code = envelope.Code
		if apiErr.Message == "" {
			apiErr.Message = envelope.Error
		}
	}
	if apiErr.Message == "" {
		apiErr.Snippet = snippet(raw)
	}
	return apiErr
}

// snippet collapses raw's whitespace into single spaces, strips non-printable
// characters, and truncates the result to maxErrorBodySnippet runes, so an
// HTML proxy page or plain-text error shows up as a recognizable fragment in
// an error message instead of vanishing.
//
// The stripping is a security measure, not cosmetic: raw is untrusted,
// server-controlled bytes — from a proxy, a captive portal, or a spoofed or
// compromised host — and this snippet is printed straight to the user's
// terminal (a CLI today, a TUI from milestone 3 on). Without stripping, a
// hostile server could embed ANSI/terminal control sequences in its response
// body — clearing the screen, forging fake output, or renaming the terminal
// tab via an OSC sequence — turning a diagnostic aid into a terminal
// injection vector. unicode.IsPrint reports true for the ASCII space, so the
// single-space collapsing above survives; strings.Fields has already turned
// newlines and tabs into that same space.
func snippet(raw []byte) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, strings.Join(strings.Fields(string(raw)), " "))
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > maxErrorBodySnippet {
		return string(r[:maxErrorBodySnippet]) + "…"
	}
	return s
}

// do performs a request, refreshing the access token once and retrying if the
// server rejects it. Every caller outside this file uses do, not doJSON, so a
// two-hour access token expiring mid-session is invisible to the user.
//
// stale is read before doJSON, which re-reads the token itself under the
// mutex; a concurrent refresh landing in the gap between the two reads means
// stale may not be the token actually sent on the wire. That's a benign
// race, deliberately left alone: worst case is one extra 401 round trip, and
// fixing it would mean threading an explicit token through doJSON — breaking
// its signature and its 24 existing tests — for no user-visible gain.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	stale := c.accessToken()
	err := c.doJSON(ctx, method, path, body, out)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		return err
	}
	// No token was sent at all, so a fresh one wouldn't change anything —
	// refreshing here would just be an extra round trip against
	// /user/refresh with a refresh token that has nothing to do with this
	// 401 (an unauthenticated endpoint, a client with no session yet).
	if stale == "" {
		return err
	}

	refreshed, refreshErr := c.refreshTokens(ctx, stale)
	if !refreshed {
		// The exchange itself failed, or the caller's context was canceled
		// while waiting on another goroutine's exchange — either way there's
		// no new token to retry with. The bare original 401 is a bad error
		// to hand back here: it reads as "your access token is expired,"
		// which is exactly the thing refreshTokens just silently tried, and
		// failed, to fix, and it discards a context cancellation entirely.
		switch {
		case errors.Is(refreshErr, context.Canceled), errors.Is(refreshErr, context.DeadlineExceeded):
			// Let cancellation surface as itself — a caller checking
			// errors.Is(err, context.Canceled) (Ctrl-C) or
			// context.DeadlineExceeded (a timeout) should see that, not an
			// unrelated 401 that has nothing to do with why the request
			// didn't complete.
			return refreshErr
		case refreshErr != nil:
			// Wrap both with %w: the first keeps errors.As(*APIError)
			// working for a caller that inspects the original status or
			// message, the second keeps errors.Is/As working against the
			// refresh failure itself. The prose in between is the part a
			// human actually reads, and it needs to say the actionable
			// thing plainly — this is the 14-day refresh-token-expired path
			// every user eventually hits, and "run wi login" is the only
			// way out of it.
			return fmt.Errorf("%w: session could not be renewed, run `wi login` to sign in again: %w", err, refreshErr)
		default:
			// refreshTokens only returns (false, nil) in no known path
			// today, but fall back to the original error rather than a nil
			// one if that ever changes.
			return err
		}
	}
	// The exchange succeeded — the client now holds a usable access token in
	// memory — even if refreshErr is non-nil, which can only mean the
	// *persist* step (OnRefresh) failed. A failed write to the credentials
	// file must not fail this request: the retry below can still succeed,
	// and will keep succeeding for the rest of the process's in-memory
	// session even though the on-disk copy is stale. There is no logger
	// wired into this package yet (nothing else in internal/cli logs either)
	// and no other channel back to the caller that wouldn't also fail the
	// request, so refreshErr is deliberately dropped here rather than
	// invented a reporting path for; the alternative — failing the request —
	// is exactly the regression this function exists to avoid. Once a
	// logger does exist in this package, a silently-unpersisted refresh
	// belongs on a debug line here, not surfaced to the caller.
	_ = refreshErr
	return c.doJSON(ctx, method, path, body, out)
}
