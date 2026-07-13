package home

import (
	"fmt"
	"os"
	"path/filepath"
)

const DefaultDirName = ".munsu"

// HomeDirNames are the subdirectories created under the home directory.
var HomeDirNames = []string{"state", "data", "config", "projects"}

// Resolve determines the munsu home directory using the following precedence:
// 1. The override path (typically from --home flag)
// 2. MUNSU_HOME environment variable
// 3. Default ~/.munsu
func Resolve(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if env := os.Getenv("MUNSU_HOME"); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, DefaultDirName), nil
}

// EnsureDirTree creates the home directory and subdirectory structure.
// Returns the path to the home directory.
func EnsureDirTree(path string) error {
	for _, d := range append([]string{""}, HomeDirNames...) {
		dir := filepath.Join(path, d)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
	}
	return nil
}
