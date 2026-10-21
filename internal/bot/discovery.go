package bot

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func findCLI(configured, name string) (string, error) {
	home, _ := os.UserHomeDir()
	return executable(configured, name, installLocations(runtime.GOOS, home, name)...)
}

// An explicit host override must not be replaced by a different installation.
func executable(configured, name string, locations ...string) (string, error) {
	if configured != "" {
		return exec.LookPath(configured)
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return path, nil
	}
	for _, location := range locations {
		if path, candidateErr := exec.LookPath(location); candidateErr == nil {
			return path, nil
		}
	}
	return "", err
}

func installLocations(goos, home, name string) []string {
	var locations []string
	if home != "" {
		locations = append(locations, filepath.Join(home, ".local", "bin", name), filepath.Join(home, ".npm-global", "bin", name))
	}
	locations = append(locations, filepath.Join("/usr/local/bin", name), filepath.Join("/opt/homebrew/bin", name))
	if goos == "darwin" && name == "codex" {
		bundle := filepath.Join("Codex.app", "Contents", "Resources", "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex")
		locations = append(locations, filepath.Join("/Applications", bundle))
		if home != "" {
			locations = append(locations, filepath.Join(home, "Applications", bundle))
		}
	}
	return locations
}
