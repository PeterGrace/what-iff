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
