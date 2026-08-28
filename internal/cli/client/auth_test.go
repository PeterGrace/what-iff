package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestLoginStoresTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/login" {
			t.Errorf("path = %q, want /user/login", r.URL.Path)
			return
		}
		var req models.UserLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request body: %v", err)
			return
		}
		if req.Username != "pete" || req.Password != "hunter2" {
			t.Errorf("credentials = %q/%q, want pete/hunter2", req.Username, req.Password)
		}
		json.NewEncoder(w).Encode(models.LoginResponse{
			AccessToken:  "acc-1",
			RefreshToken: "ref-1",
			User:         models.UserResponse{Username: "pete"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{})
	user, err := c.Login(context.Background(), "pete", "hunter2")
	if err != nil {
		t.Fatalf("Login returned %v", err)
	}
	if user.Username != "pete" {
		t.Errorf("Username = %q, want pete", user.Username)
	}
	if got := c.Tokens(); got.Access != "acc-1" || got.Refresh != "ref-1" {
		t.Errorf("tokens = %+v, want acc-1/ref-1", got)
	}
}

func TestRefreshReplacesTokensAndPersists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req models.RefreshTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request body: %v", err)
			return
		}
		if req.RefreshToken != "ref-1" {
			t.Errorf("refresh_token = %q, want ref-1", req.RefreshToken)
		}
		json.NewEncoder(w).Encode(models.LoginResponse{
			AccessToken:  "acc-2",
			RefreshToken: "ref-2",
		})
	}))
	defer srv.Close()

	var persisted Tokens
	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	c.OnRefresh = func(t Tokens) error {
		persisted = t
		return nil
	}

	refreshed, err := c.refreshTokens(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("refreshTokens returned %v", err)
	}
	if !refreshed {
		t.Error("refreshed = false, want true")
	}
	if got := c.Tokens(); got.Access != "acc-2" {
		t.Errorf("access = %q, want acc-2", got.Access)
	}
	if persisted.Access != "acc-2" || persisted.Refresh != "ref-2" {
		t.Errorf("persisted = %+v, want acc-2/ref-2", persisted)
	}
}

func TestRefreshIsSingleFlight(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		json.NewEncoder(w).Encode(models.LoginResponse{AccessToken: "acc-2", RefreshToken: "ref-2"})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.refreshTokens(context.Background(), "acc-1")
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("refresh endpoint called %d times, want exactly 1", got)
	}
}

// TestRefreshSucceedsWhenPersistFails is the regression test for design
// correction 1: a failed OnRefresh (read-only credentials file, full disk)
// must not be reported as a failed exchange. The exchange itself succeeded
// and the client is holding perfectly usable tokens in memory — refreshed
// must be true, and those in-memory tokens must be the new ones, even though
// err is non-nil.
func TestRefreshSucceedsWhenPersistFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.LoginResponse{AccessToken: "acc-2", RefreshToken: "ref-2"})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	c.OnRefresh = func(Tokens) error {
		return errors.New("disk full")
	}

	refreshed, err := c.refreshTokens(context.Background(), "acc-1")
	if !refreshed {
		t.Error("refreshed = false, want true (the exchange succeeded even though persisting failed)")
	}
	if err == nil {
		t.Error("err = nil, want the persist error to be reported")
	}
	if got := c.Tokens(); got.Access != "acc-2" || got.Refresh != "ref-2" {
		t.Errorf("tokens = %+v, want acc-2/ref-2 in memory regardless of the persist failure", got)
	}
}

// TestRefreshWaitersLearnLeaderFailure is the regression test for design
// correction 2: when the leader's exchange fails, every waiter parked on
// c.inflight must learn that — not report success and hand the caller a
// still-stale token to retry with.
func TestRefreshWaitersLearnLeaderFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"refresh token expired"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	const n = 8
	results := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			refreshed, _ := c.refreshTokens(context.Background(), "acc-1")
			results[i] = refreshed
		}(i)
	}
	wg.Wait()

	for i, refreshed := range results {
		if refreshed {
			t.Errorf("goroutine %d: refreshed = true, want false — the leader's exchange failed", i)
		}
	}
}

// panicRoundTripper panics on every RoundTrip, simulating a transport-level
// panic — the kind a misbehaving http.RoundTripper, not the server, can
// produce.
type panicRoundTripper struct{}

func (panicRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	panic("simulated transport panic")
}

// TestRefreshSurvivesLeaderPanic is the regression test for the panic-wedge
// bug: if the leader's cleanup (clearing c.inflight, closing done) only ran
// on a normal return, a panic out of doJSON would skip it entirely, leaving
// every later caller blocked forever on a channel nobody closes. The fix
// moves that cleanup into a defer so it runs regardless of how the leader
// exits; this test panics the leader, recovers, and then asserts a later
// call actually proceeds instead of hanging.
func TestRefreshSurvivesLeaderPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	c.HTTP = &http.Client{Transport: panicRoundTripper{}}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("refreshTokens did not panic, want the injected transport panic to propagate")
			}
		}()
		c.refreshTokens(context.Background(), "acc-1")
	}()

	// Swap in a working transport and confirm the client isn't wedged: a
	// later refreshTokens call must proceed — succeeding here — rather than
	// blocking forever on a c.inflight channel the panicked leader never
	// closed.
	c.HTTP = srv.Client()

	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshed, err := c.refreshTokens(context.Background(), "acc-1")
		if err != nil || !refreshed {
			t.Errorf("post-panic refresh: refreshed=%v err=%v, want true/nil", refreshed, err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshTokens blocked after a leader panic — the client is permanently wedged")
	}
}

