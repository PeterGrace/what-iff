package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir is the CLI's configuration directory: WHATIFF_CONFIG_DIR when set,
// otherwise the OS user config directory (os.UserConfigDir) plus a whatiff/
// component.
//
// WHATIFF_CONFIG_DIR exists because os.UserConfigDir is NOT XDG-compliant
// across platforms: on Linux it honours XDG_CONFIG_HOME, but on darwin it
// always returns $HOME/Library/Application Support and on windows it always
// returns %AppData%, ignoring XDG_CONFIG_HOME entirely on both (see
// $(go env GOROOT)/src/os/file.go). Setting XDG_CONFIG_HOME to redirect a
// test or a scratch run therefore does nothing on a Mac or Windows machine —
// it silently falls through to the real per-OS default, which for this
// package means a hermetic test would read and overwrite the developer's
// actual config.toml and credentials.json. WHATIFF_CONFIG_DIR is a single,
// portable override that every platform honours identically, needed both for
// hermetic tests here and in cmd/whatiff-cli, and for the `make cli-e2e`
// target planned for a later milestone.
func Dir() (string, error) {
	if dir := os.Getenv("WHATIFF_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
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
