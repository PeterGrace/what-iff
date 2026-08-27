# WhatIff CLI — Milestone 1: Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `wi` binary that can authenticate against a WhatIff server and list your chats, with profile-based config and a REST client that transparently refreshes expired tokens.

**Architecture:** Three new packages under `internal/cli/`. `config` owns TOML profiles and a `0600` credential file. `client` is the single chokepoint that speaks HTTP, decoding into `internal/models` types so client and server DTOs cannot drift. `cmd/whatiff-cli` is thin dispatch over those. No backend changes.

**Tech Stack:** Go 1.27, `github.com/BurntSushi/toml` (already a direct dep, `go.mod:7`), `golang.org/x/term` (already a direct dep, `go.mod:41`), stdlib `net/http` and `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-08-27-whatiff-cli-design.md`

---

## Milestone context

This is milestone 1 of 5. It deliberately stops short of any conversation
handling — that is milestone 2 (turn engine). What "done" means here: you can run
`wi login`, then `wi chats`, against a local `make run-mock` stack and see your
chats. Everything later builds on these three packages.

The full roadmap, each milestone producing working software on its own:

| # | Milestone | Done when |
|---|---|---|
| 1 | **Foundation** (this plan) | `wi login` and `wi chats` work against a local stack |
| 2 | **Turn engine + one-shot** | `wi -p "..."` holds a real conversation; `Engine`/`RemoteEngine` covered by fake-job tests |
| 3 | **Inline TUI shell** | Interactive `wi` with streaming, input box, `^C` interrupt, `/help`, `/quit` |
| 4 | **Overlays** | `^P` chat switcher, `/model`, `/persona`, `/memory`, rituals |
| 5 | **Attachments + polish** | Upload, image download, `/context` breakdown, `make cli-e2e` |

Each gets its own plan document written when its milestone starts. Writing
milestones 3–5 in detail now would be speculative: the TUI's shape depends on
what the engine's event stream turns out to feel like in practice.

## File structure

| File | Responsibility |
|---|---|
| `internal/cli/config/config.go` | TOML config, profile resolution, XDG paths |
| `internal/cli/config/credentials.go` | Per-profile token storage, `0600`, atomic writes |
| `internal/cli/config/_PACKAGE_SUMMARY.md` | Package docs (repo anti-drift rule) |
| `internal/cli/client/client.go` | Base HTTP client, typed errors, 401 refresh |
| `internal/cli/client/auth.go` | `Login`, `Refresh` |
| `internal/cli/client/chat.go` | `ListChats` and the pagination envelope |
| `internal/cli/client/_PACKAGE_SUMMARY.md` | Package docs |
| `cmd/whatiff-cli/main.go` | Flag parsing, subcommand dispatch |
| `cmd/whatiff-cli/login.go` | `wi login` — interactive password entry |
| `cmd/whatiff-cli/chats.go` | `wi chats`, `--json` |
| `Makefile` | `build-cli`, `install-cli` targets |

Files are split by responsibility rather than layer, and each stays small enough
to hold in context while editing.

---

### Task 1: Config types and profile resolution

**Files:**
- Create: `internal/cli/config/config.go`
- Test: `internal/cli/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config

import "testing"

func TestResolveProfile(t *testing.T) {
	cfg := Config{
		DefaultProfile: "hosted",
		Profiles: map[string]Profile{
			"local":  {APIURL: "http://localhost:8080/api"},
			"hosted": {APIURL: "https://whatiff.chat/api"},
		},
	}

	tests := []struct {
		name     string
		asked    string
		wantURL  string
		wantName string
		wantErr  bool
	}{
		{name: "explicit name wins", asked: "local", wantURL: "http://localhost:8080/api", wantName: "local"},
		{name: "empty falls back to default", asked: "", wantURL: "https://whatiff.chat/api", wantName: "hosted"},
		{name: "unknown name errors", asked: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, got, err := cfg.Resolve(tt.asked)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) = nil error, want error", tt.asked)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) returned %v", tt.asked, err)
			}
			if got.APIURL != tt.wantURL {
				t.Errorf("APIURL = %q, want %q", got.APIURL, tt.wantURL)
			}
			if name != tt.wantName {
				t.Errorf("resolved name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestResolveEmptyConfigUsesBuiltinLocal(t *testing.T) {
	var cfg Config
	name, got, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("Resolve on empty config returned %v", err)
	}
	if got.APIURL != "http://localhost:8080/api" {
		t.Errorf("APIURL = %q, want the builtin local default", got.APIURL)
	}
	if name != DefaultProfileName {
		t.Errorf("resolved name = %q, want %q", name, DefaultProfileName)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/config/ -run TestResolve -v`
