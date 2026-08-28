# WhatIff CLI — Milestone 1: Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `wi` binary that can authenticate against a WhatIff server and list your chats, with profile-based config and a REST client that transparently refreshes expired tokens.

**Architecture:** Three new packages under `internal/cli/`. `config` owns TOML profiles and a `0600` credential file. `client` is the single chokepoint that speaks HTTP, decoding into `internal/models` types so client and server DTOs cannot drift. `cmd/whatiff-cli` is thin dispatch over those. No backend changes.

**Tech Stack:** Go 1.27, `github.com/BurntSushi/toml` (already a direct dep, `go.mod:7`), `golang.org/x/term` (already a direct dep, `go.mod:42`), `golang.org/x/sys` (promoted to a direct dep by Task 9's terminal-flush ioctl, `go.mod:41`), stdlib `net/http` and `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-08-27-whatiff-cli-design.md`

---

## Status: executed

This plan has been carried out. Tasks 1–10 are complete and shipped; Task 11
(the live end-to-end check) is marked `- [x]` below because it was run for
real, verifying the other ten. The remaining `- [ ]` checkboxes on Tasks
1–10 are vestigial — an artifact of the plan being written and executed in
one pass rather than checked off step-by-step — not a sign the work is
outstanding. Do not go back and re-check them individually.

A whole-milestone review after Task 11 landed found a few gaps no
per-task review could see on its own (a macOS-destructive test default, a
subcommand that skipped the shared flag-parsing convention, global flags
that only worked before the subcommand name, and others) and fixed them in
follow-up commits on top of what this plan describes. The task bodies below
were left as originally written/executed rather than rewritten around those
fixes; where a fix changes something a task body asserts, this status
section or an inline note says so instead.

Known gap: `wi chats --search` is shipped and works correctly on the CLI
side but fails 100% of the time against the real server — see "Milestone 1
done when" below and issue #8.

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
| `internal/cli/config/config.go` | TOML config, profile resolution |
| `internal/cli/config/paths.go` | XDG config dir and default config path |
| `internal/cli/config/credentials.go` | Per-profile token storage, `0600`, atomic writes |
| `internal/cli/config/_PACKAGE_SUMMARY.md` | Package docs (repo anti-drift rule) |
| `internal/cli/client/client.go` | Base HTTP client, typed errors, 401 refresh |
| `internal/cli/client/auth.go` | `Login`; unexported `refreshTokens`, the single-flight 401 retry `do` calls |
| `internal/cli/client/chat.go` | `ListChats` and the pagination envelope |
| `internal/cli/client/_PACKAGE_SUMMARY.md` | Package docs |
| `cmd/whatiff-cli/main.go` | Flag parsing, subcommand dispatch |
| `cmd/whatiff-cli/login.go` | `wi login` — interactive password entry |
| `cmd/whatiff-cli/chats.go` | `wi chats`, `--json` |
| `cmd/whatiff-cli/tty_tcflsh.go` | `flushPendingInput` via `TCFLSH` (linux, aix, solaris) |
| `cmd/whatiff-cli/tty_bsd.go` | `flushPendingInput` via `TIOCFLUSH` (the BSD family) |
| `cmd/whatiff-cli/tty_other.go` | `flushPendingInput` no-op (every remaining GOOS) |
| `cmd/whatiff-cli/_PACKAGE_SUMMARY.md` | Package docs |
| `Makefile` | `build-cli`, `install-cli`, `build-cli-crosscheck` targets |

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

import (
	"strings"
	"testing"
)

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

func TestResolveErrorsOnProfileWithNoAPIURL(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"hosted": {Model: "gpt-5"},
		},
	}
	_, _, err := cfg.Resolve("hosted")
	if err == nil {
		t.Fatal("Resolve(\"hosted\") = nil error, want error")
	}
	if !strings.Contains(err.Error(), "api_url") {
		t.Errorf("error = %q, want it to mention api_url", err.Error())
	}
}

func TestResolveAllowsLocalProfileWithNoAPIURL(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"local": {Model: "gpt-5"},
		},
	}
	name, p, err := cfg.Resolve("local")
	if err != nil {
		t.Fatalf("Resolve(\"local\") returned %v", err)
	}
	if name != "local" {
		t.Errorf("resolved name = %q, want local", name)
	}
	if p.APIURL != "http://localhost:8080/api" {
		t.Errorf("APIURL = %q, want the builtin local default", p.APIURL)
	}
	if p.Model != "gpt-5" {
		t.Errorf("Model = %q, want gpt-5", p.Model)
	}
}

func TestResolveRejectsAPIURLWithoutScheme(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"hosted": {APIURL: "whatiff.chat/api"},
		},
	}
	if _, _, err := cfg.Resolve("hosted"); err == nil {
		t.Fatal("Resolve(\"hosted\") = nil error, want error")
	}
}

func TestResolveTrimsAPIURLWhitespace(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"hosted": {APIURL: "  http://localhost:8080/api  "},
		},
	}
	_, p, err := cfg.Resolve("hosted")
	if err != nil {
		t.Fatalf("Resolve(\"hosted\") returned %v", err)
	}
	if p.APIURL != "http://localhost:8080/api" {
		t.Errorf("APIURL = %q, want trimmed %q", p.APIURL, "http://localhost:8080/api")
	}
}

func TestResolveErrorListsAvailableProfiles(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"local":  {APIURL: "http://localhost:8080/api"},
			"hosted": {APIURL: "https://whatiff.chat/api"},
		},
	}
	_, _, err := cfg.Resolve("nope")
	if err == nil {
		t.Fatal("Resolve(\"nope\") = nil error, want error")
	}
	if !strings.Contains(err.Error(), "local") || !strings.Contains(err.Error(), "hosted") {
		t.Errorf("error = %q, want it to list both profile names", err.Error())
	}
}

func TestResolveNoDefaultProfileErrorIsSpecific(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"hosted": {APIURL: "https://whatiff.chat/api"},
		},
	}
	_, _, err := cfg.Resolve("")
	if err == nil {
		t.Fatal("Resolve(\"\") = nil error, want error")
	}
	if !strings.Contains(err.Error(), "default_profile") {
		t.Errorf("error = %q, want it to mention default_profile", err.Error())
	}
}

func TestResolveDoesNotMutateConfig(t *testing.T) {
	cfg := Config{
		Profiles: map[string]Profile{
			"local": {},
		},
	}
	if _, _, err := cfg.Resolve("local"); err != nil {
		t.Fatalf("Resolve(\"local\") returned %v", err)
	}
	if cfg.Profiles["local"].APIURL != "" {
		t.Errorf("cfg.Profiles[\"local\"].APIURL = %q, want unchanged empty string", cfg.Profiles["local"].APIURL)
	}
}
```

Note: these tests need `strings.Contains`, so the import block is
`import ("strings"; "testing")` from the start — Task 1 introduces `strings`
because `Resolve`'s error messages need it (see Step 3 below), not because of
anything Task 2 adds.

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

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

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
// The empty-api_url convenience default applies ONLY to the builtin local
// profile name. Any other profile with a missing or malformed api_url is
// rejected here rather than silently resolving to localhost: once a profile
// is misconfigured (e.g. an `apiurl` typo that leaves `api_url` unset),
// falling back to the local default would mean credentials meant for a
// hosted server end up sent to whatever is listening on localhost instead.
//
// The resolved name is returned rather than recomputed by callers: it is what
// keys the credential store, and duplicating this fallback chain in cmd/ would
// silently drift the moment the chain changes.
func (c Config) Resolve(name string) (string, Profile, error) {
	asked := name
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		name = DefaultProfileName
	}

	// p ends up holding the candidate profile from whichever branch below
	// applies; validateAPIURL is then called exactly once, so the builtin
	// local default (used by both the found-profile and no-profiles-at-all
	// branches) is validated the same way a declared profile is.
	p, ok := c.Profiles[name]
	switch {
	case ok:
		if p.APIURL == "" {
			if name != DefaultProfileName {
				return "", Profile{}, fmt.Errorf("profile %q has no api_url", name)
			}
			p.APIURL = DefaultAPIURL
		}
	case len(c.Profiles) == 0 && name == DefaultProfileName:
		p = Profile{APIURL: DefaultAPIURL}
	case asked == "" && c.DefaultProfile == "":
		return "", Profile{}, fmt.Errorf("no default_profile set and no profile named %q (available: %s)", name, c.profileNames())
	default:
		return "", Profile{}, fmt.Errorf("no profile named %q (available: %s)", name, c.profileNames())
	}

	cleaned, err := validateAPIURL(name, p.APIURL)
	if err != nil {
		return "", Profile{}, err
	}
	p.APIURL = cleaned
	return name, p, nil
}

// validateAPIURL rejects a profile URL that net/http could not use, at the one
// chokepoint where the profile name is still known. Without this the failure
// surfaces later as an opaque transport error naming no profile. It returns
// the trimmed URL: ordinary whitespace around api_url in config.toml would
// otherwise reach net/http unexamined — a trailing space is silently accepted
// and turns into a literal %20 in the request path, and a leading space
// produces the unrelated-looking error "first path segment in URL cannot
// contain colon". Callers must use the returned string, not raw.
func validateAPIURL(name, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("profile %q has an invalid api_url %q: %w", name, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("profile %q api_url %q must start with http:// or https://", name, raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("profile %q api_url %q has no host", name, raw)
	}
	return raw, nil
}

// profileNames lists configured profile names for error messages, sorted so the
// output is stable across runs (Go map iteration order is not).
func (c Config) profileNames() string {
	if len(c.Profiles) == 0 {
		return "none"
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/config/ -run TestResolve -v`
Expected: PASS, all subtests and standalone tests above.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config/config.go internal/cli/config/config_test.go
git commit -m "feat(cli): config profile resolution"
```

### Task 2: Load config from disk

**Files:**
- Modify: `internal/cli/config/config.go`
- Create: `internal/cli/config/paths.go`
- Test: `internal/cli/config/config_test.go`
- Test: `internal/cli/config/paths_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/config/config_test.go`:

```go
import (
	"os"
	"path/filepath"
	"strings"
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
	// Hardcoded rather than compared against DefaultAPIURL: hardcoding here is
	// what would catch an accidental change to the constant.
	if p.APIURL != "http://localhost:8080/api" {
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

func TestLoadRejectsUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantKey string
	}{
		{
			name:    "top-level typo",
			body:    `defaultprofile = "hosted"`,
			wantKey: "defaultprofile",
		},
		{
			// The scenario the api_url docs actually cite: nested-table
			// unification is exactly what toml.Unmarshal would silently
			// absorb, which is why Load uses toml.Decode + Undecoded()
			// instead.
			name: "nested typo inside a profile table",
			body: `
[profiles.hosted]
apiurl = "https://whatiff.chat/api"
`,
			wantKey: "profiles.hosted.apiurl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load returned nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error = %q, want it to name the offending key %q", err.Error(), tt.wantKey)
			}
		})
	}
}

```

IMPORTANT: `strings` was already added to the test file's import block by
Task 1 (its `Resolve` error-message tests need it) — merge these new tests
into the existing `os` / `path/filepath` / `strings` / `testing` block rather
than adding a second import statement.

`Dir` and `DefaultPath` and their test live in their own file rather than in
`config.go`/`config_test.go`: path discovery is a separate responsibility from
TOML parsing, and Task 3 gives it a second consumer (`DefaultCredentialStore`),
so it earns its own file from the start. Create
`internal/cli/config/paths_test.go`:

```go
package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathEndsWithWhatiffConfigToml(t *testing.T) {
	// os.UserConfigDir errors if neither XDG_CONFIG_HOME nor HOME is set —
	// true in a minimal container even though CI's ambient HOME hides it.
	// Pinning XDG_CONFIG_HOME keeps this test hermetic and deterministic.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath returned %v", err)
	}
	want := filepath.Join("whatiff", "config.toml")
	if !strings.HasSuffix(path, want) {
		t.Errorf("DefaultPath() = %q, want suffix %q", path, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/config/ -run TestLoad -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cli/config/config.go` (and extend the import block to
`"errors"`, `"fmt"`, `"net/url"`, `"os"`, `"sort"`, `"strings"`, and
`"github.com/BurntSushi/toml"`):

```go
// Load reads a config file. A missing file is not an error: the CLI is usable
// with no configuration at all, against a local server.
//
// Unknown keys (e.g. an `apiurl` typo for `api_url`) are rejected rather than
// silently discarded, since a silently-discarded typo is exactly what let a
// misconfigured profile fall back to the builtin local default.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return cfg, fmt.Errorf("unknown key(s) in %s: %s", path, strings.Join(keys, ", "))
	}
	return cfg, nil
}
```

Create `internal/cli/config/paths.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir is the CLI's configuration directory: the OS user config directory
// (XDG_CONFIG_HOME on Linux) plus a whatiff/ component.
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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	if _, err := store.Load("local"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("Load = %v, want ErrNoCredentials", err)
	}
}

func TestLoadProfileMissingFromPopulatedFileReturnsErrNoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}
	if err := store.Save("hosted", Credentials{AccessToken: "hosted-acc"}); err != nil {
		t.Fatal(err)
	}

	// The file exists and parses fine; it just has no "local" entry. This is
	// a different code path than the missing-file case above (the !ok branch
	// after a successful readAll, not the os.ErrNotExist branch).
	if _, err := store.Load("local"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("Load = %v, want ErrNoCredentials", err)
	}
}

func TestSaveOverwritesSameProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "old-acc", RefreshToken: "old-ref"}); err != nil {
		t.Fatal(err)
	}
	// This is the path exercised on every token refresh, not just login.
	want := Credentials{AccessToken: "new-acc", RefreshToken: "new-ref"}
	if err := store.Save("local", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveOnNullFileDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	// json.MarshalIndent of a nil map produces exactly this, and this store's
	// own Save can therefore write the input that used to crash its own
	// readAll/Save round trip via a nil-map assignment panic.
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "acc-1"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v, want nil (and no panic)", err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveRecoversFromCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"local": {"access_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "fresh-acc"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v, want nil", err)
	}

	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Errorf("quarantined file %s.corrupt not found: %v", path, err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadOnCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"local": {"access_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	_, err := store.Load("local")
	if err == nil {
		t.Fatal("Load on a corrupt file returned nil error, want error")
	}
	if errors.Is(err, ErrNoCredentials) {
		t.Errorf("Load on a corrupt file = %v, want it NOT to be ErrNoCredentials", err)
	}
	if !strings.Contains(err.Error(), "wi login") {
		t.Errorf("error = %q, want it to name the remedy (`wi login`)", err.Error())
	}
}

func TestFailedSaveLeavesExistingFileIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so this write-protection test cannot fail the way it needs to")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "original"}); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	if err := store.Save("local", Credentials{AccessToken: "should-not-land"}); err == nil {
		t.Fatal("Save into a read-only directory returned nil error, want error")
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got.AccessToken != "original" {
		t.Errorf("AccessToken = %q, want unchanged %q", got.AccessToken, "original")
	}
}

func TestSaveSweepsOnlyStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "acc-1"}); err != nil {
		t.Fatal(err)
	}

	// A fresh temp file looks like an in-flight Save's own file and must
	// survive the sweep; a stale one looks like leftovers from a killed
	// Save and must be removed.
	freshTmp := filepath.Join(dir, "credentials-fresh.tmp")
	if err := os.WriteFile(freshTmp, []byte("in flight"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTmp := filepath.Join(dir, "credentials-stale.tmp")
	if err := os.WriteFile(staleTmp, []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleTempAge)
	if err := os.Chtimes(staleTmp, old, old); err != nil {
		t.Fatal(err)
	}

	if err := store.Save("local", Credentials{AccessToken: "acc-2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(freshTmp); err != nil {
		t.Errorf("fresh temp file was removed by the sweep: %v", err)
	}
	if _, err := os.Stat(staleTmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp file still exists (stat err = %v), want it swept", err)
	}
}

func TestSaveConcurrentSavesAllSucceed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	// Regression test for a sweep that unlinked a concurrent Save's own
	// in-flight temp file out from under it: the reviewer measured 7/50
	// concurrent Saves failing before the staleness check was added.
	const n = 30
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = store.Save("local", Credentials{AccessToken: fmt.Sprintf("acc-%d", i)})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Save #%d returned %v, want nil", i, err)
		}
	}
}
```

NOTE: this uses `errors.Is`, not `err != ErrNoCredentials` — sentinel comparison
by `!=` breaks the moment anyone wraps the error; `errors.Is` is the correct
idiom and costs nothing.

The eight tests after `TestLoadMissingProfileReturnsErrNoCredentials` cover
bugs that code review found empirically, across two rounds: a panic on a
`null` file and a corrupt file that locked `Save` out of recovering (see "Why
`readAll` normalizes and quarantines" after Step 5), then a sweep that removed
a concurrent `Save`'s own in-flight temp file (see "Why the temp-file sweep
checks age", also after Step 5).

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
	"time"
)

// ErrNoCredentials means the profile has never been logged in to.
var ErrNoCredentials = errors.New("no stored credentials for profile")

// errCorruptStore marks a readAll failure as unparsable JSON, as opposed to
// an I/O error, so Save can tell the two apart: a corrupt file is something
// Save can quarantine and recover from, an I/O error is not.
var errCorruptStore = errors.New("credentials file is corrupt")

// staleTempAge is how old a leftover temp file must be before Save removes it.
// A temp file belonging to an in-flight Save is milliseconds old, so the
// generous threshold is what keeps cleanup from unlinking a concurrent
// writer's file out from under it — the rename would then fail with ENOENT
// after its write, sync, and close had all succeeded.
const staleTempAge = time.Hour

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

// readAll returns every profile's credentials from disk, or an empty
// (never nil) map if the file does not exist. A parse failure is wrapped in
// errCorruptStore, naming the remedy, so callers can tell "file is malformed"
// apart from other I/O errors and react differently — Save quarantines and
// recovers, Load reports the failure as-is.
func (s CredentialStore) readAll() (map[string]Credentials, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Credentials{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	all := map[string]Credentials{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing %s: %w: %w (delete this file or run `wi login` to reset stored logins)", s.Path, errCorruptStore, err)
	}
	// A file containing literal `null` unmarshals to a nil map with no error,
	// and writing to a nil map panics. Normalize so every non-error return is
	// a usable map — json.MarshalIndent of a nil map emits exactly "null", so
	// this store can otherwise generate the input that crashes it.
	if all == nil {
		all = map[string]Credentials{}
	}
	return all, nil
}

// Load returns the profile's credentials, or ErrNoCredentials.
//
// A corrupt file is reported as-is rather than folded into ErrNoCredentials:
// masking corruption as "never logged in" would send the user chasing the
// wrong problem, and errors.Is(err, ErrNoCredentials) is false for it.
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

// sweepStaleTemp best-effort removes credentials-*.tmp files older than
// staleTempAge from a prior Save that was killed (e.g. SIGKILL) before its
// deferred os.Remove could run. Each holds a refresh token valid for 14 days;
// they're 0600 so not a disclosure risk, but they'd otherwise accumulate
// invisibly. The age check is what keeps this from unlinking a concurrent
// Save's in-flight temp file out from under it. Errors — from the glob, from
// stat, from the remove itself — are ignored: a failed sweep must never fail
// the save it's cleaning up after.
func (s CredentialStore) sweepStaleTemp() {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(s.Path), "credentials-*.tmp"))
	if err != nil {
		return
	}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || time.Since(fi.ModTime()) < staleTempAge {
			continue
		}
		_ = os.Remove(m)
	}
}

// Save writes the profile's credentials, preserving other profiles.
//
// The write is synced to disk and made atomic (temp file plus rename) so an
// interrupted save cannot leave a truncated or zero-length file that locks
// the user out of every profile at once. This does not make the save fully
// durable: after a power loss the rename itself may not have landed, in
// which case the file's previous contents come back — costing a re-login,
// which is proportionate for a file that is entirely re-derivable that way.
//
// Save is read-modify-write with no locking: two `wi` processes refreshing
// tokens in different terminals at the same moment can clobber each other's
// profile entry. That is tolerated rather than fixed here, since the file is
// re-derivable at any time by logging in again — last-writer-wins costs at
// most a re-login, not data loss.
//
// A file that fails to parse is quarantined (renamed to Path+".corrupt",
// keeping the most recent such file around for forensics — a second
// corruption overwrites the first) rather than left blocking every future
// save: the natural recovery path, `wi login`, goes through Save, so Save
// has to be able to recover from corruption Load can only report.
//
// Save replaces whatever is at Path via atomic rename; if Path is a symlink,
// the rename replaces the symlink itself with a regular file rather than
// writing through it, so symlinking this file into a dotfiles repo will stop
// tracking new tokens after the first save.
func (s CredentialStore) Save(profile string, c Credentials) error {
	s.sweepStaleTemp()

	all, err := s.readAll()
	if errors.Is(err, errCorruptStore) {
		if rerr := os.Rename(s.Path, s.Path+".corrupt"); rerr != nil {
			return fmt.Errorf("quarantining corrupt credentials file: %w", rerr)
		}
		all = map[string]Credentials{}
	} else if err != nil {
		return err
	}
	all[profile] = c

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	dir := filepath.Dir(s.Path)
	// 0700 applies only when this call actually creates the directory; a
	// pre-existing directory keeps whatever mode it already had.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// os.CreateTemp's requested 0600 is masked by the process umask like any
	// other open(2): under a restrictive umask such as 0200 the file would
	// come out 0400 without this Chmod, silently failing
	// TestCredentialFileIsNotWorldReadable's exact-0600 assertion on some
	// machines and not others. This call is load-bearing, not belt-and-braces.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temp credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing credentials: %w", err)
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

#### Why `readAll` normalizes and quarantines

Not a step — the code in Step 3 already includes all of this. Recorded because
both bugs were found empirically by code review, both are non-obvious, and both
would be easy to reintroduce while "simplifying" `readAll`:

- `json.Unmarshal([]byte("null"), &all)` sets `all` to `nil` with no error.
  `Save` then panicked on `all[profile] = c` — assignment to a nil map. Since
  `json.MarshalIndent` of a nil map writes exactly `"null"`, the store could
  generate the input that crashed its own decoder. `readAll` now normalizes a
  nil result to an empty map.
- A truncated or 0-byte `credentials.json` (a plausible outcome of a full
  disk or a non-atomic writer elsewhere) failed both `Load` and `Save` with
  the same parse error — including `Save`, so the natural recovery path
  (`wi login`) could not repair the file. `readAll` now wraps parse failures
  in the unexported `errCorruptStore` sentinel; `Save` quarantines the file to
  `Path+".corrupt"` and continues with an empty map, while `Load` still
  reports the failure, with the message naming the remedy.

Also: a corrected comment on the temp file's `Chmod(0o600)` explaining it
normalizes against the process umask rather than being redundant; doc-comment
notes on `Save` about its symlink-replacement behavior and about power-loss
durability; and the remedy parenthetical in the parse-failure message moved to
the end of the sentence (`parsing <path>: credentials file is corrupt:
<detail> (delete this file or run `wi login` to reset stored logins)`) so a
stuck user reads the diagnosis before the fix, not interleaved with it.

#### Why the temp-file sweep checks age

Not a step either — Step 3's `sweepStaleTemp` already includes the fix.
Recorded because the bug shipped once already and the failure mode is easy to
miss in review: it doesn't show up as a build or lint problem, only as an
occasional `rename ...: no such file or directory` under concurrent load.

The sweep was added (see the commit history) to clean up `credentials-*.tmp`
files left behind when a `Save` is killed before its deferred `os.Remove` can
run. The first version removed every match unconditionally. A temp file
belonging to an in-flight `Save` in another goroutine or process also matches
`credentials-*.tmp` — the writer still holds its file descriptor, so its
`Write`/`Sync`/`Close` all succeed regardless, but the final `os.Rename` then
fails because the file the sweep just unlinked is gone. Measured against 50
concurrent `Save` calls: 0 failures before the sweep existed, 7 failures with
the unconditional sweep.

The fix is `staleTempAge`: only remove a match whose `os.Stat` mtime is older
than the threshold. An in-flight temp file is milliseconds old; `time.Hour` is
a large safety margin against a slow-but-legitimate concurrent write, not a
value to tune down. This also means the concurrency limitation documented on
`Save` — a race costs at most a re-login, never an error — is back to being
true; the unconditional sweep had silently falsified it (both concurrent
`Save` calls used to return `nil`, and with the bug one could return an
error).

### Task 4: Base HTTP client and typed errors

**Files:**
- Create: `internal/cli/client/client.go`
- Test: `internal/cli/client/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

The test file's import block is:

