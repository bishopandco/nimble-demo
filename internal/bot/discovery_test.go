package bot

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDiscoveryWithoutCLIOnPath(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "installed codex")
	if err := os.Symlink(path, candidate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	found, err := executable("", "codex", candidate)
	if err != nil || found != candidate {
		t.Fatalf("install location not found: %q, %v", found, err)
	}
	if _, err := executable(filepath.Join(t.TempDir(), "missing override"), "codex", candidate); err == nil {
		t.Fatal("invalid explicit path was ignored")
	}
}

func TestMacCodexAppLocations(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	bundle := filepath.Join("Codex.app", "Contents", "Resources", "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex")
	locations := installLocations("darwin", home, "codex")
	for _, expected := range []string{filepath.Join("/Applications", bundle), filepath.Join(home, "Applications", bundle)} {
		if !slices.Contains(locations, expected) {
			t.Fatalf("missing app location: %q", expected)
		}
	}
}
