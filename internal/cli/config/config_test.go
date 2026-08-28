package config

import (
	"os"
	"path/filepath"
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