```go
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
	"unicode"
	"unicode/utf8"
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
// transparent re-auth use do (added in the next task).
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -v`
Expected: PASS, twenty-four tests (four of them subtests of one table-driven test).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/client.go internal/cli/client/client_test.go
git commit -m "feat(cli): base HTTP client with typed API errors"
```

#### Why `doJSON` wraps `decodeAPIError` with the endpoint

`decodeAPIError`'s own `*APIError.Error()` string carries no endpoint — a bare
`free tier message limit reached (HTTP 403)` gives a CLI user, who has no
network tab, nothing to say which request failed. `doJSON` wraps the returned
error with `fmt.Errorf("%s %s: %w", method, path, decodeAPIError(resp))`,
matching how the transport-error and decode-error paths already report the
method and path. This does not change `*APIError`'s fields or defeat
`errors.As(err, &apiErr)`: `%w` still chains through to the `*APIError`
beneath, so `apiErr.Status`, `apiErr.Code`, and `apiErr.Message` are unaffected
and the three originally-specified tests pass unmodified.

#### Why `decodeAPIError` drains the body after its limited read

`io.ReadAll(io.LimitReader(resp.Body, 64<<10))` stops at 64KB. If the actual
body is larger, the unread remainder is left on the connection, and
`net/http` cannot reuse that connection for a future request — every
oversized error response would otherwise force a fresh TCP (and TLS)
handshake. `decodeAPIError` follows the limited read with
`_, _ = io.Copy(io.Discard, resp.Body)` to drain whatever is left, discarding
both return values since a failure to drain leaves the connection merely
unreusable, not the request itself in error.

#### Why `doJSON` normalizes a missing leading slash

`c.BaseURL + path` is a plain string concatenation. A caller that passes
`"chat"` instead of `"/chat"` would build `.../apichat` instead of
`.../api/chat` — a wrong URL that still parses, so nothing catches the typo
until the server 404s in a confusing way. `doJSON` prepends `/` when `path`
doesn't already start with one, so the concatenation is correct regardless of
how a call site spells its path.

#### Why non-envelope error and success bodies carry a snippet

A code-review mutation-testing pass found the original test suite didn't pin
several of the behaviors above — 6 of 8 injected mutations survived — and
separately measured what a CLI user actually sees on three common failures:
a captive portal or corporate proxy returning HTML on a 200, a proxy's HTML
502, and a JSON error body that doesn't match `models.ErrorResponse`. In all
three cases the user got either a bare `server returned 502` or a raw JSON
syntax error with no indication that non-JSON (or unrecognized-shape JSON)
came back at all. For a CLI with no network tab, that is the single most
useful piece of information missing from the error path — and it is
common, not exotic: any user behind a captive portal or corporate proxy hits
it before they hit a real API error.

`APIError` gains a fourth field, `Snippet`, populated only when neither
`Message` nor the deprecated `Error` field yielded anything — so a caller
that already has a real message never sees a change in behavior, and
`Status`/`Code`/`Message` keep their existing meaning exactly. `Error()`
falls back to including the snippet only when `Message` is empty. On the
success path, `doJSON` now reads the full body with `io.ReadAll` before
attempting `json.Unmarshal` rather than streaming into `json.NewDecoder`
directly; this both lets a decode failure be diagnosed against the raw bytes
(via `looksLikeJSON`, distinguishing "not JSON at all" from "JSON that
doesn't match the struct") and fixes a real connection-reuse gap the same
review measured: `json.Decoder.Decode` can return before reading the rest of
a non-JSON body, leaving the connection undrained.

#### Why `doJSON` falls back to `http.DefaultClient`

`Client.BaseURL` and `Client.HTTP` are both exported, so `&Client{BaseURL:
x}` compiles without going through `New` — and then panics the first time
`doJSON` calls `c.HTTP.Do` on a nil `*http.Client`. `httpClient()` falls back
to `http.DefaultClient` when `c.HTTP` is nil, so a struct literal degrades to
an unbounded-timeout client rather than panicking; `Client`'s doc comment
says `New` is the intended constructor precisely because a struct literal
skips both this and the `BaseURL` trailing-slash normalization.

#### Why `snippet` strips non-printable characters, not just collapses whitespace

A follow-up review pass found that `snippet`'s original whitespace-only
collapsing let ANSI/terminal control sequences through untouched — and
`Snippet` is server-controlled text that gets printed straight to the user's
terminal (a CLI today, a TUI from milestone 3 on). A hostile or compromised
server, a malicious proxy, or a spoofed captive portal could embed an escape
sequence in its response body and have the CLI print it verbatim: `[2J`
clears the user's screen, an OSC sequence (`]0;...`) renames the
terminal tab, and BEL/backspace/NUL can be used to forge misleading output.
This is not a hypothetical for a diagnostic feature that exists specifically
to surface bytes from servers that are *already* misbehaving.

`snippet` now runs `strings.Map` with `unicode.IsPrint` over the
whitespace-collapsed text, dropping every rune it reports as non-printable —
which includes ESC, BEL, backspace, and NUL, but not the ASCII space, so the
collapsed spacing from `strings.Fields`/`strings.Join` survives unchanged.

#### Why the success-path read is capped, and other minor hardening

Three smaller issues came out of the same review pass:

- **Unbounded success-body read.** `decodeAPIError`'s 64KB cap only applies to
  non-2xx responses; the success path's `io.ReadAll(resp.Body)` had no cap at
  all, so a broken or hostile server answering 200 with an unbounded stream
  could drive the CLI to OOM. It is now wrapped in
  `io.LimitReader(resp.Body, maxSuccessBody)`, a named 32MB constant — far
  larger than any real JSON API response, existing only to bound a
  misbehaving server rather than to constrain legitimate payloads.
- **Dangling colon on an empty snippet.** When `snippet(raw)` returns `""`
  (an empty success body, say), the not-JSON error used to read
  `response was not JSON: : unexpected end of JSON input` — a stray `: :`.
  `doJSON` now substitutes `"empty response body"` when the snippet is empty,
  so the message reads `response was not JSON: empty response body: ...`.
- **Attachment limitation.** `doJSON` always JSON-unmarshals its result, so it
  cannot be used for a large binary payload such as milestone 5's image
  downloads — a note on `doJSON`'s doc comment now says so, so nobody reaches
  for it there and has to discover the limitation by debugging.

### Task 5: Login and refresh calls

**Files:**
- Create: `internal/cli/client/auth.go`
- Test: `internal/cli/client/auth_test.go`

#### Why `refreshTokens` returns `(refreshed bool, err error)` instead of `error`

A naive `refreshTokens(ctx, staleAccess) error` conflates two different
failures: the token *exchange* against `/user/refresh` failing (the refresh
token itself is dead — nothing to retry with), and the subsequent *persist*
via `OnRefresh` failing (a read-only credentials file, a full disk — the
client is holding perfectly good tokens in memory, it just couldn't write
them to disk). If persisting fails, the caller (`do`, Task 6) has a choice:
treat it as a hard failure and return the original 401 for the rest of the
process's life, or retry anyway because the in-memory tokens are fine. The
right answer is the second one — a `chmod -w` on the credentials file
shouldn't break every authenticated request until the process restarts.

The two-value return makes that distinction visible at the call site instead
of forcing the caller to inspect error internals: `refreshed` says whether
the exchange succeeded (retry-worthy or not), `err` carries diagnostic detail
that a caller may log but must not use to decide whether to retry. See
`TestRefreshSucceedsWhenPersistFails` and (in Task 6) `TestDoSucceedsWhenPersistFails`.

#### Why a refresh waiter re-reads the token instead of trusting `<-existing`

The single-flight guard means only one goroutine ("the leader") actually
calls `/user/refresh`; everyone else ("waiters") blocks on the leader's
`inflight` channel and wakes when it closes. A version of this that just
returns success once the channel closes is wrong: if the leader's exchange
failed, every waiter would believe it succeeded and retry with the same
stale, already-401'd token — one guaranteed-doomed extra round trip per
waiter.

The fix doesn't add a second piece of shared state (an error field or
channel) to carry the leader's outcome — that would itself need
synchronizing, and could race with the leader clearing `c.inflight`. Instead,
each waiter re-reads `c.tokens.Access` under the mutex after the channel
closes and compares it to the `staleAccess` it saw fail: a changed access
token is proof the leader's exchange succeeded, because only a successful
exchange changes it. See `TestRefreshWaitersLearnLeaderFailure`.

#### Why the leader's cleanup is a defer

The leader's cleanup — clearing `c.inflight` and closing `done` — originally
ran only after `doJSON` returned normally. If `doJSON` panics instead (a
panicking `http.RoundTripper`, for instance — not a normal error, an actual
panic), that cleanup code never runs: `c.inflight` stays set and `done` is
never closed. Every later caller becomes a waiter blocked forever on a
channel nobody will ever close, wedging the client for the rest of the
process. An unrecovered panic kills the process today, so this was latent —
but a caller that recovers (a TUI event loop, for instance, in a later
milestone) would inherit a permanently wedged client.

The fix wraps the `doJSON` call in an inner closure whose `defer` always
runs — panic or normal return — and does the cleanup there. `recover()`
inside that defer is what lets it tell "doJSON panicked" apart from "doJSON
returned normally with a nil `err`": both would otherwise look identical to
the zero-valued `err`, and mistaking a panic for a nil error would adopt a
zero-valued `resp` as the new tokens. On a panic, cleanup still runs (no
tokens adopted) and the panic is re-raised afterward so it keeps propagating
to the caller exactly as it would have without the defer — this does not
turn a would-be-fatal panic into a silently swallowed one. See
`TestRefreshSurvivesLeaderPanic`.

This also affects two invariants worth stating explicitly, because a future
edit could easily break either while "cleaning up" this function further:
tokens must be adopted **before** `OnRefresh` runs (see
`TestOnRefreshCanReadClientStateWithoutDeadlock`, which checks it from the
callback's side), and `OnRefresh` must **not** be called while `c.mu` is
held — nothing stops a future `OnRefresh` from calling back into `c.Tokens()`
or another locking method, and calling it under the lock would deadlock that
(`sync.Mutex` is not reentrant), or serialize every request behind whatever
`OnRefresh` does (an fsync to the credentials file, say).

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -run 'TestLogin|TestRefresh' -v`
Expected: FAIL — `c.Login undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// Login exchanges credentials for a token pair and stores it on the client.
// usernameOrEmail matches the server, which accepts either.
//
// Login calls doJSON directly rather than do: there is no token to refresh
// yet, so the 401-retry wrapper has nothing to add here and would only add a
// pointless extra accessToken() read.
func (c *Client) Login(ctx context.Context, usernameOrEmail, password string) (models.UserResponse, error) {
	req := models.UserLoginRequest{Username: usernameOrEmail, Password: password}
	var resp models.LoginResponse
	if err := c.doJSON(ctx, http.MethodPost, "/user/login", req, &resp); err != nil {
		return models.UserResponse{}, err
	}
	c.setTokens(Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken})
	return resp.User, nil
}

// setTokens replaces the client's token pair under the mutex.
func (c *Client) setTokens(t Tokens) {
	c.mu.Lock()
	c.tokens = t
	c.mu.Unlock()
}

// refreshTokens exchanges the refresh token for a new pair. It reports
// whether the exchange itself succeeded, separately from any error.
//
// The distinction matters: a failed *persist* (read-only credentials file,
// full disk) still leaves the client holding usable tokens in memory, so the
// caller (do) should retry the request rather than fail it. Only a failed
// *exchange* means the request cannot succeed. So:
//
//   - exchange fails: (false, err).
//   - exchange succeeds, no OnRefresh, or OnRefresh returns nil: (true, nil).
//   - exchange succeeds but OnRefresh errors: (true, wrappedErr) — the new
//     tokens are adopted in memory first, then OnRefresh is called, so the
//     error reflects only the persist step.
//   - another goroutine already refreshed (see staleAccess below): (true, nil).
//
// staleAccess is the access token the caller saw fail. If the client's token
// has already moved on, another goroutine refreshed first and this returns
// immediately — so a burst of concurrent 401s produces exactly one refresh
// request rather than a stampede against /user/refresh.
//
// refreshTokens calls doJSON, never do: do calls refreshTokens on a 401, so
// going through do here would recurse the moment a refresh token itself came
// back expired.
func (c *Client) refreshTokens(ctx context.Context, staleAccess string) (bool, error) {
	c.mu.Lock()
	if c.tokens.Access != staleAccess {
		c.mu.Unlock()
		return true, nil
	}
	if existing := c.inflight; existing != nil {
		c.mu.Unlock()
		select {
		case <-existing:
			// The leader's outcome isn't stored anywhere — inferring it from
			// whether the token moved avoids a second piece of shared state
			// (an error channel/field) that would itself need synchronizing
			// and could race with the leader clearing c.inflight. A changed
			// access token is proof the leader's exchange succeeded; if it
			// didn't succeed, the token is still staleAccess and the waiter
			// correctly reports failure rather than retrying with a token
			// that's known to be dead.
			c.mu.Lock()
			changed := c.tokens.Access != staleAccess
			c.mu.Unlock()
			return changed, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	done := make(chan struct{})
	c.inflight = done
	refresh := c.tokens.Refresh
	c.mu.Unlock()

	// The leader's cleanup — clearing c.inflight and closing done — runs in a
	// defer inside this inner closure so it happens no matter how doJSON
	// exits, panic included. Without this, a panicking RoundTripper (or
	// anything else that panics out of doJSON) leaves c.inflight set and
	// done unclosed forever: every later caller becomes a waiter blocked on
	// a channel nobody will ever close, wedging the client for the rest of
	// the process. Today an unrecovered panic kills the process anyway, so
	// this is latent — but a caller that recovers (a TUI event loop, say)
	// would otherwise inherit a permanently wedged client.
	//
	// recover() is what lets this tell "doJSON panicked" apart from
	// "doJSON returned normally with a nil err" — both would otherwise look
	// identical to the zero-valued err below, and treating a panic as a nil
	// error would adopt a zero-valued resp as the new tokens. On a panic,
	// cleanup still runs (inflight cleared, done closed, no tokens adopted)
	// and the panic is re-raised afterward so it keeps propagating to the
	// caller exactly as it would have without this defer.
	var resp models.LoginResponse
	var err error
	var updated Tokens
	func() {
		defer func() {
			r := recover()
			c.mu.Lock()
			c.inflight = nil
			if r == nil && err == nil {
				updated = Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken}
				c.tokens = updated
			}
			c.mu.Unlock()
			close(done)
			if r != nil {
				panic(r)
			}
		}()
		err = c.doJSON(ctx, http.MethodPost, "/user/refresh", models.RefreshTokenRequest{RefreshToken: refresh}, &resp)
	}()

	if err != nil {
		return false, err
	}

	// The exchange succeeded and the new tokens are already live in memory
	// (set above, before OnRefresh runs) — so a persist failure below is
	// reported through err but must not flip the reported bool to false.
	if c.OnRefresh != nil {
		if persistErr := c.OnRefresh(updated); persistErr != nil {
			return true, fmt.Errorf("persisting refreshed tokens: %w", persistErr)
		}
	}
	return true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -race -count=20 -v`
Expected: PASS. `-race` matters because the single-flight test is the one
place in this milestone where concurrency bugs are plausible; `-count=20`
matters because single-flight bugs are frequently intermittent rather than
deterministic. Also run `GOMAXPROCS=1 go test ./internal/cli/client/ -race
-count=10` — a single-P schedule forces different interleavings than the
default and has caught issues the default schedule didn't.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/auth.go internal/cli/client/auth_test.go
git commit -m "feat(cli): login and single-flight token refresh"
```

### Task 6: Transparent re-auth on 401

**Files:**
- Modify: `internal/cli/client/client.go`
- Test: `internal/cli/client/client_test.go`

#### Why `do` retries whenever `refreshed` is true, ignoring `refreshErr`

Task 5's `refreshTokens` separates "the exchange succeeded" from "the persist
succeeded" precisely so `do` can act on the first and ignore the second. If
`do` failed the request whenever `refreshTokens` returned any error at all —
including a persist error — then a read-only credentials file or a full disk
would turn every single authenticated request into a failure for the rest of
the process's life, even though the client is holding a perfectly valid
access token in memory the whole time. `do` retries on `refreshed == true`
regardless of `refreshErr`, and there's no logger in this package (nothing
else in `internal/cli` logs either) to hand `refreshErr` to, so it's dropped
rather than routed somewhere that would itself risk failing the request. See
`TestDoSucceedsWhenPersistFails`.

#### Why the `stale`-read-before-`doJSON` race is left alone

`do` reads `stale := c.accessToken()` before calling `doJSON`, which re-reads
the token itself (under the mutex) when it builds the request. A concurrent
refresh landing in the gap between those two reads means `stale` might not be
the token actually sent on the wire — so if `doJSON` comes back 401, the
retry could in rare cases be triggered by a token that wasn't `stale` at all.
This is deliberately left unfixed: the worst case is one extra 401 round
trip, and closing the gap would mean threading an explicit token parameter
through `doJSON`, breaking its signature and its 24 existing tests, for a
race with no user-visible consequence.

#### Why `do`'s `!refreshed` branch surfaces `refreshErr` instead of the bare original error

The first version of this branch just returned the original 401 whenever
`refreshed` was false. That's wrong in two different ways, both surfaced by
the same underlying problem: it throws away exactly the information that
would tell the user what to do next.

The 401 reads as "your access token is expired" — which is precisely the
thing `refreshTokens` just silently tried, and failed, to fix. For the
14-day refresh-token-expired path every user eventually hits, that means the
error the user actually sees names the symptom refreshTokens already fixed
the diagnosis of, and gives no hint that the fix is `wi login`. For a
context cancellation during the waiter branch (Ctrl-C, or a timeout), the
401 is actively misleading: `refreshTokens` correctly returns
`(false, ctx.Err())`, and the bare 401 discards that entirely, so a caller
checking `errors.Is(err, context.Canceled)` never sees it.

The fix branches on what `refreshErr` actually is: a context error surfaces
as itself (so `errors.Is` works for a caller that cares about cancellation);
any other non-nil `refreshErr` gets wrapped together with the original `err`
using two `%w` verbs — the same double-`%w` pattern already used in
`internal/cli/config/credentials.go` — so `errors.As` still finds the
original `*APIError` and `errors.Is`/`errors.As` still find whatever's inside
`refreshErr`, with human-readable prose in between that names the remedy
(`wi login`) plainly. See `TestDoSurfacesReauthNeededOnExpiredRefreshToken`
and `TestDoCancellationDuringRefreshSurfacesAsContextError`.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client/client_test.go`. Add `"errors"` and `"sync"`
to its import block if not already present (`"sync/atomic"` and `"time"` are
already imported by Task 4's tests).

```go
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

// TestDoSurfacesReauthNeededOnExpiredRefreshToken is the regression test for
// an expired refresh token (the 14-day path every user eventually hits): the
// bare original 401 tells the user "your access token is expired," which is
// exactly what do() just silently, and unsuccessfully, tried to fix. The
// returned error must say something actionable instead, and still let a
// caller recover the original *APIError via errors.As.
func TestDoSurfacesReauthNeededOnExpiredRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"refresh token expired","code":"token_expired"}`))
		case "/thing":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"token is expired"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil)
	if err == nil {
		t.Fatal("do returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "wi login") {
		t.Errorf("error = %q, want it to mention `wi login`", err.Error())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(err, &apiErr) = false, want true")
	}
}

