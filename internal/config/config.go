package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KnownKeys is the authoritative list of well-known config keys.
// Used by both config show and config get to distinguish known-unset
// from unknown keys.
var KnownKeys = []string{
	"backend",
	"soldier-harness",
	"captain-harness",
	"model",
	"backlog-backend",
	"default-mode",
	"require-no-mistakes",
	"afk-digest-window",
	"afk-wedge-stale-beat",
	"afk-wedge-max-repeat",
	"afk-max-defer",
}

// IsKnownKey returns true if key is a well-known config key.
func IsKnownKey(key string) bool {
	for _, k := range KnownKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ConfigDir returns the path to the config directory under homeDir.
func ConfigDir(homeDir string) string {
	return filepath.Join(homeDir, "config")
}

// Get reads a config value. Resolution:
//  1. MUNSU_<KEY>_OVERRIDE env var
//  2. File at $MUNSU_HOME/config/<key>
func Get(homeDir, key string) (string, error) {
	// Check override env var
	envKey := fmt.Sprintf("MUNSU_%s_OVERRIDE", strings.ToUpper(key))
	if val, ok := os.LookupEnv(envKey); ok {
		return val, nil
	}

	// Read from file
	p := filepath.Join(ConfigDir(homeDir), key)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("config key %q not found", key)
		}
		return "", fmt.Errorf("reading config file %s: %w", p, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Set writes a config value to $MUNSU_HOME/config/<key>.
func Set(homeDir, key, value string) error {
	p := filepath.Join(ConfigDir(homeDir), key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(value+"\n"), 0644); err != nil {
		return fmt.Errorf("writing config file %s: %w", p, err)
	}
	return nil
}
