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
//
// MUNSU_ROOT_OVERRIDE is intentionally not supported; use MUNSU_HOME instead.
// If MUNSU_ROOT_OVERRIDE is set, it is ignored and a notice is printed to stderr.
func Resolve(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if env := os.Getenv("MUNSU_HOME"); env != "" {
		return filepath.Abs(env)
	}
	if env := os.Getenv("MUNSU_ROOT_OVERRIDE"); env != "" {
		fmt.Fprintf(os.Stderr, "warning: MUNSU_ROOT_OVERRIDE is deprecated; use MUNSU_HOME instead (ignoring %s)\n", env)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(homeDir, DefaultDirName), nil
}

// EnsureDirTree creates the home directory and subdirectory structure.
func EnsureDirTree(path string) error {
	for _, d := range append([]string{""}, HomeDirNames...) {
		dir := filepath.Join(path, d)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("cannot secure directory %s: %w", dir, err)
		}
	}
	return nil
}
