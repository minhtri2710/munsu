package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TempHome creates a fully initialized temporary MUNSU_HOME directory structure.
func TempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "state"),
		filepath.Join(dir, "data"),
		filepath.Join(dir, "config"),
		filepath.Join(dir, "projects"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("failed to create temp home dir %s: %v", d, err)
		}
	}
	return dir
}

// ClearEnv clears munsu-related environment variables for test isolation and restores them on cleanup.
func ClearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"MUNSU_HOME",
		"MUNSU_ROLE",
		"HERDR_ENV",
	}
	for _, key := range envVars {
		if val, set := os.LookupEnv(key); set {
			t.Setenv(key, "")
			_ = val
		}
	}
}

// PathInMessage reports whether message names path.
//
// The verbatim path, its %q rendering, and its JSON-escaped rendering are
// accepted. The path must still appear in full, so this answers the same
// question as a raw substring search rather than a weaker one.
func PathInMessage(message, path string) bool {
	if path == "" {
		return false
	}
	if strings.Contains(message, path) {
		return true
	}
	quoted := strconv.Quote(path)
	if strings.Contains(message, quoted[1:len(quoted)-1]) {
		return true
	}
	jsonPath, err := json.Marshal(path)
	if err != nil {
		return false
	}
	return strings.Contains(message, string(jsonPath[1:len(jsonPath)-1]))
}

// SetUserHome points os.UserHomeDir at dir for the duration of the test.
//
// The postcondition is checked rather than assumed, so a platform that reads
// some other variable fails here instead of surfacing as an unrelated path
// mismatch somewhere downstream.
func SetUserHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	got, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir after setting HOME=%s: %v", dir, err)
	}
	if got != dir {
		t.Fatalf("os.UserHomeDir = %q after setting HOME=%q; this platform reads some other variable", got, dir)
	}
}
