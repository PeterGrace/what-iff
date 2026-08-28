package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestListChatsUnwrapsPaginationEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			t.Errorf("path = %q, want /chat", r.URL.Path)
		}
		if got := r.URL.Query().Get("archived"); got != "" {
			t.Errorf("archived = %q, want it omitted by default", got)
		}
		w.Write([]byte(`{
			"results": [
				{"name": "deploy plan"},
				{"name": "rust notes"}
			],
			"total_count": 2,
			"page": 1
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("len(page.Results) = %d, want 2", len(page.Results))
	}
	if page.Results[0].Name != "deploy plan" {
		t.Errorf("page.Results[0].Name = %q, want deploy plan", page.Results[0].Name)
	}
}

func TestListChatsPassesArchivedAndSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("archived") != "true" {
			t.Errorf("archived = %q, want true", q.Get("archived"))
		}
		if q.Get("search") != "deploy" {
			t.Errorf("search = %q, want deploy", q.Get("search"))
		}
		w.Write([]byte(`{"results":[],"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	if _, err := c.ListChats(context.Background(), ListChatsOptions{Archived: true, Search: "deploy"}); err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
}

// TestListChatsPassesLimit pins the limit query param: the server's default
// page size is 10 (internal/handlers/chat/chat.go), so a caller that asks
// for more than that must have its Limit actually reach the server rather
// than silently falling back to a truncated listing.
func TestListChatsPassesLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q, want 100", got)
		}
		w.Write([]byte(`{"results":[],"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	if _, err := c.ListChats(context.Background(), ListChatsOptions{Limit: 100}); err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
}

// TestListChatsZeroLimitOmitsParam pins the deliberate choice, documented on
// ListChatsOptions.Limit and ListChats, that a zero Limit sends no limit
// param at all rather than inventing a client-side default — the caller
// (the wi chats command, in the next task) owns that policy.
func TestListChatsZeroLimitOmitsParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["limit"]; ok {
			t.Errorf("limit param present = %q, want it omitted for a zero Limit", r.URL.Query().Get("limit"))
		}
		w.Write([]byte(`{"results":[],"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	if _, err := c.ListChats(context.Background(), ListChatsOptions{}); err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
}

// TestListChatsRefreshesExpiredToken pins ListChats going through do, not
// doJSON. Without transparent re-auth, ListChats would be the one resource
// call in this package that forces a user to re-run `wi login` the moment
// their access token expires mid-session, instead of refreshing silently
// like every other call.
func TestListChatsRefreshesExpiredToken(t *testing.T) {
	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/chat":
			atomic.AddInt32(&chatCalls, 1)
			if r.Header.Get("Authorization") != "Bearer acc-2" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			w.Write([]byte(`{"results":[{"name":"deploy plan"}],"total_count":1,"page":1}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].Name != "deploy plan" {
		t.Fatalf("page.Results = %+v, want one chat named deploy plan", page.Results)
	}
	if got := atomic.LoadInt32(&chatCalls); got != 2 {
		t.Errorf("/chat called %d times, want 2 (one 401, one retry after refresh)", got)
	}
}

// TestListChatsDecodesTotalCount is the regression test for the whole point
// of ChatPage: total_count can exceed len(results) once a listing is capped
// by Limit, and a caller needs that number to say "showing 2 of 347" rather
// than presenting a truncated list that looks like the account only has 2
// chats.
func TestListChatsDecodesTotalCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"results": [
				{"name": "deploy plan"},
				{"name": "rust notes"}
			],
			"total_count": 347,
			"page": 1
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("len(page.Results) = %d, want 2", len(page.Results))
	}
	if page.TotalCount != 347 {
		t.Errorf("page.TotalCount = %d, want 347", page.TotalCount)
	}
}

// TestListChatsEmptyResults pins the "no chats" case: an explicit empty
// results array must decode to a nil/empty slice and no error, not a panic
// or a spurious error from an all-zero envelope.
func TestListChatsEmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 0 {
		t.Errorf("len(page.Results) = %d, want 0", len(page.Results))
	}
}

// TestListChatsMissingResultsField pins the case where the server omits
// "results" entirely rather than sending an empty array — a distinct wire
// shape from TestListChatsEmptyResults that must decode the same way: a
// nil/empty slice and no error, not a panic.
func TestListChatsMissingResultsField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 0 {
		t.Errorf("len(page.Results) = %d, want 0", len(page.Results))
	}
}

// TestListChatsNullResultsField pins a third wire shape for "no chats":
// "results" present but explicitly JSON null, rather than omitted or an
// empty array. encoding/json treats a null field the same as an absent one
// for a slice, but that equivalence is exactly the kind of thing worth
// pinning rather than assuming.
func TestListChatsNullResultsField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":null,"total_count":0,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(page.Results) != 0 {
		t.Errorf("len(page.Results) = %d, want 0", len(page.Results))
	}
}
