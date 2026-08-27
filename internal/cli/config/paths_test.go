package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathEndsWithWhatiffConfigToml(t *testing.T) {
	// WHATIFF_CONFIG_DIR, not XDG_CONFIG_HOME: os.UserConfigDir is not
	// XDG-compliant on every platform (darwin and windows ignore
	// XDG_CONFIG_HOME entirely — see Dir's doc comment), so pinning
	// XDG_CONFIG_HOME here would leave this test non-hermetic on those
	// platforms. WHATIFF_CONFIG_DIR is honoured identically everywhere. The
	// override points at a directory named "whatiff" so the suffix assertion
	// below still exercises the same production path shape
	// (base/whatiff/config.toml) that a real deployment produces.
	t.Setenv("WHATIFF_CONFIG_DIR", filepath.Join(t.TempDir(), "whatiff"))
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath returned %v", err)
	}
	want := filepath.Join("whatiff", "config.toml")
	if !strings.HasSuffix(path, want) {
		t.Errorf("DefaultPath() = %q, want suffix %q", path, want)
	}
}

// TestDirHonoursOverride pins the override itself: when WHATIFF_CONFIG_DIR is
// set, Dir() must return exactly that value, with no further join against
// "whatiff" or anything else — the caller who set the override chose the
// directory on purpose (a test's t.TempDir(), or the cli-e2e target's scratch
// dir) and does not want a surprise subdirectory appended to it. This holds
// on every platform, unlike the os.UserConfigDir fallback path below it.
func TestDirHonoursOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "anything-the-caller-picked")
	t.Setenv("WHATIFF_CONFIG_DIR", want)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir returned %v", err)
	}
	if got != want {
		t.Errorf("Dir() = %q, want exactly %q", got, want)
	}
}
