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