// TestDoCancellationDuringRefreshSurfacesAsContextError is the regression
// test for a waiter's context being canceled while parked on another
// goroutine's in-flight refresh: the cancellation must surface as itself
// (errors.Is(err, context.Canceled)), not get discarded in favor of the
// unrelated 401 that triggered the refresh in the first place.
func TestDoCancellationDuringRefreshSurfacesAsContextError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			// Slow enough that the waiter's context below is always
			// canceled well before this responds.
			time.Sleep(150 * time.Millisecond)
			w.Write([]byte(`{"access_token":"acc-2","refresh_token":"ref-2"}`))
		case "/thing":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Unauthorized"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})

	// Kick off the leader: its do() call 401s on /thing, then becomes the
	// leader of the slow refresh above.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.do(context.Background(), http.MethodGet, "/thing", nil, nil)
	}()
	defer wg.Wait()
	time.Sleep(20 * time.Millisecond) // give the leader time to claim c.inflight

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	err := c.do(ctx, http.MethodGet, "/thing", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want errors.Is(err, context.Canceled) = true", err)
	}
}

// TestDoDoesNotRetryWhenRefreshFails pins client.go's !refreshed branch
// returning without a second doJSON call: when the refresh itself fails,
// there is no new token to retry with, so the protected endpoint must be
// hit exactly once — a retry here would just be a second, equally doomed,
// request.
func TestDoDoesNotRetryWhenRefreshFails(t *testing.T) {
	var thingCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/refresh":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"refresh token expired"}`))
		case "/thing":
			atomic.AddInt32(&thingCalls, 1)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Unauthorized"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1", Refresh: "ref-1"})
	if err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil); err == nil {
		t.Fatal("do returned nil error, want error")
	}
	if got := atomic.LoadInt32(&thingCalls); got != 1 {
		t.Errorf("/thing called %d times, want exactly 1 — no retry when the refresh itself failed", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/client/ -run TestDo -v`
Expected: FAIL — `c.do undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cli/client/client.go`. Add `"errors"` to its import block.

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/client/ -race -count=20 -v`
Expected: PASS, all client tests. `-count=20` matters here — single-flight
and concurrency bugs are intermittent, and this is the one package in the
milestone where that risk is real. Also run `GOMAXPROCS=1 go test
./internal/cli/client/ -race -count=10` for the same reason as Task 5.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client/
git commit -m "feat(cli): transparent token refresh on 401"
```

#### Follow-up: panic safety and re-auth error surfacing

A later review pass on this task found the leader-panic wedge and the
error-surfacing gaps described in the two `#### Why` sections above; they
weren't part of the original implementation and were fixed in a follow-up
commit rather than folded silently into this task's history:

```bash
git add internal/cli/client/
git commit -m "fix(cli): survive refresh panics and surface re-auth failures"
```

### Task 7: List chats

**Files:**
- Create: `internal/cli/client/chat.go`
- Test: `internal/cli/client/chat_test.go`

#### Why `ListChats` calls `do`, not `doJSON`

`ListChats` is the first (and, in this milestone, only) resource call in the
package — everything before it was auth plumbing. It would be easy to reach
for `doJSON` here since there's no obvious retry concern at the call site,
but that would silently drop transparent re-auth from the one place a user
actually feels it: an access token expiring mid-session while browsing
chats. `TestListChatsRefreshesExpiredToken` pins this by 401'ing the first
`/chat` request and succeeding only after the refreshed token appears on the
retry, so swapping `do` for `doJSON` fails the test outright rather than
just losing a property nothing checks.

#### Why a zero `Limit` omits the query param instead of defaulting it

The server's own default page size is 10
(`internal/handlers/chat/chat.go`), which is easy to miss and easy to regress
into: a listing that "just works" in casual testing (few chats) silently
truncates once a user has more than 10. The fix isn't to have the client
invent a bigger default, though — that would make `internal/cli/client` a
place with presentation opinions instead of a thin transport. Task 10's `wi
chats` command is where that policy belongs (it will pass an explicit
`defaultChatLimit = 100`). `ListChats` only omits the param when
`opts.Limit <= 0`, with a comment at the call site explaining that the
omission is deliberate, and `TestListChatsZeroLimitOmitsParam` pins the zero
value's actual wire behavior so the split doesn't quietly erode later.

#### Why both an empty and a missing `results` field are tested

`ChatPage.Results` is `[]models.Chat`, and `encoding/json` leaves a struct
field at its zero value when the field is absent from the payload, or
explicitly `null` — a nil slice, not a decode error. `TestListChatsEmptyResults`,
`TestListChatsMissingResultsField`, and `TestListChatsNullResultsField` pin
all three of the server's ways of saying "no chats" (`"results":[]`,
omitting `results` outright, and `"results":null`) so a caller ranging over
the returned slice never has to special-case any of them.

#### Why `ListChats` returns a `ChatPage`, not just `[]models.Chat`

The first version of this method returned `page.Results` directly and
dropped `total_count` on the floor — the pagination envelope was decoded and
then thrown away three lines later. That's the same "looks like missing
data" failure the `Limit`-omission comment above exists to prevent, just
relocated one layer up: with `total_count: 347` on the wire and `Limit: 100`,
a caller gets 100 chats back and no way to learn there were 347 — a
truncated listing that's indistinguishable from an account that only has
100 chats. `ChatPage` surfaces `TotalCount` alongside `Results` specifically
so `wi chats` (Task 10) can say "showing 100 of 347" instead of silently
lying by omission. `TestListChatsDecodesTotalCount` pins this with a
response where `total_count` exceeds `len(results)`.

The envelope's `page` number is dropped rather than carried onto `ChatPage`:
nothing in this package or its only planned caller reads it yet, and an
unread field is exactly the kind of dead weight this correction exists to
avoid reintroducing. It can come back once pagination auto-follow or a
page-aware command actually consumes it.

- [ ] **Step 1: Write the failing test**

```go
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

// ChatPage is one page of a chat listing.
//
// TotalCount is returned alongside the results because a listing is capped by
// the caller's limit: without it, a command showing 100 of 347 chats has no way
// to say so, and a truncated list is indistinguishable from missing data.
//
// The server's envelope also carries a page number, but nothing here consumes
// it yet, so it is deliberately left undecoded rather than kept as dead
// weight — it can come back once pagination actually lands and something
// reads it.
type ChatPage struct {
	Results    []models.Chat `json:"results"`
	TotalCount int           `json:"total_count"`
}
```

> **Superseded (issue #4).** Milestone 2 needed a second paginated listing
> (`ChatMessagePage`), so this struct became a generic `Page[T]` in
> `internal/cli/client/page.go` with `ChatPage = Page[models.Chat]` as a type
> alias — every use above still compiles and behaves identically, and the
> rationale in the doc comment moved with it. The drift guard was renamed
> and generalized to `TestPageMatchesPaginatedResponseEnvelope`
> (`page_test.go`), which now runs once per aliased element type.

```go

// ListChats returns one page of the user's chats. It calls do, not doJSON, so
// an expired access token refreshes transparently — the same guarantee every
// other resource call in this package gets.
func (c *Client) ListChats(ctx context.Context, opts ListChatsOptions) (ChatPage, error) {
	q := url.Values{}
	if opts.Archived {
		q.Set("archived", "true")
	}
	if opts.Search != "" {
		q.Set("search", opts.Search)
	}
	// The server defaults to a page size of 10 when limit is omitted
	// (internal/handlers/chat/chat.go). Leaving Limit <= 0 unset here is a
	// deliberate choice, not an oversight: this package stays a thin
	// transport, and it is the caller's job to decide what "list chats"
	// should default to (the wi chats command passes an explicit limit).
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}

	path := "/chat"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var page ChatPage
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return ChatPage{}, err
	}
	return page, nil
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
- Test: `cmd/whatiff-cli/main_test.go`

#### Why `-h`/`--help` gets its own branch instead of `flag.Usage`

The package-level `flag` API exits 2 on `-h`/`--help` unconditionally — right
for a genuine usage error, wrong for someone deliberately asking for help.
`main` uses its own `flag.NewFlagSet("wi", flag.ContinueOnError)` with output
discarded, so nothing is printed until `main` decides which of the two cases
it is: `errors.Is(err, flag.ErrHelp)` (covers `-h`, `-help`, and `--help` —
`flag` treats one and two leading dashes the same) prints usage to stdout and
exits 0; any other parse error prints to stderr and exits 2. A bare `wi help`
gets the same stdout/exit-0 treatment as a first positional argument, since
"help" reads as a request for help whether or not it's spelled as a flag.

#### Why `main` wires `signal.NotifyContext` instead of `context.Background()`

`internal/cli/client`'s `do`/`refreshTokens` already special-cases
`context.Canceled` so a canceled context unwinds a request cleanly instead of
surfacing as a confusing 401-adjacent error (see that package's doc
comments) — but nothing ever canceled the context that reached it, which made
that handling unreachable and left Ctrl-C to just kill the process mid-request.
`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`
gives Ctrl-C (and a container's SIGTERM) something to cancel; `stop` is
deferred so the signal handler is released once `main` returns normally.
`main` then checks `errors.Is(err, context.Canceled)` on the way out and
prints a short "cancelled" instead of the generic wrapped-error message —
the user asked for this, so it should not read as a crash.

#### Why a subcommand FlagSet cannot just return `fs.Parse`'s error to `run`

An earlier version of this file let `runChats` return `fs.Parse(args)`'s
error straight through to `run`, which only had two outcomes to give any
error: success, or `main`'s generic `exit(1)` with the error message repeated
on stderr. That made `wi chats --help` behave nothing like `wi --help` —
`flag`'s own default handling printed to stderr and returned a plain error,
which `main` then re-wrapped and printed *again*, exiting 1 instead of the
top-level convention's 0 for help and 2 for a genuine mistake. `main` now
defines two sentinel errors, `errHelpRequested` and `errFlagUsage`, and a
`parseSubFlags` helper every subcommand FlagSet must be parsed through: it
prints exactly once, to the stream the case calls for (stdout for help,
stderr for a bad flag), and returns the matching sentinel so `main`'s error
switch can pick the same 0/2 exit codes the top level uses — without
`parseSubFlags` printing the message a second time itself. `printSubUsage`
exists alongside it because `fs.Usage` is nil unless a caller sets it, and
`fs.Parse`'s own internal usage call already fired (harmlessly, into
`io.Discard`) before `parseSubFlags` gets to inspect the error and choose a
destination stream — calling `fs.Usage()` directly here would nil-panic.

#### Why `loadProfile` wraps a `Resolve` failure with the config file path

`config.Resolve`'s own errors (`internal/cli/config/config.go`) are already
good — they name the requested profile and list what's available — but they
say nothing about *where* that list came from. A user with no config file at
all, running `wi --profile nope chats`, would see `no profile named "nope"
(available: none)` with no way to tell whether "none" means "you have no
config file" or "your config file exists and genuinely defines nothing."
`loadProfile` appends `(config file: <path>)` to any `Resolve` error — using
the path `config.DefaultPath()` already computed, even when nothing exists at
it yet, since that path is exactly where the user would create one.

There is no unit test for `main` itself, the signal wiring, or
`parseSubFlags`/`printSubUsage`'s stdout/stderr routing: all three need a
real process, a real terminal, or output-stream inspection that a plain unit
test can't easily do. `loadProfile` is unit tested below since it is pure;
the rest is covered by manual verification in Task 10's step 4 and Task 11's
end-to-end check.

- [ ] **Step 1: Write the implementation**

```go
// Command whatiff-cli (installed as `wi`) is a terminal client for WhatIff.
//
// Milestone 1 provides `login` and `chats`. Conversation handling arrives with
// the turn engine in milestone 2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/config"
)

func main() {
	// Ctrl-C (and a container's SIGTERM) cancel ctx rather than killing the
	// process outright. The client package already propagates a canceled
	// context correctly through the token-refresh path (see
	// internal/cli/client/client.go's do/refreshTokens) - without this wiring
	// that plumbing had nothing to cancel it, so Ctrl-C just killed the
	// process mid-request instead of letting a request unwind cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A dedicated FlagSet, not the package-level flag.CommandLine, so -h/--help
	// can be routed to stdout+exit(0) below instead of flag's own default of
	// stderr+exit(2): that default is right for a genuine usage error but
	// wrong for someone deliberately asking for help. Output is discarded here
	// so flag never prints on our behalf; both branches below print exactly
	// once, to the stream the case calls for.
	fs := flag.NewFlagSet("wi", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", os.Getenv("WHATIFF_PROFILE"), "config profile to use")
	asJSON := fs.Bool("json", false, "emit JSON instead of human-readable output")

	switch err := fs.Parse(os.Args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		// -h / -help / --help (flag treats one and two leading dashes the
		// same): an explicit request for help, not a mistake. Exit 0.
		usage(os.Stdout)
		os.Exit(0)
	case err != nil:
		fmt.Fprintf(os.Stderr, "wi: %v\n", err)
		usage(os.Stderr)
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	if args[0] == "help" {
		usage(os.Stdout)
		os.Exit(0)
	}

	if err := run(ctx, args[0], args[1:], *profile, *asJSON); err != nil {
		switch {
		case errors.Is(err, errHelpRequested):
			// A subcommand's own FlagSet already printed its usage to stdout
			// (see parseSubFlags) - nothing left to do here but match the
			// exit code main's own --help handling above uses.
			os.Exit(0)
		case errors.Is(err, errFlagUsage):
			// A subcommand's own FlagSet already printed the parse error and
			// its usage to stderr (see parseSubFlags) - do not print err
			// again here, it would just duplicate that output.
			os.Exit(2)
		case errors.Is(err, context.Canceled):
			// Ctrl-C mid-request: the user asked for this, so it is not an
			// error worth a scary wrapped message - just say so and leave
			// with a non-zero status.
			fmt.Fprintln(os.Stderr, "wi: cancelled")
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "wi: %v\n", err)
			os.Exit(1)
		}
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `wi - WhatIff terminal client

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

// errHelpRequested and errFlagUsage let a subcommand's FlagSet report the
// same two special outcomes main's own top-level flag parsing already
// handles above - a satisfied --help (exit 0, already printed to stdout) and
// a genuine bad-flag error (exit 2, already printed to stderr) - without
// falling through to run()'s generic "any error means exit 1". They carry no
// message of their own because parseSubFlags has already written whatever
// there was to write; main only needs to tell them apart from a normal error
// via errors.Is to pick the right exit code.
var (
	errHelpRequested = errors.New("help requested")
	errFlagUsage     = errors.New("flag usage error")
)

// parseSubFlags parses fs against args using exactly the stdout/exit-0
// (help) and stderr/exit-2 (bad flag) split main uses for its own top-level
// flags above. Every subcommand FlagSet must be parsed through this, not
// fs.Parse directly: fs.Parse alone prints to stderr and returns a plain
// error for BOTH -h/--help and a genuine mistake, which is what let
// `wi chats --help` print to stderr and exit 1 (via run()'s generic error
// handling) while `wi --help` printed to stdout and exited 0 twenty lines
// away in the same file - and let a bad flag print its message twice, once
// from flag's own default output and once from main's error wrapper.
func parseSubFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		printSubUsage(fs, os.Stdout)
		return errHelpRequested
	case err != nil:
		fmt.Fprintf(os.Stderr, "wi %s: %v\n", fs.Name(), err)
		printSubUsage(fs, os.Stderr)
		return errFlagUsage
	}
	return nil
}

