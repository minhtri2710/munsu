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
	"parent-home",
	"soldier-harness",
	"captain-harness",
	"model",
	"model-allowlist",
	"default-mode",
	"wake-delivery-mode",
	"require-no-mistakes",
	"allow-direct-pr-fallback",
	"afk-digest-window",
	"afk-wedge-stale-beat",
	"afk-wedge-max-repeat",
	"afk-max-defer",
	"install-root",
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

// Get reads a config value from the flat config file at
// $MUNSU_HOME/config/<key>. Core Config never reads the process environment.
func Get(homeDir, key string) (string, error) {
	if !IsKnownKey(key) {
		return "", fmt.Errorf("unknown config key %q", key)
	}
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
	if !IsKnownKey(key) {
		return fmt.Errorf("unknown config key %q", key)
	}
	p := filepath.Join(ConfigDir(homeDir), key)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("securing config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("creating config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing config temp file: %w", err)
	}
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing config temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing config temp file: %w", err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return fmt.Errorf("installing config file %s: %w", p, err)
	}
	return nil
}
