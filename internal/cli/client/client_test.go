package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestDoJSONDecodesSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer acc-1" {
			t.Errorf("Authorization = %q, want Bearer acc-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"deploy plan"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})

	var out struct {
		Name string `json:"name"`
	}
	if err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, &out); err != nil {
		t.Fatalf("doJSON returned %v", err)
	}
	if out.Name != "deploy plan" {
		t.Errorf("Name = %q, want deploy plan", out.Name)
	}
}

func TestDoJSONSurfacesServerErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"free tier message limit reached","code":"QUOTA"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", apiErr.Status)
	}
	if apiErr.Message != "free tier message limit reached" {
		t.Errorf("Message = %q, want the server's message verbatim", apiErr.Message)
	}
}

func TestDoJSONNonJSONErrorBodyStillReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>proxy error</html>"))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want 502", apiErr.Status)
	}
}

func TestDoJSONSendsBodyAndContentType(t *testing.T) {
	type payload struct {
		Message string `json:"message"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var got payload
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			// t.Fatalf from a handler goroutine cannot stop the test the way
			// it would from the test goroutine itself (FailNow requires
			// running on the test's own goroutine); report and bail out of
			// this handler invocation instead.
			t.Errorf("decoding request body: %v", err)
			return
		}
		if got.Message != "hello" {
			t.Errorf("request body Message = %q, want hello", got.Message)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodPost, "/thing", payload{Message: "hello"}, nil)
	if err != nil {
		t.Fatalf("doJSON returned %v", err)
	}
}

// TestDoJSONNilOutDiscardsBody pins the out==nil drain by observing its
// effect rather than its mechanism: a client that fails to fully read a
// response body before returning forces net/http to close the connection
// instead of reusing it, so a dropped drain shows up here as more than one
// connection across repeated calls to the same server. The body must be
// large — a small body (tens of bytes) still fits in the connection's read
// buffer and gets reused even when nothing explicitly drains it, so the
// mutation this test exists to catch would silently survive.
func TestDoJSONNilOutDiscardsBody(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 300<<10) // large enough that an undrained body prevents reuse

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(big)
	}))
	var newConns atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	for i := 0; i < 5; i++ {
		if err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil); err != nil {
			t.Fatalf("call %d: doJSON returned %v", i, err)
		}
	}

	if got := newConns.Load(); got != 1 {
		t.Errorf("new connections opened across 5 calls = %d, want 1 (an undrained body forces a fresh connection per call)", got)
	}
}

func TestDoJSONNoTokenOmitsAuthorizationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{})
	if err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil); err != nil {
		t.Fatalf("doJSON returned %v", err)
	}
}

func TestDoJSONNormalizesMissingLeadingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/thing" {
			t.Errorf("path = %q, want /thing", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	if err := c.doJSON(context.Background(), http.MethodGet, "thing", nil, nil); err != nil {
		t.Fatalf("doJSON returned %v", err)
	}
}

func TestDoJSONFallsBackToDeprecatedErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"legacy wording"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Message != "legacy wording" {
		t.Errorf("Message = %q, want the deprecated error field's wording", apiErr.Message)
	}
}

func TestDoJSONPrefersMessageOverDeprecatedErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"dup","message":"real msg"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Message != "real msg" {
		t.Errorf("Message = %q, want message to win over the deprecated error field", apiErr.Message)
	}
}

func TestDoJSONSurfacesServerErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"free tier message limit reached","code":"QUOTA"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Code != "QUOTA" {
		t.Errorf("Code = %q, want QUOTA", apiErr.Code)
	}
}

// TestDoJSONDrainsOversizedErrorBody pins the drain of whatever is left after
// decodeAPIError's 64KB-capped read, the same way TestDoJSONNilOutDiscardsBody
// pins the out==nil drain: by observing connection reuse across repeated
// calls rather than the drain call itself.
func TestDoJSONDrainsOversizedErrorBody(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 128<<10) // well past the 64KB read cap

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(big)
	}))
	var newConns atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	for i := 0; i < 5; i++ {
		err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("call %d: error = %v, want *APIError", i, err)
		}
	}

	if got := newConns.Load(); got != 1 {
		t.Errorf("new connections opened across 5 calls = %d, want 1 (the remainder past the 64KB cap must be drained)", got)
	}
}

// TestDoJSONTreatsNotModifiedAsError pins the 2xx-only success boundary using
// 304: net/http's client never auto-follows a 304 (it only follows
// 301/302/303/307/308), so this is deterministic without needing to disable
// redirect handling or supply a Location header.
func TestDoJSONTreatsNotModifiedAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusNotModified {
		t.Errorf("Status = %d, want 304", apiErr.Status)
	}
}

func TestDoJSONContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := c.doJSON(ctx, http.MethodGet, "/thing", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDoJSONErrorEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", apiErr.Status)
	}
	if apiErr.Message != "" {
		t.Errorf("Message = %q, want empty for an empty body", apiErr.Message)
	}
}

func TestDoJSONSuccessBodyNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>Sign in to WiFi</html>"))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	var out struct {
		Name string `json:"name"`
	}
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, &out)
	if err == nil {
		t.Fatal("doJSON returned nil error for a non-JSON 200 body")
	}
	if !strings.Contains(err.Error(), "Sign in to WiFi") {
		t.Errorf("error = %q, want it to include the response snippet", err.Error())
	}
	if !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %q, want a not-JSON hint", err.Error())
	}
}

func TestDoJSONNonJSONErrorBodyIncludesSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>502 Bad Gateway nginx</html>"))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway nginx") {
		t.Fatalf("error = %v, want it to include the response body snippet", err)
	}
}

func TestDoJSONUnrecognizedJSONErrorBodyIncludesSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"detail":"upstream refused"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "upstream refused") {
		t.Fatalf("error = %v, want it to include the response body snippet", err)
	}
}

// TestSnippetStripsTerminalControlSequences pins the security-relevant half
// of snippet: raw is untrusted, server-controlled bytes that end up printed
// straight to the user's terminal, so control sequences (ANSI/OSC escapes,
// BEL, backspace, NUL) must not survive into APIError.Snippet. Each case is
// something a hostile or compromised server could send to clear the screen,
// forge fake-looking output, or rename the terminal tab.
func TestSnippetStripsTerminalControlSequences(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "ansi color codes and clear screen",
			body: "\x1b[31mFAKE ERROR\x1b[0m\x1b[2J",
			want: "[31mFAKE ERROR[0m[2J",
		},
		{
			name: "osc sequence sets terminal title",
			body: "\x1b]0;pwned\x07rest",
			want: "]0;pwnedrest",
		},
		{
			name: "nul byte",
			body: "before\x00after",
			want: "beforeafter",
		},
		{
			name: "bell and backspace",
			body: "alert\x07\x08\x08x",
			want: "alertx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := New(srv.URL, Tokens{Access: "acc-1"})
			err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *APIError", err)
			}
			if apiErr.Snippet != tt.want {
				t.Errorf("Snippet = %q, want %q", apiErr.Snippet, tt.want)
			}
			for _, r := range apiErr.Snippet {
				if !unicode.IsPrint(r) {
					t.Errorf("Snippet %q contains non-printable rune %U", apiErr.Snippet, r)
				}
			}
		})
	}
}

func TestSnippetCollapsesWhitespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("line1\n\n\tline2   line3"))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Snippet != "line1 line2 line3" {
		t.Errorf("Snippet = %q, want whitespace collapsed to single spaces", apiErr.Snippet)
	}
}

func TestSnippetTruncatesLongBodies(t *testing.T) {
	body := strings.Repeat("a", 250)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	want := strings.Repeat("a", 200) + "…"
	if apiErr.Snippet != want {
		t.Errorf("Snippet = %q, want 200 a's plus an ellipsis", apiErr.Snippet)
	}
}

// TestSnippetTruncatesByRuneNotByte uses a 3-byte-per-rune character so that
// a byte-indexed cap (instead of the intended rune-indexed one) lands
// mid-character at the 200-rune boundary and produces invalid UTF-8 — 200 is
// not a multiple of 3, so the misalignment is guaranteed rather than
// incidental.
func TestSnippetTruncatesByRuneNotByte(t *testing.T) {
	multibyte := strings.Repeat("中", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(multibyte))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if !utf8.ValidString(apiErr.Snippet) {
		t.Fatalf("Snippet = %q is not valid UTF-8", apiErr.Snippet)
	}
	want := strings.Repeat("中", 200) + "…"
	if apiErr.Snippet != want {
		t.Errorf("Snippet = %q, want 200 runes of 中 plus an ellipsis", apiErr.Snippet)
	}
}

// TestDoJSONSuccessValidJSONWrongShapeIsNotMisreportedAsNonJSON pins
// looksLikeJSON's actual job: telling "not JSON at all" apart from "JSON
// that doesn't match our struct". A response that is valid JSON but the
// wrong shape must not be reported with the not-JSON hint.
func TestDoJSONSuccessValidJSONWrongShapeIsNotMisreportedAsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":123}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	var out struct {
		Name string `json:"name"`
	}
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, &out)
	if err == nil {
		t.Fatal("doJSON returned nil error for JSON that doesn't match the target struct")
	}
	if strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %q, want it not to claim the response wasn't JSON", err.Error())
	}
}

func TestDoJSONSuccessEmptyBodyReportsWithoutDanglingColon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	var out struct {
		Name string `json:"name"`
	}
	err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, &out)
	if err == nil {
		t.Fatal("doJSON returned nil error for an empty 200 body")
	}
	if strings.Contains(err.Error(), ": :") {
		t.Errorf("error = %q, want no dangling colon for an empty snippet", err.Error())
	}
	if !strings.Contains(err.Error(), "empty response body") {
		t.Errorf("error = %q, want an explicit empty-body hint", err.Error())
	}
}

// TestClientWithoutNewFallsBackToDefaultHTTPClient pins httpClient's nil
// fallback: both Client fields it takes to build one this way are exported,
// so this compiles and must work rather than nil-panic on c.HTTP.Do.
func TestClientWithoutNewFallsBackToDefaultHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	if err := c.doJSON(context.Background(), http.MethodGet, "/thing", nil, nil); err != nil {
		t.Fatalf("doJSON returned %v, want a struct-literal Client to work without New", err)
	}
}

func TestDoRetriesOnceAfter401(t *testing.T) {
	var thingCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/thing":
			atomic.AddInt32(&thingCalls, 1)
			if r.Header.Get("Authorization") != "Bearer acc-2" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			w.Write([]byte(`{"name":"ok"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	var out struct {
		Name string `json:"name"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/thing", nil, &out); err != nil {
		t.Fatalf("do returned %v", err)
	}
	if out.Name != "ok" {
		t.Errorf("Name = %q, want ok", out.Name)
	}
	if got := atomic.LoadInt32(&thingCalls); got != 2 {
		t.Errorf("/thing called %d times, want 2 (one 401, one retry)", got)
	}
}

func TestDoDoesNotRetryTwice(t *testing.T) {
	var thingCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/refresh" {
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
			return
		}
		atomic.AddInt32(&thingCalls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil {
		t.Fatal("do returned nil error on a persistent 401, want error")
	}
	if got := atomic.LoadInt32(&thingCalls); got != 2 {
		t.Errorf("/thing called %d times, want 2 — one retry only, no loop", got)
	}
}

// TestDoSucceedsWhenPersistFails is the full-path regression test for design
// correction 1: OnRefresh fails to persist the refreshed tokens, but the
// exchange itself succeeded, so the retried request must still succeed
// rather than surfacing the original 401.
func TestDoSucceedsWhenPersistFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/thing":
			if r.Header.Get("Authorization") != "Bearer acc-2" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			w.Write([]byte(`{"name":"ok"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	c.OnRefresh = func(Tokens) error {
		return errors.New("read-only credentials file")
	}

	var out struct {
		Name string `json:"name"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/thing", nil, &out); err != nil {
		t.Fatalf("do returned %v, want success despite the persist failure", err)
	}
	if out.Name != "ok" {
		t.Errorf("Name = %q, want ok", out.Name)
	}
}

// TestRefreshExpiredRefreshTokenDoesNotRecurse pins refreshTokens calling
// doJSON rather than do: if it called do instead, a 401 from /user/refresh
// itself would trigger another refreshTokens call from inside the first
// one's do — which would find c.inflight already set by the outer call and
// block forever waiting for a channel that only the outer call (itself
// blocked on this same inner call) can close. With a real context.Background
// caller that deadlocks permanently; here it would eventually be released by
// the context deadline below, so a bounded elapsed-time check is what
// actually distinguishes "fixed promptly" from "only stopped because the
// test's own timeout intervened" — go test's pass/fail alone would not catch
// this mutation, since blocking until ctx expires still produces a non-nil
// error and exactly one /user/refresh call.
func TestRefreshExpiredRefreshTokenDoesNotRecurse(t *testing.T) {
	var refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"refresh token expired"}`))
		case "/thing":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Unauthorized"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	const budget = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	start := time.Now()
	err := c.do(ctx, http.MethodGet, "/thing", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("do returned nil error, want the original 401")
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("/user/refresh called %d times, want exactly 1 (no recursion)", got)
	}
	// A correct implementation resolves in well under a second; a deadlocked
	// one only returns once the context deadline fires, so an elapsed time
	// anywhere near budget means the deadlock happened and the deadline —
	// not the code — is what stopped it.
	if elapsed > budget/2 {
		t.Errorf("do took %s, want well under %s — this smells like it only stopped because the context deadline fired, i.e. a deadlock", elapsed, budget)
	}
}

// TestDoDoesNotRefreshWithoutToken pins the stale == "" guard in do: a
// client with no access token at all (never logged in) getting a 401 from
// some endpoint must not attempt a refresh — there is no session to refresh.
func TestDoDoesNotRefreshWithoutToken(t *testing.T) {
	var refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/refresh" {
			atomic.AddInt32(&refreshCalls, 1)
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{})
	err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil {
		t.Fatal("do returned nil error on a 401, want error")
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 0 {
		t.Errorf("/user/refresh called %d times, want 0 — no token means nothing to refresh", got)
	}
}

// TestDoConcurrentRequestsAllSucceed exercises the whole stack under load:
// many goroutines simultaneously hit an endpoint that 401s until the client
// refreshes, and every one of them must come back with a success — with
// /user/refresh hit exactly once, proving the single-flight guard covers the
// do path as well as direct refreshTokens calls (TestRefreshIsSingleFlight).
func TestDoConcurrentRequestsAllSucceed(t *testing.T) {
	var refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			time.Sleep(10 * time.Millisecond)
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/thing":
			if r.Header.Get("Authorization") != "Bearer acc-2" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			w.Write([]byte(`{"name":"ok"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	const n = 20
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct {
				Name string `json:"name"`
			}
			if err := c.do(context.Background(), http.MethodGet, "/thing", nil, &out); err != nil || out.Name != "ok" {
				atomic.AddInt32(&failures, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&failures); got != 0 {
		t.Errorf("%d of %d concurrent do calls failed, want 0", got, n)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("/user/refresh called %d times, want exactly 1", got)
	}
}
