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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
// The empty-api_url convenience default applies ONLY to the builtin local
// profile name. Any other profile with a missing or malformed api_url is
// rejected here rather than silently resolving to localhost: once a profile
// is misconfigured (e.g. a `apiurl` typo that leaves `api_url` unset),
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

	if p, ok := c.Profiles[name]; ok {
		if p.APIURL == "" {
			if name != DefaultProfileName {
				return "", Profile{}, fmt.Errorf("profile %q has no api_url", name)
			}
			p.APIURL = DefaultAPIURL
		}
		if err := validateAPIURL(name, p.APIURL); err != nil {
			return "", Profile{}, err
		}
		return name, p, nil
	}
	if len(c.Profiles) == 0 && name == DefaultProfileName {
		return name, Profile{APIURL: DefaultAPIURL}, nil
	}
	if asked == "" && c.DefaultProfile == "" {
		return "", Profile{}, fmt.Errorf("no default_profile set and no profile named %q (available: %s)", name, c.profileNames())
	}
	return "", Profile{}, fmt.Errorf("no profile named %q (available: %s)", name, c.profileNames())
}

// validateAPIURL rejects a profile URL that net/http could not use, at the one
// chokepoint where the profile name is still known. Without this the failure
// surfaces later as an opaque transport error naming no profile.
func validateAPIURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("profile %q has an invalid api_url %q: %w", name, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("profile %q api_url %q must start with http:// or https://", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("profile %q api_url %q has no host", name, raw)
	}
	return nil
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