// printSubUsage writes fs's usage to w, matching flag.FlagSet's own default
// usage format. It does not use fs.Usage()/fs.Usage - that field is nil
// unless a caller sets it, and fs.Parse's internal usage call already fired
// (harmlessly, into io.Discard) before parseSubFlags gets a chance to
// inspect the error and pick a destination stream.
func printSubUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, "Usage of %s:\n", fs.Name())
	fs.SetOutput(w)
	fs.PrintDefaults()
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
//
// A Resolve failure is wrapped with the config file path it was resolved
// against (even when that file does not exist yet - DefaultPath still names
// where it would live): without this, an error like `no profile named
// "nope" (available: none)` leaves a user with no config file wondering
// where "available" was even supposed to come from.
func loadProfile(profileName string) (string, config.Profile, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", config.Profile{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Profile{}, err
	}
	name, profile, err := cfg.Resolve(profileName)
	if err != nil {
		return "", config.Profile{}, fmt.Errorf("%w (config file: %s)", err, path)
	}
	return name, profile, nil
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
		if errors.Is(err, config.ErrNoCredentials) {
			return nil, fmt.Errorf("not logged in to profile %q at %s - run: wi --profile %s login", name, profile.APIURL, name)
		}
		return nil, err
	}

	c := client.New(profile.APIURL, client.Tokens{Access: creds.AccessToken, Refresh: creds.RefreshToken})
	// username is captured once, read-only, rather than closing over the
	// mutable creds variable and read-modify-writing it: two overlapping
	// refreshes (do's single-flight only dedupes within one Client, not
	// across separate goroutines racing this OnRefresh closure via
	// unrelated paths) would otherwise be mutating and reading the same
	// shared creds value with no synchronization. Building a fresh
	// config.Credentials value inside the closure means each call is
	// independent of every other call's timing.
	username := creds.Username
	c.OnRefresh = func(t client.Tokens) error {
		return store.Save(name, config.Credentials{
			AccessToken:  t.Access,
			RefreshToken: t.Refresh,
			Username:     username,
		})
	}
	return &session{profileName: name, profile: profile, client: c}, nil
}
```

- [ ] **Step 2: Write tests for the pure parts**

`loadProfile` is the one pure, easily-testable piece of this file — it
forwards to `config.Load`/`config.Resolve`, both already covered, but
`loadProfile` itself is what `newSession`, `runLogin`, and `runChats` all
depend on, so its own contract (returns the *resolved* name, not the asked-for
one; a bad `--profile` produces an actionable error naming both the profile
and the config file it was resolved against) is worth pinning directly rather
than only indirectly through those three.

```go
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
```

- [ ] **Step 3: Run the tests and verify the binary compiles**

Run: `go test ./cmd/whatiff-cli/ -run TestLoadProfile -race -v && go build ./cmd/whatiff-cli/`
Expected: the three `TestLoadProfile_*` tests pass; the build fails with
`undefined: runLogin` and `undefined: runChats`. That is expected — Tasks 9
and 10 supply them. Do not commit yet.

### Task 9: `wi login`

**Files:**
- Create: `cmd/whatiff-cli/login.go`
- Create: `cmd/whatiff-cli/tty_tcflsh.go`
- Create: `cmd/whatiff-cli/tty_bsd.go`
- Create: `cmd/whatiff-cli/tty_other.go`
- Test: `cmd/whatiff-cli/login_test.go`
- Test: `cmd/whatiff-cli/main_test.go` (adds `TestNewSession_OnRefreshPreservesUsername`)
- Modify: `Makefile` (adds `build-cli-crosscheck`, wired into `pre-commit`)

#### Why `promptLine` takes one shared `*bufio.Reader` instead of building its own

An earlier version of `promptLine` did `bufio.NewReader(os.Stdin).ReadString('\n')`
inline — a fresh `bufio.Reader` on every call. `bufio.Reader` reads ahead in
chunks, so the first call's reader can buffer bytes from stdin past the
newline it returns; a second, distinct `bufio.Reader` constructed for the next
prompt starts with an empty buffer of its own and never sees those
already-consumed bytes. Against a real interactive terminal this went
unnoticed, because the terminal is itself line-buffered and delivers input one
line at a time — the bug only shows up once stdin is piped or comes from a
heredoc, where a multi-line answer can arrive in one `Read`. `runLogin` now
constructs exactly one `*bufio.Reader` over `os.Stdin` and threads it through
both the username and password-adjacent prompts; `promptLine`'s doc comment
spells out why, so the fix doesn't get "simplified" back to the broken form.

`TestPromptLine_SharedReaderSeesBothLines` exercises this — but it is *not* a
mutation test for "a fresh `bufio.Reader` per call," and its own comment says
so explicitly. `bufio.NewReader(rd)` special-cases an `rd` that is already a
`*bufio.Reader` with a large enough buffer and returns it unchanged rather
than wrapping it, so even a `promptLine` that re-wrapped its argument
(`bufio.NewReader(r).ReadString('\n')`) on every call would still pass this
test — `r` comes back as itself. The actual fix is the function's *signature*:
taking `*bufio.Reader`, not `io.Reader`, forces every caller to construct
exactly one reader for a whole prompt sequence, because raw `os.Stdin` (an
`io.Reader` that is *not* already a `*bufio.Reader`) is what the short-circuit
does not apply to, and re-wrapping *that* fresh each call is what the original
bug depended on. The signature makes the bug impossible to reintroduce by
construction; the test only pins ordinary two-line reading behavior.

#### Why a pasted password gets echoed, and what `flushPendingInput` can and cannot do about it

Reproduced against a real pty: piping `"alice\nhunter2\n"` into the terminal
as a single write — exactly what a paste from a password manager looks like —
shows both lines on screen, including the password, *before* `promptPassword`
ever runs. The mechanism is not the `bufio` read-ahead problem above: in
canonical terminal mode the kernel delivers one line per `read()`, so the
username prompt's read only ever consumes `"alice\n"`. The password line is
still sitting in the *kernel tty driver's* own input queue — invisible to any
Go-level buffering — and the driver echoes every byte to the screen the
instant it arrives, because local echo is still on; it is only turned off once
`term.ReadPassword` runs, by which point the password has already been echoed
and is sitting in scrollback.

`flushPendingInput` is the fix, and it is the same trick `sudo` uses
immediately before its own password prompt: `promptPassword` calls it right
before turning off echo, discarding whatever the tty driver has queued so
the stale line can never be silently read back as the password once echo is
off. **It cannot undo the echo that already happened.** The paste is already
on screen and in scrollback; the flush only prevents it from being *used*.
`promptPassword` is honest about this distinction: when `flushPendingInput`
reports something was actually discarded, it prints `warning: discarded
pending input before the password prompt - if you pasted your password it
may be visible in your terminal history` to stderr. An ordinary typed login
— nothing queued when the flush runs — stays silent; the warning is not
printed "just in case."

`flushPendingInput` needs `unix.Poll` (not a `FIONREAD` ioctl) to check
whether anything was pending *before* discarding it: `golang.org/x/sys/unix`
does not export a `FIONREAD` constant in this module's pinned version (or, it
turns out, any version), so a zero-timeout `POLLIN` poll answers the same
yes/no question `flushPendingInput`'s caller needs, without reading (and thus
itself consuming) the queued bytes.

This is the one place `go.mod` changes in this task: `golang.org/x/sys` was
already present as an *indirect* dependency (pulled in transitively); using
`golang.org/x/sys/unix` directly promotes it to a direct one. `go mod tidy`
picks up exactly that move — no version change, no new module, `go.sum`
untouched — and `make tidy` must still pass.

#### Why the flush ioctl is split three ways, one per platform family — and how that was verified

An initial version of `flushPendingInput` lived in a single
`//go:build unix` file using `TCFLSH`/`TCIFLUSH`. `unix` is Go's recognized
shorthand for `aix`, `android`, `darwin`, `dragonfly`, `freebsd`, `hurd`,
`illumos`, `ios`, `linux`, `netbsd`, `openbsd`, and `solaris` together — but
`golang.org/x/sys/unix` does not export `TCFLSH` for most of that list. That
broke the build on every BSD (`darwin`, `dragonfly`, `freebsd`, `netbsd`,
`openbsd`) with `undefined: unix.TCFLSH`, caught by nothing in the pipeline:
CI is Linux-only and the Makefile's own `build` target cross-compiles
`GOOS=linux GOARCH=amd64` exclusively.

The real per-`GOOS` split was determined by grepping every
`zerrors_<goos>_<goarch>.go` file this module ships and then, critically,
confirmed (and in two places, corrected) by cross-compiling for every `GOOS`
Go itself supports rather than trusting either the grep or a header
reference:

- **`TCFLSH`/`TCIFLUSH`** (via `unix.IoctlSetInt`) is exported for `linux`
  (every arch), `aix`, and `solaris`. This lives in **`tty_tcflsh.go`**.
- **`TIOCFLUSH`** (value `0x80047410` on every one of them) is exported for
  `darwin`, `dragonfly`, `freebsd`, `netbsd`, and `openbsd`. Unlike `TCFLSH`,
  its ioctl argument is a pointer to an int bitmask selecting which queue to
  flush, so it needs `unix.IoctlSetPointerInt`, not `IoctlSetInt`. The
  bitmask value for "input queue" (`FREAD`, historically `0x1` on every
  BSD-derived system) is not exported by `golang.org/x/sys/unix` on any of
  these platforms either, so it is a literal with a comment explaining what
  it means, not a symbolic constant. This lives in **`tty_bsd.go`**.
- Everything else falls to the no-op in **`tty_other.go`**.

Two mistakes surfaced only by cross-compiling, not by reading files:

1. The first attempt named the `TCFLSH` file `tty_linux.go`, tagged
   `//go:build linux || aix || solaris`. Cross-compiling for `aix` and
   `solaris` still failed with `undefined: flushPendingInput` — a source
   file named `..._<goos>.go` carries an *implicit* build constraint for
   that `GOOS`, ANDed together with whatever the explicit `//go:build` line
   says, so `tty_linux.go` only ever built for `linux` regardless of what
   its tag claimed. Renaming the file to `tty_tcflsh.go` (a name Go's
   filename matcher does not recognize as any `GOOS`) fixed it; `tty_bsd.go`
   and `tty_other.go` were never affected, since "bsd" and "other" are not
   `GOOS` names either.
2. `illumos` was assumed to have neither constant, since grepping for an
   `illumos`-named file in `golang.org/x/sys/unix` finds nothing. Once (1)
   was fixed, cross-compiling for `illumos` still failed — until
   `go help buildconstraint` revealed that `GOOS=illumos` matches every
   `solaris`-tagged file *in addition to* its own (the same relationship
   `android` has to `linux`, and `ios` has to `darwin`). `tty_tcflsh.go`'s
   `linux || aix || solaris` tag already covers `illumos` as a result — no
   file needed to name it explicitly, and no fourth file was needed.

The three files' tags are each other's exact negation (`tty_other.go`'s is
the explicit negation of the other two, spelled out term by term rather than
as `!(A || B)`, to keep it grep-able), so every buildable `GOOS` matches
exactly one: matching zero fails with `undefined: flushPendingInput`;
matching two fails with a duplicate declaration. `hurd` — part of the `unix`
tag, but with no zerrors file for either constant and no documented alias to
another `GOOS` — falls to `tty_other.go`'s no-op; it is also not a target
this Go toolchain can cross-compile for at all
(`GOOS=hurd go build` reports "unsupported GOOS/GOARCH pair"), so this could
not be verified by building even if desired.

Nothing catches a platform-specific build mistake like this without an
explicit check: `make build-cli-crosscheck` (wired into `pre-commit`)
cross-compiles `cmd/whatiff-cli` for `linux/amd64`, `darwin/amd64`, and
`darwin/arm64` — build only, no tests, fast — specifically so a Linux-only
CI run and a Linux-only local `make build` can no longer both stay green
while a Mac (or BSD) user's build is broken.