// TestRefreshSurvivesLeaderPanicWithoutDiscardingTokens pins the `r == nil`
// guard in the leader's deferred cleanup (see refreshTokens). Without that
// guard, a leader panic still reaches the "adopt tokens" branch: err is
// still nil (nothing ever assigned it), so a zero-valued resp gets adopted
// and the client's tokens silently become Tokens{} — discarding the user's
// session deterministically, with every other test in this package still
// green. This asserts the original tokens survive a leader panic intact.
func TestRefreshSurvivesLeaderPanicWithoutDiscardingTokens(t *testing.T) {
	want := Tokens{Access: "acc-1", Refresh: "ref-1"}
	c := New("http://example.invalid", want)
	c.HTTP = &http.Client{Transport: panicRoundTripper{}}

	func() {
		defer func() { recover() }()
		c.refreshTokens(context.Background(), "acc-1")
	}()

	if got := c.Tokens(); got != want {
		t.Errorf("tokens after leader panic = %+v, want unchanged %+v", got, want)
	}
}

// TestRefreshTwoSequentialGenerationsBothReachServer pins c.inflight being
// cleared after a completed refresh: if it weren't, a second caller — even
// one reporting a genuinely newer stale token — would find c.inflight still
// set to the first refresh's already-closed channel, take the waiter branch,
// return immediately without ever contacting the server, and report success
// against a token that was never actually refreshed. Two sequential refreshes
// against two different token generations must both reach /user/refresh.
func TestRefreshTwoSequentialGenerationsBothReachServer(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(models.LoginResponse{
			AccessToken:  fmt.Sprintf("acc-%d", n+1),
			RefreshToken: fmt.Sprintf("ref-%d", n+1),
		})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	refreshed1, err1 := c.refreshTokens(context.Background(), "acc-1")
	if err1 != nil || !refreshed1 {
		t.Fatalf("first refresh: refreshed=%v err=%v, want true/nil", refreshed1, err1)
	}
	if got := c.Tokens().Access; got != "acc-2" {
		t.Fatalf("access after first refresh = %q, want acc-2", got)
	}

	refreshed2, err2 := c.refreshTokens(context.Background(), "acc-2")
	if err2 != nil || !refreshed2 {
		t.Fatalf("second refresh: refreshed=%v err=%v, want true/nil", refreshed2, err2)
	}
	if got := c.Tokens().Access; got != "acc-3" {
		t.Errorf("access after second refresh = %q, want acc-3", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("/user/refresh called %d times, want exactly 2 (one per generation)", got)
	}
}

// TestRefreshLateCallerAfterCompletedRefreshDoesNotRetrigger pins the
// staleAccess early-return: a caller that still believes the OLD access
// token is current — because it saw the 401 before anyone had refreshed —
// but arrives only after some other caller already completed the refresh,
// must recognize the token has moved on and skip the server entirely rather
// than re-triggering a second, redundant exchange.
func TestRefreshLateCallerAfterCompletedRefreshDoesNotRetrigger(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(models.LoginResponse{AccessToken: "acc-2", RefreshToken: "ref-2"})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	refreshed1, err1 := c.refreshTokens(context.Background(), "acc-1")
	if err1 != nil || !refreshed1 {
		t.Fatalf("first refresh: refreshed=%v err=%v, want true/nil", refreshed1, err1)
	}

	// A "late" caller: it also saw acc-1 fail, but only gets around to
	// calling refreshTokens after the first refresh has already completed
	// and c.inflight has already been cleared.
	refreshed2, err2 := c.refreshTokens(context.Background(), "acc-1")
	if err2 != nil {
		t.Errorf("late caller: err = %v, want nil", err2)
	}
	if !refreshed2 {
		t.Errorf("late caller: refreshed = false, want true — the token already moved on")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("/user/refresh called %d times, want exactly 1 — this is the stampede the early-return guard prevents", got)
	}
}

// TestOnRefreshCanReadClientStateWithoutDeadlock pins two properties at
// once: OnRefresh must not be called while c.mu is held (or a callback that
// itself calls c.Tokens() would deadlock against sync.Mutex's
// non-reentrancy), and the new tokens must already be adopted by the time
// OnRefresh runs (the ordering TestRefreshReplacesTokensAndPersists also
// checks, from the other direction).
func TestOnRefreshCanReadClientStateWithoutDeadlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.LoginResponse{AccessToken: "acc-2", RefreshToken: "ref-2"})
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	var observed Tokens
	observedDone := make(chan struct{})
	c.OnRefresh = func(Tokens) error {
		observed = c.Tokens()
		close(observedDone)
		return nil
	}

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		refreshed, err := c.refreshTokens(context.Background(), "acc-1")
		if err != nil || !refreshed {
			t.Errorf("refreshTokens: refreshed=%v err=%v, want true/nil", refreshed, err)
		}
	}()

	select {
	case <-refreshDone:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshTokens did not return — OnRefresh's call to c.Tokens() likely deadlocked")
	}
	select {
	case <-observedDone:
	default:
		t.Fatal("OnRefresh never ran")
	}
	if observed.Access != "acc-2" || observed.Refresh != "ref-2" {
		t.Errorf("OnRefresh observed %+v via c.Tokens(), want acc-2/ref-2 (tokens adopted before OnRefresh runs)", observed)
	}
}
