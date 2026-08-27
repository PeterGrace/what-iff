package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

// writeConfig points $XDG_CONFIG_HOME at a fresh temp dir for this test and
// writes contents (if non-empty) to the whatiff/config.toml the config
// package expects there.
func writeConfig(t *testing.T, contents string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if contents == "" {
		return
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestLoadProfile_NoConfigFallsBackToLocalDefault(t *testing.T) {
	writeConfig(t, "")

	name, profile, err := loadProfile("")
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if name != config.DefaultProfileName {
		t.Errorf("name = %q, want %q", name, config.DefaultProfileName)
	}
	if profile.APIURL != config.DefaultAPIURL {
		t.Errorf("APIURL = %q, want %q", profile.APIURL, config.DefaultAPIURL)
	}
}

func TestLoadProfile_ReturnsResolvedName(t *testing.T) {
	writeConfig(t, `
default_profile = "staging"

[profiles.staging]
api_url = "https://staging.example.com/api"
`)

	// Passing the empty name exercises the fallback-to-default chain; the
	// resolved name it returns must be the one config.Resolve computed
	// ("staging"), not the empty string that was asked for. loadProfile must
	// not recompute this chain itself (see its doc comment) - this test
	// pins that it forwards config.Resolve's return value.
	name, profile, err := loadProfile("")
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if name != "staging" {
		t.Errorf("name = %q, want %q", name, "staging")
	}
	if profile.APIURL != "https://staging.example.com/api" {
		t.Errorf("APIURL = %q, want %q", profile.APIURL, "https://staging.example.com/api")
	}
}

func TestLoadProfile_UnknownProfileIsActionable(t *testing.T) {
	writeConfig(t, `
[profiles.staging]
api_url = "https://staging.example.com/api"
`)

	_, _, err := loadProfile("nope")
	if err == nil {
		t.Fatal("expected an error for an unknown profile, got nil")
	}
	// The error must name both the bad profile and what does exist, so a
	// user can fix their --profile flag without going and reading
	// config.toml themselves.
	msg := err.Error()
	if !strings.Contains(msg, "nope") {
		t.Errorf("error %q does not mention the requested profile %q", msg, "nope")
	}
	if !strings.Contains(msg, "staging") {
		t.Errorf("error %q does not list the available profile %q", msg, "staging")
	}
}

// TestNewSession_OnRefreshPreservesUsername is the regression test for a
// specific way newSession's OnRefresh closure could quietly break: an
// implementation that builds its config.Credentials from only the new token
// pair (e.g. config.Credentials{AccessToken: t.Access, RefreshToken:
// t.Refresh}) would silently blank the stored username on every token
// refresh. Nothing else would notice - Username is written once at login
// and never otherwise read back by any code path a human would look at
// during normal use - so this has to be checked directly rather than
// trusted to show up as a side effect of something else failing.
func TestNewSession_OnRefreshPreservesUsername(t *testing.T) {
	writeConfig(t, `
[profiles.local]
api_url = "http://127.0.0.1:1"
`)
	store, err := config.DefaultCredentialStore()
	if err != nil {
		t.Fatalf("DefaultCredentialStore: %v", err)
	}
	if err := store.Save("local", config.Credentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		Username:     "tester",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sess, err := newSession("local")
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	// Invoke the closure directly with a fresh token pair, the same way
	// client.refreshTokens would call it after a real refresh - no network
	// round trip needed, since OnRefresh is just persisting to disk.
	if err := sess.client.OnRefresh(client.Tokens{Access: "new-access", Refresh: "new-refresh"}); err != nil {
		t.Fatalf("OnRefresh: %v", err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load after refresh: %v", err)
	}
	if got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" {
		t.Errorf("tokens after refresh = %+v, want access=new-access refresh=new-refresh", got)
	}
	if got.Username != "tester" {
		t.Errorf("Username after refresh = %q, want %q (must survive every refresh)", got.Username, "tester")
	}
}