`TestNewSession_OnRefreshPreservesUsername` (`main_test.go`, Task 8) is
unrelated to the platform split but landed in the same pass: it is the
direct regression test for `newSession`'s `OnRefresh` closure silently
dropping the stored `Username` on every token refresh — invoking the closure
directly with a fresh token pair and asserting the username survives,
without needing a real HTTP round trip.

#### Why `runLogin`'s empty-username guard is its own function

`validateUsername` used to be two inline lines in `runLogin`
(`if username == "" { return fmt.Errorf(...) }`). `runLogin` itself talks to
a real network and a real terminal, so it can't be unit tested end to end —
which left that guard reachable only by a human actually running `wi login`
and pressing enter at the first prompt. Pulling it out as its own function
gives it a direct test (`TestValidateUsername`) that fails the moment the
guard is weakened or removed, independent of anything else in the login flow.

There is no unit test for `promptPassword`'s TTY-required path or the
`flushPendingInput` build-tagged files themselves — both need a real terminal
(a pty, specifically, to reproduce the paste-echo scenario) rather than
anything `go test` can drive directly. See Task 10's step 4 for the manual
(pty-scripted) verification of both the paste and the ordinary-login cases.

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

	// One shared reader for the whole prompt sequence - see promptLine's doc
	// comment for why a fresh bufio.Reader per call is wrong.
	stdin := bufio.NewReader(os.Stdin)

	username, err := promptLine(stdin, "Username or email: ")
	if err != nil {
		return err
	}
	if err := validateUsername(username); err != nil {
		return err
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

// validateUsername rejects an empty username before any network call is
// attempted.
//
// Pulled out of runLogin as its own function so this guard has a test
// pinning it directly (TestValidateUsername): runLogin itself talks to a
// real network and a real terminal, so it cannot be unit tested end to end,
// which would otherwise leave this check reachable only by a human running
// `wi login` and pressing enter at the first prompt.
func validateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}
	return nil
}

