// Package config loads the WhatIff CLI's TOML configuration and stores
// per-profile credentials.
//
// A profile is a named server the CLI can talk to, so one binary can address a
// local development stack and a hosted instance without re-authenticating each
// time it switches.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
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