Expected: FAIL — the package does not compile, `undefined: Config`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package config loads the WhatIff CLI's TOML configuration and stores
// per-profile credentials.
//
// A profile is a named server the CLI can talk to, so one binary can address a
// local development stack and a hosted instance without re-authenticating each
// time it switches.
package config

import "fmt"

// DefaultProfileName is used when the config file names no default.
const DefaultProfileName = "local"

// DefaultAPIURL is the local development stack, matching `make run`.
const DefaultAPIURL = "http://localhost:8080/api"

// Profile is one named WhatIff server plus per-server defaults.
type Profile struct {
	APIURL string `toml:"api_url"`
	// Model and Personality are optional defaults applied to new chats.
	Model       string `toml:"model"`
	Personality string `toml:"personality"`
}

// Config is the parsed contents of config.toml.
type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	DownloadDir    string             `toml:"download_dir"`
	Profiles       map[string]Profile `toml:"profiles"`
}

// Resolve returns the resolved profile name and the profile itself. An empty
// name falls back to the configured default, then to DefaultProfileName. A
// config with no profiles at all resolves to the builtin local default, so the
// CLI works against `make run` with no config file.
//
// The resolved name is returned rather than recomputed by callers: it is what
// keys the credential store, and duplicating this fallback chain in cmd/ would
// silently drift the moment the chain changes.
func (c Config) Resolve(name string) (string, Profile, error) {
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		name = DefaultProfileName
	}
	if p, ok := c.Profiles[name]; ok {
		if p.APIURL == "" {
			p.APIURL = DefaultAPIURL
		}
		return name, p, nil
	}
	if len(c.Profiles) == 0 && name == DefaultProfileName {
		return name, Profile{APIURL: DefaultAPIURL}, nil
	}
	return "", Profile{}, fmt.Errorf("no profile named %q in config", name)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/config/ -run TestResolve -v`
Expected: PASS, all four subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config/config.go internal/cli/config/config_test.go
git commit -m "feat(cli): config profile resolution"
```

### Task 2: Load config from disk

**Files:**
- Modify: `internal/cli/config/config.go`
- Test: `internal/cli/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/config/config_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsTOML(t *testing.T) {
	dir := t.TempDir()
	body := `
default_profile = "hosted"
download_dir = "/tmp/wi-files"

[profiles.local]
api_url = "http://localhost:8080/api"

[profiles.hosted]
api_url = "https://whatiff.chat/api"
model = "gpt-5"
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if cfg.DefaultProfile != "hosted" {
		t.Errorf("DefaultProfile = %q, want hosted", cfg.DefaultProfile)
	}
	if cfg.DownloadDir != "/tmp/wi-files" {
		t.Errorf("DownloadDir = %q, want /tmp/wi-files", cfg.DownloadDir)
	}
	if got := cfg.Profiles["hosted"].Model; got != "gpt-5" {
		t.Errorf("hosted model = %q, want gpt-5", got)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load on a missing file returned %v, want nil", err)
	}
	_, p, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if p.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q, want the builtin default", p.APIURL)
	}
}

func TestLoadMalformedTOMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not = = toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load on malformed TOML returned nil error, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/config/ -run TestLoad -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cli/config/config.go` (and extend the import block to
`"errors"`, `"fmt"`, `"os"`, `"path/filepath"`, and
`"github.com/BurntSushi/toml"`):

```go
// Load reads a config file. A missing file is not an error: the CLI is usable
// with no configuration at all, against a local server.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