// promptLine prints prompt, then reads one line from r.
//
// r must be a single *bufio.Reader shared across every prompt in a sequence -
// never construct a fresh one per call. bufio.Reader reads ahead in chunks,
// so the first call can buffer bytes past its newline into its own internal
// buffer; a second, distinct bufio.Reader created for the next prompt starts
// with an empty buffer of its own and never sees those already-consumed
// bytes. Against a real interactive terminal this goes unnoticed because the
// terminal itself is line-buffered and delivers input one line at a time, so
// it happened to work "by luck" - but it silently drops input whenever stdin
// is piped or comes from a heredoc (exactly the shape of the regression test
// for this function). Do not "simplify" this back to bufio.NewReader(r) per
// call.
func promptLine(r *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
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

	// Discard anything already queued by the terminal driver before turning
	// off echo. A paste of "username\npassword\n" delivered as one write
	// leaves the password line sitting in the driver's input queue after the
	// username prompt above has consumed its own line - and that queued line
	// gets echoed to the screen the instant it arrives, before this function
	// ever runs, because echo is still on until term.ReadPassword below
	// turns it off. See flushPendingInput's doc comment (tty_tcflsh.go, with
	// tty_bsd.go and tty_other.go covering the rest of the build matrix) for
	// why this is the same defense sudo uses. The error return is ignored
	// deliberately: it is best-effort hardening, not a prerequisite for
	// reading a password, so a failing ioctl must not fail the whole login -
	// but discarded itself is still trustworthy even when flushPendingInput
	// also returned an error, since it reflects only what Poll observed, not
	// whether the flush that followed succeeded.
	discarded, _ := flushPendingInput(fd)
	if discarded {
		// This cannot undo the echo that already happened - only prevent the
		// stale bytes from being silently accepted as the password. Say so
		// plainly rather than letting a user assume the paste never reached
		// the screen.
		fmt.Fprintln(os.Stderr, "warning: discarded pending input before the password prompt - if you pasted your password it may be visible in your terminal history")
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

```go
//go:build linux || aix || solaris

package main

import "golang.org/x/sys/unix"

// flushPendingInput discards any input already queued by the terminal
// driver for fd, and reports whether it actually discarded something.
//
// This is the same trick sudo uses immediately before its own password
// prompt. The problem it defends against: pasting "alice\nhunter2\n" into a
// terminal arrives at the tty driver as one write, and the driver echoes
// every byte of it - including the password half - to the screen as it
// lands, because local echo is still on at that point; it is only turned off
// once promptPassword calls term.ReadPassword, by which time the password
// has already been echoed and is sitting in scrollback. In canonical mode
// the kernel delivers one line per read(), so the prompt sequence's username
// read consumes only "alice\n" and leaves "hunter2\n" queued in the driver's
// own buffer, invisible to anything done in Go-level buffering (bufio.Reader
// included) - only the kernel can discard it. TCIFLUSH does that, so the
// stale line can never be silently read back as the password once echo is
// off. It cannot undo the echo that already happened - the caller is
// responsible for warning about that separately, using this function's
// reported bool to know whether there was anything to warn about.
//
// This file is one of three platform-specific implementations, split by
// which ioctl request constant golang.org/x/sys/unix actually exports for
// the target GOOS - verified by cross-compiling for every GOOS Go supports,
// not by trusting a header reference or a filename grep (both misled an
// earlier version of this split - see the note on the file name below and
// the illumos note further down). TCFLSH is exported for linux (every arch
// this repo might target), aix, and solaris. tty_bsd.go covers the
// platforms where x/sys/unix instead exports TIOCFLUSH; tty_other.go is the
// no-op fallback for every remaining GOOS.
//
// This file is deliberately NOT named tty_linux.go. A source file whose name
// ends in "_<goos>.go" gets an implicit build constraint for that GOOS,
// ANDed together with whatever an explicit "//go:build" line says - so
// tty_linux.go with a "//go:build linux || aix || solaris" line would still
// only ever build for linux, silently excluding aix and solaris despite the
// tag naming them (confirmed by cross-compiling: both failed with
// "undefined: flushPendingInput" until this file was renamed). tty_bsd.go
// and tty_other.go are unaffected only because "bsd" and "other" are not
// recognized GOOS names Go's file-name matching looks for.
//
// illumos is covered by this file too, via its "linux || aix || solaris"
// tag, even though no zerrors_illumos_*.go file exists anywhere in this
// module: `go help buildconstraint` documents that GOOS=illumos matches
// every "solaris"-tagged file in addition to its own, and cross-compiling
// confirms unix.TCFLSH really does resolve for GOOS=illumos as a result. A
// filename-only search for "illumos" (which is how the platform set here
// was first drafted) misses this and wrongly concludes illumos has no
// working ioctl constant at all.
//
// Whether anything was pending is checked with a zero-timeout unix.Poll
// (POLLIN) immediately before the flush, not by reading: a read would
// consume bytes itself rather than leaving the flush to do it, and this
// package has no portable FIONREAD-equivalent available in
// golang.org/x/sys/unix to ask the kernel "how many bytes" directly across
// every GOOS this build tag covers. Poll only answers yes/no, which is all
// the caller needs to decide whether to print a warning.
//
// The poll result and the flush are independent facts, and both are
// attempted unconditionally: an earlier version returned early on a Poll
// error (so a stray EINTR would skip the flush entirely, leaving a stale
// password line in place) and discarded a true "pending" the moment the
// ioctl itself failed (so "something was pending but the flush failed" was
// indistinguishable from "nothing was pending" - silently accepting the
// stale pasted line as the password with no warning at all, the exact
// failure this function exists to prevent). pending now reflects only what
// Poll observed, err reports whichever step failed (flush takes priority,
// since a caller that only checks err for logging purposes should see the
// more actionable failure), and the flush always runs regardless of whether
// Poll succeeded.
func flushPendingInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(fds, 0)
	pending := pollErr == nil && n > 0 && fds[0].Revents&unix.POLLIN != 0

	flushErr := unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)

	err := flushErr
	if err == nil {
		err = pollErr
	}
	return pending, err
}
```
```go
//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package main

import "golang.org/x/sys/unix"

// flushBSDInputQueue selects TIOCFLUSH's input-queue argument: the BSD
// family's TIOCFLUSH ioctl takes a pointer to an int bitmask naming which
// queue(s) to flush (0 means both), built from the historic BSD
// sys/fcntl.h FREAD/FWRITE bits. golang.org/x/sys/unix does not export
// either constant on darwin/dragonfly/freebsd/netbsd/openbsd (checked by
// grepping the whole module, not just this platform's generated file), so
// the literal is spelled out here instead of a symbolic name that does not
// exist. FREAD is 0x1 on every BSD-derived system, a value that predates
// and is far more stable than anything this file needs to track.
const flushBSDInputQueue = 0x1

// flushPendingInput discards any input already queued by the terminal
// driver for fd, and reports whether it actually discarded something.
//
// See tty_tcflsh.go's doc comment for the full rationale (the sudo-style
// paste defense, and why both the poll result and the flush attempt are
// unconditional and independent). This file exists because
// golang.org/x/sys/unix does not export TCFLSH for darwin, dragonfly,
// freebsd, netbsd, or openbsd - confirmed by grepping the module's
// generated zerrors_<goos>_<goarch>.go files for every arch this repo might
// target, and by cross-compiling for each GOOS, not by trusting a header
// reference. These platforms instead export TIOCFLUSH (value 0x80047410 on
// every one of them in this module), which - unlike TCFLSH's IoctlSetInt -
// takes its argument by pointer: IoctlSetPointerInt, not IoctlSetInt.
func flushPendingInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(fds, 0)
	pending := pollErr == nil && n > 0 && fds[0].Revents&unix.POLLIN != 0

	flushErr := unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, flushBSDInputQueue)

	err := flushErr
	if err == nil {
		err = pollErr
	}
	return pending, err
}
```
```go
//go:build !linux && !aix && !solaris && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package main

// flushPendingInput is a no-op on every GOOS not covered by tty_tcflsh.go or
// tty_bsd.go - Windows, Plan 9, js/wasm, and GNU/Hurd (which, unlike
// illumos, is not documented anywhere as inheriting another GOOS's build
// tags, has no zerrors_hurd_*.go file in golang.org/x/sys/unix defining
// either TCFLSH or TIOCFLUSH, and is not even a target this Go toolchain
// can cross-compile for - `GOOS=hurd go build` reports "unsupported
// GOOS/GOARCH pair" outright). Rather than pretending otherwise, this
// always reports that nothing was discarded - which is honest, since
// flushPendingInput's only caller (promptPassword) uses the returned bool
// solely to decide whether to print a warning about discarded input, and
// printing that warning here would be a lie.
//
// illumos is NOT handled here despite not having its own zerrors file in
// x/sys/unix: `go help buildconstraint` documents that GOOS=illumos matches
// every "solaris"-tagged file too, so tty_tcflsh.go's "linux || aix ||
// solaris" tag already covers it (confirmed by cross-compiling for
// GOOS=illumos) - see that file's doc comment for the full explanation.
//
// The three tty_*.go files' build tags are each other's exact negation, so
// every GOOS matches exactly one of them: a GOOS matching zero would fail to
// build (undefined: flushPendingInput), and one matching two would fail to
// build the other way (duplicate declaration).
func flushPendingInput(fd int) (bool, error) {
	return false, nil
}
```

- [ ] **Step 2: Write the tests**

```go
package main

import (
	"bufio"
	"strings"
	"testing"
)

// TestPromptLine_SharedReaderSeesBothLines exercises promptLine reading two
// sequential lines off one shared *bufio.Reader.
//
// This is NOT a mutation test for a "fresh bufio.Reader per call"
// regression, despite an earlier version of this comment claiming it was:
// bufio.NewReader(rd) special-cases an rd that is already a *bufio.Reader
// with a large enough internal buffer and returns it unchanged rather than
// wrapping it (see the "Is it already a Reader?" check in
// bufio.NewReaderSize). So even a promptLine that did
// bufio.NewReader(r).ReadString('\n') internally on every call would still
// pass this test - r would just come back as itself, unwrapped, and behave
// identically. The real fix for the original bug is the function signature:
// promptLine takes a *bufio.Reader, not an io.Reader, which forces every
// caller to construct exactly one reader for a whole prompt sequence instead
// of re-wrapping raw stdin (an io.Reader that is NOT already a *bufio.Reader,
// so the short-circuit above does not apply to it) fresh on each call - that
// mismatch is what the original bug depended on. The signature makes the bug
// impossible to reintroduce by construction; this test only pins ordinary
// two-line behavior, not that construction.
func TestPromptLine_SharedReaderSeesBothLines(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("alice\nswordfish\n"))

	first, err := promptLine(r, "Username: ")
	if err != nil {
		t.Fatalf("first promptLine: %v", err)
	}
	if first != "alice" {
		t.Errorf("first = %q, want %q", first, "alice")
	}

	second, err := promptLine(r, "Password: ")
	if err != nil {
		t.Fatalf("second promptLine: %v", err)
	}
	if second != "swordfish" {
		t.Errorf("second = %q, want %q", second, "swordfish")
	}
}

func TestPromptLine_TrimsWhitespace(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("  spaced out  \n"))
	got, err := promptLine(r, "")
	if err != nil {
		t.Fatalf("promptLine: %v", err)
	}
	if got != "spaced out" {
		t.Errorf("got %q, want %q", got, "spaced out")
	}
}

// TestPromptLine_NoTrailingNewline covers input with no final newline (e.g.
// the last line before EOF), which io.Reader.ReadString reports as an error
// (io.EOF) alongside whatever it did manage to read.
func TestPromptLine_NoTrailingNewline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("noeol"))
	got, err := promptLine(r, "")
	if err != nil {
		t.Fatalf("promptLine: %v", err)
	}
	if got != "noeol" {
		t.Errorf("got %q, want %q", got, "noeol")
	}
}

func TestPromptLine_EmptyInputErrors(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := promptLine(r, "")
	if err == nil {
		t.Fatal("expected an error reading from an empty/exhausted reader")
	}
}

// TestValidateUsername is the direct test for the guard runLogin applies
// before ever making a network call or prompting for a password - see
// validateUsername's doc comment for why it is unit tested on its own rather
// than only indirectly, by running the whole interactive login flow.
func TestValidateUsername(t *testing.T) {
	if err := validateUsername("alice"); err != nil {
		t.Errorf("validateUsername(%q) = %v, want nil", "alice", err)
	}
	if err := validateUsername(""); err == nil {
		t.Error("validateUsername(\"\") = nil, want an error rejecting the empty username")
	}
}
```

- [ ] **Step 3: Run the tests and verify the binary compiles**

Run: `go test ./cmd/whatiff-cli/ -run 'TestPromptLine|TestValidateUsername' -race -v && go build ./cmd/whatiff-cli/`
Expected: all `TestPromptLine_*` and `TestValidateUsername` tests pass; the
build still fails on `undefined: runChats`, `undefined: parseSubFlags`
resolves fine (it lives in `main.go`, already written), and
`undefined: normalizeResultsForJSON`/`sanitizeCell`/`truncationNotice` are
not referenced yet outside `chats.go`. Task 10 finishes the build.

- [ ] **Step 4: `go mod tidy` and confirm the dependency change is exactly what was intended**

Run: `go mod tidy && git diff go.mod`
Expected: `golang.org/x/sys v0.47.0` moves from the `// indirect` block to the
direct `require` block; no version changes; `go.sum` is unchanged. Then run
`make tidy` (after `git add go.mod`, since the check compares against the
last commit) and confirm it passes.

- [ ] **Step 5: Add the cross-compile check and prove it catches the platform bug**

Add `build-cli-crosscheck` to the `Makefile` (near the existing `build`
target) and wire it into `pre-commit`:

```makefile
.PHONY: build-cli-crosscheck
build-cli-crosscheck: ## Cross-compile the wi CLI for linux/darwin (build only, catches platform-specific build-tag mistakes like a file name that silently narrows its own //go:build line)
	@echo "Cross-compiling cmd/whatiff-cli for linux/amd64, darwin/amd64, darwin/arm64..."
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/whatiff-cli
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/whatiff-cli
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/whatiff-cli
	@echo "✅ wi cross-compiles for linux/darwin"
```

Add `build-cli-crosscheck` to `pre-commit`'s prerequisite list, right after
`build`. It intentionally has no `$(ENT_SENTINEL)` prerequisite —
`cmd/whatiff-cli` does not import `ent` (`go list -deps` confirms zero
`.../ent` packages in its dependency graph) — and intentionally builds to
`/dev/null` rather than a real output path, so it stays fast (a few seconds)
and leaves nothing to clean up.

Run: `make build-cli-crosscheck`
Expected: `✅ wi cross-compiles for linux/darwin`.

Then verify it actually would have caught the original bug: temporarily
replace `tty_tcflsh.go`, `tty_bsd.go`, and `tty_other.go` with a single
`//go:build unix` file using `unix.TCFLSH` (the broken shape this task
started from), run `make build-cli-crosscheck` again, and confirm it fails
with `undefined: unix.TCFLSH`. Restore the three files afterward and confirm
`git diff` (or a byte-for-byte `diff` against a backup) shows no change.

Finally, cross-compile for the full platform matrix directly (not just the
two `build-cli-crosscheck` covers) to confirm the split is complete:

```bash
for os in linux darwin freebsd openbsd netbsd dragonfly solaris illumos aix windows plan9; do
  GOOS=$os go build -o /dev/null ./cmd/whatiff-cli 2>&1 | head -3
done
GOOS=js GOARCH=wasm go build -o /dev/null ./cmd/whatiff-cli
```

`darwin`/`freebsd`/`openbsd`/`netbsd`/`dragonfly` need no `GOARCH` override
on an amd64 host; `aix` needs `GOARCH=ppc64` (its only supported arch) and
`solaris`/`illumos` need `GOARCH=amd64` explicitly, or `go build` reports an
unsupported pair. Expected: every one builds with no output.

- [ ] **Step 6: Commit**

```bash
git add cmd/whatiff-cli/login.go cmd/whatiff-cli/tty_tcflsh.go \
  cmd/whatiff-cli/tty_bsd.go cmd/whatiff-cli/tty_other.go \
  cmd/whatiff-cli/login_test.go cmd/whatiff-cli/main_test.go \
  go.mod Makefile
git commit -m "feat(cli): wi login command"
```

`go.mod` is included deliberately and only for the `golang.org/x/sys`
indirect-to-direct promotion from Step 4 above — `go.sum`, `go work`, and
`ent/` are never part of this commit. `Makefile` is included for
`build-cli-crosscheck` (Step 5), the check that exists specifically because
nothing else in the pipeline would have caught the platform split being
wrong. `chats.go` and `chats_test.go` are NOT part of this commit — they
don't exist yet; Task 10 below creates and commits them separately.

### Task 10: `wi chats`

**Files:**
- Create: `cmd/whatiff-cli/chats.go`
- Test: `cmd/whatiff-cli/chats_test.go`

#### Why `runChats` prints a truncation notice to stderr, and `--json` emits the whole `ChatPage`

`ListChats` returns `ChatPage{Results, TotalCount}` specifically so a capped
listing can be told apart from an account that only has that many chats (see
`ChatPage`'s doc comment in `internal/cli/client/chat.go`) — but a command
that decodes `page.Results` and prints only that throws the one piece of
information `ChatPage` exists to carry straight back on the floor, one layer
up from where Task 7 fixed the same failure inside the client. `runChats`
pulls the decision into `truncationNotice(page client.ChatPage) string`,
which returns `""` when nothing was cut off and otherwise a line like
`Showing 100 of 347 chats. Use --limit to see more.` — printed to **stderr**,
after the table, not stdout: it is an advisory sentence about the listing, not
part of it, and `wi chats | grep -c .` (or any other line-counting or
line-parsing consumer) must not have to know to skip a trailing line mixed
into its data. `--json` mode never reaches that print at all, since it
returns earlier — a script already has `TotalCount` in the payload to check
for itself.

`truncationNotice` is pulled out of the print loop into its own function so
this — the entire reason `ChatPage` carries `TotalCount` instead of a bare
slice — has a table-driven test pinning it (`TestTruncationNotice`), rather
than living as a couple of inline lines after the loop that a future edit
could delete without anything noticing.

#### Why `--json` normalizes a nil `Results` to an empty slice

`encoding/json` encodes a nil slice as the JSON literal `null`, not `[]`. The
server can send `"results"` as `null`, or omit the key entirely — either one
decodes into a nil `Results` (see `internal/cli/client/chat_test.go`'s
`TestListChatsMissingResultsField`/`TestListChatsNullResultsField`) — so
without normalization, `wi chats --json` on an empty account emits
`{"results":null,...}`. A script doing `jq '.results[]'` or a naive
for-range loop over the decoded field breaks on `null` in a way it would not
on `[]`, for a value that means exactly the same thing either way: no chats.
`normalizeResultsForJSON` is a separate pure function (rather than an inline
`if` before `enc.Encode`) so `TestNormalizeResultsForJSON_NilBecomesEmptySlice`
can assert on the actual encoded bytes — `"results":[]`, not merely "the
Go slice is non-nil" — which is the property that actually matters to a
downstream `jq` or JSON parser.

#### Why chat names and model names go through `sanitizeCell` before hitting the tabwriter

`Name` and `ModelName` are server-controlled, user-supplied text reaching a
terminal — the same class of problem `internal/cli/client`'s `snippet()`
(`client.go`) already solves for a raw HTTP error body. Unsanitized, an ANSI
or OSC escape sequence in a chat name could forge terminal output or rename
the tab, and an embedded tab or newline would shift every column after it in
the table. `sanitizeCell` mirrors `snippet`'s approach — collapse whitespace
runs to a single space first (via `strings.Fields`/`strings.Join`, so an
internal tab or newline doesn't just vanish and glue two words together),
then drop anything `unicode.IsPrint` rejects (which strips a lone ESC byte
while leaving the now-inert literal characters after it, e.g. `[31m`, as
plain text) — reimplemented locally rather than imported, because
`internal/cli/client` is a thin HTTP transport with no terminal-rendering
concern of its own; the doc comment on `sanitizeCell` is what keeps the two
implementations in step if one of them changes.

- [ ] **Step 1: Write the implementation**

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
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
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}

	sess, err := newSession(profileName)
	if err != nil {
		return err
	}

	page, err := sess.client.ListChats(ctx, client.ListChatsOptions{
		Archived: *archived,
		Search:   *search,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}

	if asJSON {
		// The whole ChatPage, not a bare array of results: TotalCount is the
		// entire reason ChatPage exists over []models.Chat (see its doc
		// comment in internal/cli/client/chat.go) - a script parsing a bare
		// array has no way to tell a capped listing from the complete one,
		// which is exactly the ambiguity TotalCount exists to resolve.
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(normalizeResultsForJSON(page))
	}

	if len(page.Results) == 0 {
		fmt.Println("No chats found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODEL\tLAST MESSAGE\tUNREAD")
	for _, c := range page.Results {
		last := "-"
		if c.LastMessageTime != nil {
			last = humanizeSince(*c.LastMessageTime)
		}
		unread := ""
		if c.UnreadCount > 0 {
			unread = fmt.Sprintf("%d", c.UnreadCount)
		}
		// Name and ModelName are server-controlled, user-supplied text
		// reaching a terminal - sanitizeCell strips it the same way
		// internal/cli/client's snippet() already does for error bodies
		// (see sanitizeCell's doc comment for why this doesn't just import
		// that function instead).
		name := sanitizeCell(c.Name)
		model := sanitizeCell(c.ModelName)
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, model, last, unread)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// stderr, not stdout: this is an advisory line about the listing, not
	// part of it. `wi chats | grep -c .` (or any other line-counting or
	// line-parsing consumer of the table) must not have to know to skip a
	// trailing sentence mixed into its data. --json mode never reaches this
	// line at all (it returns above) since TotalCount is already in that
	// payload for a script to check itself.
	if notice := truncationNotice(page); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
	return nil
}

// truncationNotice reports whether page's listing was capped by the caller's
// limit and, if so, the line to print about it - e.g. "Showing 100 of 347
// chats. Use --limit to see more." It returns "" when nothing was cut off.
//
// Pulled out of the print loop so the one thing TotalCount exists for (see
// ChatPage's doc comment in internal/cli/client/chat.go) has a test pinning
// it, rather than being a couple of inline lines a future edit could delete
// without anything noticing.
func truncationNotice(page client.ChatPage) string {
	if page.TotalCount <= len(page.Results) {
		return ""
	}
	return fmt.Sprintf("Showing %d of %d chats. Use --limit to see more.", len(page.Results), page.TotalCount)
}

// normalizeResultsForJSON replaces a nil page.Results with an empty, non-nil
// slice before it is handed to json.Marshal/json.Encoder.
//
// encoding/json encodes a nil slice as the JSON literal null, not []. The
// server can send "results" as null, or omit it entirely - either decodes
// into a nil Results (see internal/cli/client/chat_test.go's
// TestListChatsMissingResultsField/TestListChatsNullResultsField) - and
// without this, `wi chats --json` on an empty account emits
// {"results":null,...}. A script doing `jq '.results[]'` or a naive
// for-range loop over the decoded field breaks on null in a way it would not
// on [], which is a needless trap for a value that means exactly the same
// thing either way: no chats.
func normalizeResultsForJSON(page client.ChatPage) client.ChatPage {
	if page.Results == nil {
		page.Results = []models.Chat{}
	}
	return page
}

// sanitizeCell strips non-printable characters from s and collapses any
// whitespace runs (including a tab or newline embedded in a chat name) to a
// single space, so server-controlled text is safe to print unescaped into a
// tabwriter cell.
//
// This mirrors internal/cli/client's snippet() (client.go), which solves the
// identical problem for a raw HTTP error body reaching a terminal: without
// stripping, a chat name containing an ANSI/OSC escape sequence could forge
// terminal output or rename the tab, and an embedded tab or newline would
// shift every column after it in the table. It is reimplemented here rather
// than imported because the client package is a thin HTTP transport with no
// terminal-rendering concern of its own - that split is deliberate, not an
// oversight, so this comment is what keeps the two implementations in step
// if one of them changes.
func sanitizeCell(s string) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, collapsed)
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

- [ ] **Step 2: Write the tests**

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestTruncationNotice(t *testing.T) {
	tests := []struct {
		name       string
		results    int
		totalCount int
		want       string
	}{
		{"nothing truncated, counts equal", 3, 3, ""},
		{"total unset (zero value), fewer than results is impossible so no notice", 3, 0, ""},
		{"truncated", 100, 347, "Showing 100 of 347 chats. Use --limit to see more."},
		{"truncated by one", 9, 10, "Showing 9 of 10 chats. Use --limit to see more."},
		{"empty page, nothing to truncate", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := client.ChatPage{
				Results:    make([]models.Chat, tt.results),
				TotalCount: tt.totalCount,
			}
			got := truncationNotice(page)
			if got != tt.want {
				t.Errorf("truncationNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHumanizeSince(t *testing.T) {
	tests := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"just now, no elapsed time", 0, "just now"},
		{"just now, well under a minute", 10 * time.Second, "just now"},
		{"a minute and a half rounds down to 1m", 90 * time.Second, "1m ago"},
		{"comfortably minutes", 45 * time.Minute, "45m ago"},
		{"an hour and a half rounds down to 1h", 90 * time.Minute, "1h ago"},
		{"comfortably hours", 5 * time.Hour, "5h ago"},
		{"a day and an hour rounds down to 1d", 25 * time.Hour, "1d ago"},
		{"comfortably days", 3 * 24 * time.Hour, "3d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := humanizeSince(time.Now().Add(-tt.ago))
			if got != tt.want {
				t.Errorf("humanizeSince(now-%v) = %q, want %q", tt.ago, got, tt.want)
			}
		})
	}
}

// TestNormalizeResultsForJSON_NilBecomesEmptySlice is the regression test
// for a nil page.Results (the server sent "results":null, or omitted the
// field - see internal/cli/client/chat_test.go's
// TestListChatsMissingResultsField/TestListChatsNullResultsField) still
// encoding as [] rather than null.
func TestNormalizeResultsForJSON_NilBecomesEmptySlice(t *testing.T) {
	page := normalizeResultsForJSON(client.ChatPage{TotalCount: 0})
	if page.Results == nil {
		t.Fatal("Results is still nil, want a non-nil empty slice")
	}
	if len(page.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(page.Results))
	}

	data, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(data), `"results":null`) {
		t.Errorf("encoded page still has null results: %s", data)
	}
	if !strings.Contains(string(data), `"results":[]`) {
		t.Errorf("encoded page does not have an empty-array results: %s", data)
	}
}

// TestNormalizeResultsForJSON_LeavesNonNilResultsAlone guards against an
// overzealous fix that replaces every Results slice rather than only a nil
// one - a real chat list must survive unchanged.
func TestNormalizeResultsForJSON_LeavesNonNilResultsAlone(t *testing.T) {
	want := []models.Chat{{Name: "deploy plan"}}
	page := normalizeResultsForJSON(client.ChatPage{Results: want, TotalCount: 1})
	if len(page.Results) != 1 || page.Results[0].Name != "deploy plan" {
		t.Errorf("Results = %+v, want unchanged %+v", page.Results, want)
	}
}

func TestSanitizeCell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "deploy plan", "deploy plan"},
		{"embedded tab collapsed to a single space", "deploy\tplan", "deploy plan"},
		{"embedded newline collapsed to a single space", "deploy\nplan", "deploy plan"},
		{"ANSI escape byte stripped, literal text survives", "\x1b[31mdanger\x1b[0m", "[31mdanger[0m"},
		{"leading and trailing whitespace trimmed", "  spaced  ", "spaced"},
		{"empty string stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCell(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeCell(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Verify the binary builds and every test passes**

Run: `go test ./cmd/whatiff-cli/ ./internal/cli/... -race && go vet ./cmd/whatiff-cli/ ./internal/cli/... && go build ./...`
Expected: all tests pass, `go vet` and `go build` produce no output. The
binary now compiles end to end.

- [ ] **Step 4: Verify the cross-compile matrix, then manually verify the binary's behavior**

Run `make build-cli-crosscheck` (see Task 9 step 5) once more here, now that
`chats.go` completes the build — this is the point at which the whole
binary, not just the platform-specific tty files in isolation, has to
cross-compile clean.

Exit codes, stdout/stderr routing, and the not-logged-in / unknown-profile /
non-TTY-password / pasted-password error paths only show up by actually
running the binary — no server needs to be running for any of these (Task 11
does the live end-to-end check against one).

```bash
go build -o bin/wi ./cmd/whatiff-cli
./bin/wi                       # usage on stderr, exit 2
./bin/wi --help                 # usage on stdout, exit 0
./bin/wi bogus                   # unknown command error, exit 1
./bin/wi chats                    # not-logged-in error naming the profile and URL, exit 1
./bin/wi --profile nope chats      # unknown-profile error listing available profiles
                                     # and the config file path, exit 1
./bin/wi chats --help                # chats' own usage on stdout, exit 0 (matches
                                       # the top-level --help convention)
./bin/wi chats --bogus                 # chats' own parse error + usage, printed once,
                                         # to stderr, exit 2 (matches the top-level
                                         # convention, not run()'s generic exit 1)
```

`wi chats | grep -c .` against a real listing (Task 11's end-to-end check, or
a local mock server) should count only the header and data rows — the
truncation notice on stderr must not appear in that count.

The pasted-password defense needs a pty to reproduce, since the exploit
depends on kernel tty echo behavior a piped `echo "x" | wi login` cannot
trigger (piped stdin is not a terminal at all, so `promptPassword` refuses it
outright — see Task 9's TTY-required check). Script one (Python's `pty`
module works well): open a pty, run `wi login` with all three of stdin/
stdout/stderr attached to the slave side, wait for the username prompt, then
write `b"alice
hunter2
"` as a *single* `os.write()` call to the master side
to simulate a paste. Confirm:

- `alice` and `hunter2` both appear in the echoed output (the echo itself is
  not preventable — see this task's `#### Why` section above).
- The stderr warning appears exactly once.
- After that write, the process **blocks** without producing further output
  until a *new* write supplies the real password — proving the stale
  `hunter2` line was discarded rather than silently accepted as the answer to
  the password prompt.

Then repeat with the username and password written as two *separate*,
naturally-spaced writes (not a paste) and confirm the warning does **not**
appear — a normal login must stay silent.

- [ ] **Step 5: Commit**

```bash
git add cmd/whatiff-cli/chats.go cmd/whatiff-cli/chats_test.go
git commit -m "feat(cli): wi chats command"
```

Only `chats.go` and its test are new here — `login.go`, the three `tty_*.go`
files, `go.mod`, and `Makefile` were already committed at the end of Task 9
(its own Step 6). `.gitignore` is not part of this commit either: nothing in
Task 9 or Task 10 edits it.

### Task 11: Makefile targets, package docs, and an end-to-end check

**Status: DONE**, with one genuine bug found by Step 5 and left unfixed —
see that step below. Actual commit: `chore(cli): build targets and package
documentation` (`make pre-commit` needed no follow-up fix; it passed clean on
the first run).

**Files:**
- Modified: `Makefile`, `docs/ARCHITECTURE_SUMMARY.md`
- Created: `internal/cli/config/_PACKAGE_SUMMARY.md`,
  `internal/cli/client/_PACKAGE_SUMMARY.md`,
  `cmd/whatiff-cli/_PACKAGE_SUMMARY.md` (a third summary beyond this plan's
  original scope — the architecture summary's own rule requires one for every
  small `main` package too, and `cmd/whatiff-cli` qualifies).

- [x] **Step 1: Add the Makefile targets**

Added, immediately after `build-cli-crosscheck`. Matches the plan's shape
with one addition: each target prints a `✅ ...` confirmation line, matching
every other target in this Makefile (`build-cli-crosscheck` included) — the
plan's snippet omitted that only for brevity.

```makefile
build-cli: ## Build the wi CLI for this machine into ./bin/wi
	@mkdir -p bin
	@go build -o bin/wi ./cmd/whatiff-cli
	@echo "✅ built ./bin/wi"

install-cli: ## Install the wi CLI via 'go install' ($(GOPATH)/bin or $(GOBIN))
	@go install ./cmd/whatiff-cli
	@echo "✅ installed as 'whatiff-cli' (go install names the binary after its directory, cmd/whatiff-cli — symlink it to 'wi' on your PATH, e.g.: ln -s \$$(go env GOPATH)/bin/whatiff-cli \$$(go env GOPATH)/bin/wi)"
```

The `go install`-names-the-binary-after-the-directory note lives inline in
`install-cli`'s own help/echo text rather than a separate README note, so a
user hits it exactly when it's relevant instead of having to have read the
README first. `docs/ARCHITECTURE_SUMMARY.md`'s new CLI subsection (Step 3
below) repeats the same one-liner for anyone reading architecture docs
top-down instead.

- [x] **Step 2: Verify the targets work**

`make build-cli && ./bin/wi` (no args) printed the usage text listing `login`
and `chats`, exit code 2, as expected. `make install-cli` also verified
separately (installs as `whatiff-cli` under `$(go env GOPATH)/bin`).

- [x] **Step 3: Write the package summaries**

The three drafts originally inlined in this plan step were written **before**
`paths.go`, `auth.go`, `chat.go`, the corrupt-credentials-file recovery, the
single-flight refresh, `ChatPage`, and the terminal-sanitization work existed
— they undersold what actually shipped and were **not** used verbatim. The
real, current summaries live at:

- `internal/cli/config/_PACKAGE_SUMMARY.md`
- `internal/cli/client/_PACKAGE_SUMMARY.md`
- `cmd/whatiff-cli/_PACKAGE_SUMMARY.md`

All three follow the repo's seven-heading template (Role, Responsibilities,
Key types and entry points, Dependencies, Non-obvious decisions, Testing,
Related) and document the hard-won decisions from milestone 1's review cycle
— atomic-save internals, the single-flight refresh's panic-safe cleanup, the
`tty_linux.go` implicit-build-constraint trap, and more. Read those files
directly rather than this plan for current content; this plan step is a
historical record of intent, not a mirror of the shipped docs.

`docs/ARCHITECTURE_SUMMARY.md` also picked up a short new "CLI (`wi`)"
subsection under **High-Level System Architecture**, alongside Frontend/
Backend, per that doc's own anti-drift rule (a new `internal/cli/` subtree
and a new binary is a system-boundary change). It notes the CLI as a second,
pure-consumer client that adds no server surface and doesn't touch
`openapi.yaml`.

