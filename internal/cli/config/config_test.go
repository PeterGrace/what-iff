package config

import (
	"os"
	"path/filepath"
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