// Dir is the CLI's configuration directory, honoring XDG_CONFIG_HOME through
// os.UserConfigDir.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(base, "whatiff"), nil
}

// DefaultPath is the standard location of config.toml.
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/config/ -v`
Expected: PASS, all tests including Task 1's.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config/
git commit -m "feat(cli): load TOML config from XDG config dir"
```

### Task 3: Credential store

Tokens are kept out of `config.toml` so the config file stays safe to share or
commit as a dotfile. `create-superuser` set the precedent that credentials are
interactive-only and never flag-supplied; this store is the persistence half of
that.

**Files:**
- Create: `internal/cli/config/credentials.go`
- Test: `internal/cli/config/credentials_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"path/filepath"
	"os"
	"testing"
)

func TestCredentialRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "acc-1", RefreshToken: "ref-1", Username: "pete"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v", err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestCredentialProfilesAreIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "local-acc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("hosted", Credentials{AccessToken: "hosted-acc"}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "local-acc" {
		t.Errorf("saving hosted clobbered local: got %q", got.AccessToken)
	}
}

func TestCredentialFileIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}
	if err := store.Save("local", Credentials{AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", perm)
	}
}

func TestLoadMissingProfileReturnsErrNoCredentials(t *testing.T) {
	store := CredentialStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if _, err := store.Load("local"); err != ErrNoCredentials {
		t.Fatalf("Load = %v, want ErrNoCredentials", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/config/ -run TestCredential -v`
Expected: FAIL — `undefined: CredentialStore`.

- [ ] **Step 3: Write minimal implementation**

```go
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoCredentials means the profile has never been logged in to.
var ErrNoCredentials = errors.New("no stored credentials for profile")

// Credentials is one profile's stored token pair.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
}

// CredentialStore persists Credentials per profile in a 0600 JSON file.
//
// This is deliberately a file rather than the OS keyring: headless servers and
// containers are a primary use case for a CLI, and keyring backends are absent
// or fail there. A keyring backend can be added later behind the same type.
type CredentialStore struct {
	Path string
}

// DefaultCredentialStore locates the store alongside config.toml.
func DefaultCredentialStore() (CredentialStore, error) {
	dir, err := Dir()
	if err != nil {
		return CredentialStore{}, err
	}
	return CredentialStore{Path: filepath.Join(dir, "credentials.json")}, nil
}

func (s CredentialStore) readAll() (map[string]Credentials, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Credentials{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.Path, err)
	}
	all := map[string]Credentials{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.Path, err)
	}
	return all, nil
}

// Load returns the profile's credentials, or ErrNoCredentials.
func (s CredentialStore) Load(profile string) (Credentials, error) {
	all, err := s.readAll()
	if err != nil {
		return Credentials{}, err
	}
	c, ok := all[profile]
	if !ok {
		return Credentials{}, ErrNoCredentials
	}
	return c, nil
}

// Save writes the profile's credentials, preserving other profiles.
//
// The write is atomic (temp file plus rename) so an interrupted save cannot
// leave a truncated file that locks the user out of every profile at once.
func (s CredentialStore) Save(profile string, c Credentials) error {
	all, err := s.readAll()
	if err != nil {
		return err
	}
	all[profile] = c

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temp credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing credentials: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("replacing %s: %w", s.Path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config/credentials.go internal/cli/config/credentials_test.go
git commit -m "feat(cli): 0600 per-profile credential store with atomic writes"
```

### Task 4: Base HTTP client and typed errors

**Files:**
- Create: `internal/cli/client/client.go`
- Test: `internal/cli/client/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
```

The test file's import block is:

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package client is the only place the WhatIff CLI speaks HTTP.
//
// Responses decode into internal/models types so the CLI and the server cannot
// disagree about a payload shape — the same single-chokepoint discipline the
// Playwright e2e SDK uses (web/app/e2e/sdk/client.ts).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// APIError is a non-2xx response. Message is the server's own wording, which
// the CLI surfaces verbatim rather than inventing its own.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("server returned %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// Tokens is a token pair held in memory by a Client.
type Tokens struct {
	Access  string
	Refresh string
}