- [x] **Step 4: Run the full repo gate**

`make pre-commit` (`fmt vet tidy test build build-cli-crosscheck
check-no-local-models check-compose-defaults check-public-hygiene`) passed
clean on the first run — no fix commit was needed for this task.

- [x] **Step 5: End-to-end check against a real server**

Ran for real: `cp .env.example .env` with generated secrets, `AUTO_MIGRATE=true`
for the first boot, `make check-env` → `make db-up` → `make run-mock`
(backgrounded), registered a user via `POST /api/user/register`, then drove
`./bin/wi login` through a pty (a scripted terminal, not piped stdin — the
password prompt requires a real TTY) with `XDG_CONFIG_HOME` pointed at a
scratch directory so the real user's `~/.config` was never touched.

Results:
- `wi login` printed `Logged in as <username>`, exit 0.
- `stat -c '%a' $XDG_CONFIG_HOME/whatiff/credentials.json` → `600`, exactly
  as required.
- `wi chats` on the fresh account printed `No chats found.`
- Created a chat via `POST /api/chat`; `wi chats` then rendered a one-row
  table (`NAME MODEL LAST MESSAGE UNREAD`).
- `wi --json chats` (note, historical: at the time of this run, `--json` was
  a top-level-only flag per `wi`'s own usage text — `wi [flags] <command>` —
  so it had to precede the subcommand; `wi chats --json` errored with `flag
  provided but not defined: -json`. This was flagged in that review as
  "expected/documented behavior, not a bug," but the design spec documents
  `wi chats --json | jq ...` as the intended usage, so a later whole-milestone
  review treated the mismatch as a real gap and fixed it — `--profile`/
  `--json` are now registered on each subcommand's own FlagSet too, so both
  orderings work. See "Status: executed" at the top of this document.)
  printed valid JSON with both `results` and `total_count`.
- A second chat plus `--limit 1` correctly printed a one-row table and the
  `Showing 1 of 2 chats. Use --limit to see more.` notice on stderr.
- `--archived` on an account with no archived chats correctly printed `No
  chats found.`
- A wrong-password login attempt failed cleanly (`login failed: POST
  /user/login: Invalid credentials (HTTP 401)`, exit 1) and — importantly —
  left the previously-stored valid credentials untouched (`wi chats` still
  worked immediately afterward with no re-login).
- Calling `wi chats` before ever logging in, and with an unknown `--profile`,
  both produced clear, actionable error messages naming the fix.

**Genuine bug found — not fixed here (out of scope for this CLI-only task,
and outside `internal/cli/*`/`cmd/whatiff-cli` entirely):**
`wi chats --search <term>` fails every time against a chat with no tags, via
`GET /chat?search=...` → HTTP 500 `Failed to list chats`. Server log:
`pq: cannot extract elements from a scalar`. Root cause, traced via `psql`
against the live `chats` table: Ent's `field.Strings("tags").Optional()`
(`ent/schema/chat.go`) stores an empty/never-set tags value as the **JSON
literal `null`** in the `jsonb` column — confirmed with `jsonb_typeof(tags)`
returning `'null'`, while `tags IS NULL` (SQL NULL) is `false`. The search
path in `internal/datastore/chat.go`'s `ListChats` builds
`jsonb_array_elements_text(COALESCE(tags::jsonb, '[]'::jsonb))` to search
tags; `COALESCE` only substitutes on **SQL** NULL, so a JSON-null tags value
passes through unchanged into `jsonb_array_elements_text`, which errors on
any non-array scalar (including JSON null). Since most chats never have tags
set, this means **`GET /chat?search=...` — an ordinary, everyday call — fails
100% of the time** for a typical account. `--archived`, `--limit`, and no
filter at all all avoid this code path and work correctly; only `--search`
(and any other future caller of `filters.Query`) hits it. This was invisible
to every prior test in this milestone because none of them ran against real
Postgres jsonb semantics — an `httptest` double has no `jsonb_array_elements_text`
to get wrong. Flagging for a separate fix in `internal/datastore/chat.go`
(the fix is almost certainly `COALESCE(NULLIF(tags::jsonb, 'null'::jsonb),
'[]'::jsonb)` or equivalent, not a CLI change).

- [x] **Step 6: Commit**

```bash
git add Makefile docs/ARCHITECTURE_SUMMARY.md cmd/whatiff-cli/_PACKAGE_SUMMARY.md internal/cli/client/_PACKAGE_SUMMARY.md internal/cli/config/_PACKAGE_SUMMARY.md
git commit -m "chore(cli): build targets and package documentation"
```

---

## Milestone 1 done when

- `make pre-commit` passes. **Done.**
- `wi login` against a `make run-mock` stack stores a `0600` credential file.
  **Done — verified live, see Step 5.**
- `wi chats` and `wi chats --json` both list chats, in either flag order
  (`--profile`/`--json` were later registered on each subcommand's own
  FlagSet, not just main's top-level one — see Step 5's original note on
  flag placement for the limitation this fixed). **Done — verified live.**
- Both new packages have a `_PACKAGE_SUMMARY.md`. **Done — plus a third for
  `cmd/whatiff-cli`.**
- `wi chats --search` is shipped but fails 100% of the time against the
  real server today — a server-side bug, not a CLI defect, filed separately
  as issue #8. The flag itself, its client-side query-param plumbing, and
  its CLI-level test coverage are all correct; the fix belongs in
  `internal/handlers/chat`, out of scope for this plan.

## What this milestone deliberately does not do

- No conversation handling — that is the `engine` package in milestone 2.
- No TUI, no Bubble Tea dependency yet. Milestone 1 uses three dependencies —
  `github.com/BurntSushi/toml`, `golang.org/x/term`, and `golang.org/x/sys`
  (the last promoted from indirect to direct by Task 9's terminal-flush
  ioctl) — all already present in `go.sum` before this milestone started, so
  the only `go.mod` change is that promotion, not a wholly new module. Keeping
  the dependency change out of the foundation makes the eventual Bubble Tea
  commit a small, reviewable diff on its own.
