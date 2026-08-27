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
	"sync/atomic"
	"testing"
	"time"
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
// connection across repeated calls to the same server.
func TestDoJSONNilOutDiscardsBody(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"deploy plan"}`))
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