// Client talks to one WhatIff server.
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
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		tokens:  t,
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

// doJSON performs one request with no refresh handling. Callers that need
// transparent re-auth use do (Task 6).
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return decodeAPIError(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// decodeAPIError turns a non-2xx response into an *APIError. A body that is not
// the server's JSON error envelope (a proxy's HTML 502, say) still yields a
// useful error carrying the status.
func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{Status: resp.StatusCode}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
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
	return apiErr
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -v`
Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/client.go internal/cli/client/client_test.go
git commit -m "feat(cli): base HTTP client with typed API errors"
```

### Task 5: Login and refresh calls

**Files:**
- Create: `internal/cli/client/auth.go`
- Test: `internal/cli/client/auth_test.go`

- [ ] **Step 1: Write the failing test**

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestLoginStoresTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/login" {
			t.Errorf("path = %q, want /user/login", r.URL.Path)
		}
		var req models.UserLoginRequest
		json.NewDecoder(r.Body).Decode(&req)
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
		json.NewDecoder(r.Body).Decode(&req)
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

	if err := c.refreshTokens(context.Background(), "acc-1"); err != nil {
		t.Fatalf("refreshTokens returned %v", err)
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
```

Add `"sync"`, `"sync/atomic"`, and `"time"` to the test file's import block for
the single-flight test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -run 'TestLogin|TestRefresh' -v`
Expected: FAIL — `c.Login undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
package client

import (
	"context"
	"net/http"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// Login exchanges credentials for a token pair and stores it on the client.
// usernameOrEmail matches the server, which accepts either.
func (c *Client) Login(ctx context.Context, usernameOrEmail, password string) (models.UserResponse, error) {
	req := models.UserLoginRequest{Username: usernameOrEmail, Password: password}
	var resp models.LoginResponse
	if err := c.doJSON(ctx, http.MethodPost, "/user/login", req, &resp); err != nil {
		return models.UserResponse{}, err
	}
	c.setTokens(Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken})
	return resp.User, nil
}

func (c *Client) setTokens(t Tokens) {
	c.mu.Lock()
	c.tokens = t
	c.mu.Unlock()
}

// refreshTokens exchanges the refresh token for a new pair.
//
// staleAccess is the access token the caller saw fail. If the client's token
// has already moved on, another goroutine refreshed first and this returns
// immediately — so a burst of concurrent 401s produces exactly one refresh
// request rather than a stampede against /user/refresh.
func (c *Client) refreshTokens(ctx context.Context, staleAccess string) error {
	c.mu.Lock()
	if c.tokens.Access != staleAccess {
		c.mu.Unlock()
		return nil
	}
	if existing := c.inflight; existing != nil {
		c.mu.Unlock()
		select {
		case <-existing:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	c.inflight = done
	refresh := c.tokens.Refresh
	c.mu.Unlock()

	var resp models.LoginResponse
	err := c.doJSON(ctx, http.MethodPost, "/user/refresh", models.RefreshTokenRequest{RefreshToken: refresh}, &resp)

	c.mu.Lock()
	c.inflight = nil
	var updated Tokens
	if err == nil {
		updated = Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken}
		c.tokens = updated
	}
	c.mu.Unlock()
	close(done)

	if err != nil {
		return err
	}
	if c.OnRefresh != nil {
		return c.OnRefresh(updated)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -race -v`
Expected: PASS. The `-race` flag matters — the single-flight test is the one
place in this milestone where concurrency bugs are plausible.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/auth.go internal/cli/client/auth_test.go
git commit -m "feat(cli): login and single-flight token refresh"
```

### Task 6: Transparent re-auth on 401

**Files:**
- Modify: `internal/cli/client/client.go`
- Test: `internal/cli/client/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client/client_test.go`:

```go
func TestDoRetriesOnceAfter401(t *testing.T) {
	var thingCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/thing":
			thingCalls++
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
	if thingCalls != 2 {
		t.Errorf("/thing called %d times, want 2 (one 401, one retry)", thingCalls)
	}
}

func TestDoDoesNotRetryTwice(t *testing.T) {
	var thingCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/refresh" {
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
			return
		}
		thingCalls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil {
		t.Fatal("do returned nil error on a persistent 401, want error")
	}
	if thingCalls != 2 {
		t.Errorf("/thing called %d times, want 2 — one retry only, no loop", thingCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -run TestDo -v`
Expected: FAIL — `c.do undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cli/client/client.go`:

```go
// do performs a request, refreshing the access token once and retrying if the
// server rejects it. Every caller outside this file uses do, not doJSON, so a
// two-hour access token expiring mid-session is invisible to the user.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	stale := c.accessToken()
	err := c.doJSON(ctx, method, path, body, out)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		return err
	}
	if stale == "" {
		return err
	}
	if refreshErr := c.refreshTokens(ctx, stale); refreshErr != nil {
		return err
	}
	return c.doJSON(ctx, method, path, body, out)
}
```

Add `"errors"` to the import block in `client.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -race -v`
Expected: PASS, all client tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/
git commit -m "feat(cli): transparent token refresh on 401"
```

### Task 7: List chats

**Files:**
- Create: `internal/cli/client/chat.go`
- Test: `internal/cli/client/chat_test.go`

- [ ] **Step 1: Write the failing test**

```go
package client

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	chats, err := c.ListChats(context.Background(), ListChatsOptions{})
	if err != nil {
		t.Fatalf("ListChats returned %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("len(chats) = %d, want 2", len(chats))
	}
	if chats[0].Name != "deploy plan" {
		t.Errorf("chats[0].Name = %q, want deploy plan", chats[0].Name)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -run TestListChats -v`
Expected: FAIL — `undefined: ListChatsOptions`.

- [ ] **Step 3: Write minimal implementation**

```go
package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// ListChatsOptions filters a chat listing. The zero value lists active
// (non-archived) chats, matching the server's default.
type ListChatsOptions struct {
	Archived bool
	Search   string
	Limit    int
}

// chatPage is the server's pagination envelope specialized to chats. The shared
// models.PaginatedResponse uses []any, which would force a second decode pass.
type chatPage struct {
	Results    []models.Chat `json:"results"`
	TotalCount int           `json:"total_count"`
	Page       int           `json:"page"`
}

// ListChats returns the user's chats.
func (c *Client) ListChats(ctx context.Context, opts ListChatsOptions) ([]models.Chat, error) {
	q := url.Values{}
	if opts.Archived {
		q.Set("archived", "true")
	}
	if opts.Search != "" {
		q.Set("search", opts.Search)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}

	path := "/chat"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var page chatPage
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	return page.Results, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/chat.go internal/cli/client/chat_test.go
git commit -m "feat(cli): list chats"
```

### Task 8: Command dispatch and shared setup

**Files:**
- Create: `cmd/whatiff-cli/main.go`

- [ ] **Step 1: Write the implementation**

There is no unit test for this file: it is argument parsing and process exit,
and the behavior worth testing lives in `config` and `client`, which are already
covered. Task 11's end-to-end check exercises it.

```go
// Command whatiff-cli (installed as `wi`) is a terminal client for WhatIff.
//
// Milestone 1 provides `login` and `chats`. Conversation handling arrives with
// the turn engine in milestone 2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

func main() {
	profile := flag.String("profile", os.Getenv("WHATIFF_PROFILE"), "config profile to use")
	asJSON := flag.Bool("json", false, "emit JSON instead of human-readable output")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	if err := run(context.Background(), args[0], args[1:], *profile, *asJSON); err != nil {
		fmt.Fprintf(os.Stderr, "wi: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `wi - WhatIff terminal client

Usage:
  wi [flags] <command> [args]

Commands:
  login    Authenticate against the configured profile
  chats    List your chats

Flags:
  --profile <name>   Config profile (default: $WHATIFF_PROFILE, then config)
  --json             Emit JSON instead of human-readable output
`)
}

func run(ctx context.Context, command string, args []string, profileName string, asJSON bool) error {
	switch command {
	case "login":
		return runLogin(ctx, profileName)
	case "chats":
		return runChats(ctx, profileName, asJSON, args)
	default:
		return fmt.Errorf("unknown command %q (try: login, chats)", command)
	}
}

// session bundles the resolved profile and an authenticated client.
type session struct {
	profileName string
	profile     config.Profile
	client      *client.Client
}

// loadProfile resolves configuration without requiring credentials. Used by
// login, which is how credentials come to exist in the first place.
func loadProfile(profileName string) (string, config.Profile, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", config.Profile{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Profile{}, err
	}
	return cfg.Resolve(profileName)
}

// newSession resolves configuration and builds a client carrying stored
// credentials, wired so a refreshed token pair is written back to disk.
func newSession(profileName string) (*session, error) {
	name, profile, err := loadProfile(profileName)
	if err != nil {
		return nil, err
	}
	store, err := config.DefaultCredentialStore()
	if err != nil {
		return nil, err
	}
	creds, err := store.Load(name)
	if err != nil {
		if err == config.ErrNoCredentials {
			return nil, fmt.Errorf("not logged in to profile %q at %s - run: wi --profile %s login", name, profile.APIURL, name)
		}
		return nil, err
	}

	c := client.New(profile.APIURL, client.Tokens{Access: creds.AccessToken, Refresh: creds.RefreshToken})
	c.OnRefresh = func(t client.Tokens) error {
		creds.AccessToken = t.Access
		creds.RefreshToken = t.Refresh
		return store.Save(name, creds)
	}
	return &session{profileName: name, profile: profile, client: c}, nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/whatiff-cli/`
Expected: fails with `undefined: runLogin` and `undefined: runChats`. That is
expected — Tasks 9 and 10 supply them. Do not commit yet.

### Task 9: `wi login`

**Files:**
- Create: `cmd/whatiff-cli/login.go`

- [ ] **Step 1: Write the implementation**

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

// runLogin authenticates and stores the resulting token pair.
//
// Password entry is interactive only. There is deliberately no --password flag
// and no password environment variable, matching cmd/create-superuser: a flag
// would put the password in shell history and in every process listing on the
// machine.
func runLogin(ctx context.Context, profileName string) error {
	name, profile, err := loadProfile(profileName)
	if err != nil {
		return err
	}

	fmt.Printf("Logging in to %s (profile %q)\n", profile.APIURL, name)

	username, err := promptLine("Username or email: ")
	if err != nil {
		return err
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}

	password, err := promptPassword("Password: ")
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	c := client.New(profile.APIURL, client.Tokens{})
	user, err := c.Login(ctx, username, password)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	store, err := config.DefaultCredentialStore()
	if err != nil {
		return err
	}
	tokens := c.Tokens()
	if err := store.Save(name, config.Credentials{
		AccessToken:  tokens.Access,
		RefreshToken: tokens.Refresh,
		Username:     user.Username,
	}); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}

	fmt.Printf("Logged in as %s\n", user.Username)
	return nil
}

func promptLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// promptPassword reads without echoing. It requires a TTY: piping a password in
// would defeat the point of not having a flag for it.
func promptPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("password entry requires an interactive terminal")
	}
	fmt.Print(prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/whatiff-cli/`
Expected: still fails on `undefined: runChats` only. Task 10 finishes it.

### Task 10: `wi chats`

**Files:**
- Create: `cmd/whatiff-cli/chats.go`

- [ ] **Step 1: Write the implementation**

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
)

// defaultChatLimit overrides the server's page size, which is 10
// (internal/handlers/chat/chat.go:167). Inheriting that default would silently
// truncate the listing and look like missing data.
const defaultChatLimit = 100

func runChats(ctx context.Context, profileName string, asJSON bool, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	archived := fs.Bool("archived", false, "list archived chats instead of active ones")
	search := fs.String("search", "", "filter by name or checkpoint summary")
	limit := fs.Int("limit", defaultChatLimit, "maximum chats to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sess, err := newSession(profileName)
	if err != nil {
		return err
	}

	chats, err := sess.client.ListChats(ctx, client.ListChatsOptions{
		Archived: *archived,
		Search:   *search,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(chats)
	}

	if len(chats) == 0 {
		fmt.Println("No chats found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODEL\tLAST MESSAGE\tUNREAD")
	for _, c := range chats {
		last := "-"
		if c.LastMessageTime != nil {
			last = humanizeSince(*c.LastMessageTime)
		}
		unread := ""
		if c.UnreadCount > 0 {
			unread = fmt.Sprintf("%d", c.UnreadCount)
		}
		model := c.ModelName
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Name, model, last, unread)
	}
	return w.Flush()
}

// humanizeSince renders a coarse relative time. Chat listings are scanned, not
// read precisely, so minutes-level granularity is enough and much easier to
// scan than a timestamp.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
```

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/whatiff-cli/ && go vet ./cmd/whatiff-cli/ ./internal/cli/...`
Expected: no output from either. The binary now compiles.

- [ ] **Step 3: Commit**

```bash
git add cmd/whatiff-cli/
git commit -m "feat(cli): wi login and wi chats commands"
```

### Task 11: Makefile targets, package docs, and an end-to-end check

**Files:**
- Modify: `Makefile`
- Create: `internal/cli/config/_PACKAGE_SUMMARY.md`
- Create: `internal/cli/client/_PACKAGE_SUMMARY.md`

- [ ] **Step 1: Add the Makefile targets**

Append to `Makefile`. Keep the existing `.PHONY` style used by neighbouring
targets, and add `build-cli install-cli` to the `.PHONY` list.

```makefile
build-cli: ## Build the wi CLI into ./bin/wi
	@mkdir -p bin
	go build -o bin/wi ./cmd/whatiff-cli

install-cli: ## Install the wi CLI into $(GOPATH)/bin
	go install ./cmd/whatiff-cli
```

`go install` names the binary after the directory, so this installs
`whatiff-cli`. Users who want `wi` on their PATH symlink it, which the README
note in Step 3 explains.

- [ ] **Step 2: Verify the targets work**

Run: `make build-cli && ./bin/wi 2>&1 | head -5`
Expected: the usage text, listing `login` and `chats`. Exit code 2.

- [ ] **Step 3: Write the package summaries**

Both follow the seven-heading template in `docs/ARCHITECTURE_SUMMARY.md`: Role,
Responsibilities, Key types and entry points, Dependencies, Non-obvious
decisions, Testing, Related.

`internal/cli/config/_PACKAGE_SUMMARY.md`:

```markdown
# Package: `internal/cli/config`

## Role

Configuration and credential storage for the `wi` CLI.

## Responsibilities

- Parse `config.toml` (profiles, default profile, download directory).
- Resolve a profile by name, falling back to a builtin `local` profile so the
  CLI works with no config file against `make run`.
- Store and load per-profile token pairs in a `0600` JSON file.

## Key types and entry points

- `Config`, `Profile`, `Load`, `Config.Resolve`, `DefaultPath`, `Dir`.
- `Credentials`, `CredentialStore`, `DefaultCredentialStore`, `ErrNoCredentials`.

## Dependencies

- **Inbound:** `cmd/whatiff-cli`.
- **Outbound:** `github.com/BurntSushi/toml`, stdlib only otherwise.

## Non-obvious decisions

- Credentials live in a separate file from `config.toml` so the config file
  stays safe to share or keep in a dotfiles repo.
- A file store rather than the OS keyring: headless servers and containers are a
  primary CLI use case, and keyring backends are absent or fail there.
- `CredentialStore.Save` writes to a temp file and renames, so an interrupted
  save cannot truncate the file and lock the user out of every profile at once.
- A missing config file is not an error.

## Testing

- `config_test.go` — profile resolution, TOML loading, malformed input.
- `credentials_test.go` — round trip, profile independence, `0600` mode,
  `ErrNoCredentials`.

## Related documentation

- [CLI design spec](../../../docs/superpowers/specs/2026-08-27-whatiff-cli-design.md)
```

`internal/cli/client/_PACKAGE_SUMMARY.md`:

```markdown
# Package: `internal/cli/client`

## Role

The only place the `wi` CLI speaks HTTP to a WhatIff server.

## Responsibilities

- Issue authenticated JSON requests and decode them into `internal/models` types.
- Convert non-2xx responses into `*APIError` carrying the server's own message.
- Refresh an expired access token transparently and retry the request once.
- Resource calls: `Login`, `ListChats`.

## Key types and entry points

- `Client`, `New`, `Tokens`, `APIError`.
- `Client.do` — the method every resource call uses; handles re-auth.
- `Client.doJSON` — a single request with no refresh handling.
- `Client.Login`, `Client.ListChats`, `ListChatsOptions`.

## Dependencies

- **Inbound:** `cmd/whatiff-cli`.
- **Outbound:** `internal/models`, stdlib `net/http`.

## Non-obvious decisions

- Decoding into `internal/models` is why the CLI lives in this repo: the client
  and server cannot disagree about a payload shape.
- `refreshTokens` takes the *stale* access token and single-flights, so a burst
  of concurrent 401s produces exactly one call to `/user/refresh`.
- `do` retries exactly once after a refresh; a persistent 401 surfaces rather
  than looping.
- `ListChats` decodes into a chat-specific page struct rather than
  `models.PaginatedResponse`, whose `[]any` results would force a second decode.
- `decodeAPIError` limits the body it reads and tolerates non-JSON bodies, so a
  proxy's HTML 502 still yields a useful error.

## Testing

- `client_test.go` — success decode, error envelope, non-JSON error body,
  401-refresh-retry, no-double-retry.
- `auth_test.go` — login token storage, refresh persistence, single-flight
  (run with `-race`).
- `chat_test.go` — pagination envelope unwrapping, query parameters.

## Related documentation

- [CLI design spec](../../../docs/superpowers/specs/2026-08-27-whatiff-cli-design.md)
```

- [ ] **Step 4: Run the full repo gate**

Run: `make pre-commit`
Expected: PASS. This runs `fmt vet tidy test build check-no-local-models`. If
`fmt` complains, run `make fmt-fix` and re-run.

- [ ] **Step 5: End-to-end check against a real server**

This is a manual verification, not an automated test — the automated
`make cli-e2e` target arrives in milestone 2 when there is a conversation to
assert on.

```bash
make db-up
make run-mock &          # or: make dev-up
./bin/wi login           # register first via the web app or the API if needed
./bin/wi chats
./bin/wi chats --json
```

Expected: `login` stores credentials and prints `Logged in as <username>`;
`chats` prints a table (or `No chats found.` on a fresh account); `--json`
prints a JSON array. Then confirm the credential file is locked down:

```bash
stat -c '%a' ~/.config/whatiff/credentials.json
```

Expected: `600`.

- [ ] **Step 6: Commit**

```bash
git add Makefile internal/cli/config/_PACKAGE_SUMMARY.md internal/cli/client/_PACKAGE_SUMMARY.md
git commit -m "chore(cli): build targets and package documentation"
```

---

## Milestone 1 done when

- `make pre-commit` passes.
- `wi login` against a `make run-mock` stack stores a `0600` credential file.
- `wi chats` and `wi chats --json` both list chats.
- Both new packages have a `_PACKAGE_SUMMARY.md`.

## What this milestone deliberately does not do

- No conversation handling — that is the `engine` package in milestone 2.
- No TUI, no Bubble Tea dependency yet. Milestone 1 adds **zero** new modules to
  `go.mod`; both dependencies it uses are already direct requirements. Keeping
  the dependency change out of the foundation makes the eventual Bubble Tea
  commit a small, reviewable diff on its own.
