package home

import (
	"fmt"
	"os"
	"path/filepath"
)

const DefaultDirName = ".munsu"

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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(homeDir, DefaultDirName), nil
}
