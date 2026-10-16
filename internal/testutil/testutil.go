package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// IsolatedHomeEnv holds the temp home IsolateUserHome made; a test binary
// re-executed as a child inherits it and keeps its parent's home.
const IsolatedHomeEnv = "MUNSU_TEST_ISOLATED_HOME"

// IsolateUserHome points HOME at a fresh temp dir for a package's test run, so
// no test reads or writes the Human's home. It first pins the go toolchain's
// cache, module and env-file paths, so go children keep the Human's caches,
// and unsets the variables that outrank HOME in harness, gh and XDG lookup.
// The returned func removes the temp home.
func IsolateUserHome() (func(), error) {
	if os.Getenv(IsolatedHomeEnv) != "" {
		return func() {}, nil
	}
	if _, err := exec.LookPath("go"); err == nil {
		keys := []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}
		out, err := exec.Command("go", append([]string{"env"}, keys...)...).Output()
		if err != nil {
			return nil, fmt.Errorf("go env: %w", err)
		}
		for i, v := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
			if i < len(keys) && v != "" {
				os.Setenv(keys[i], v)
			}
		}
	}
	dir, err := os.MkdirTemp("", "munsu-test-home-")
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"PI_CODING_AGENT_DIR", "CLAUDE_CONFIG_DIR", "GH_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		os.Unsetenv(k)
	}
	os.Setenv("HOME", dir)
	os.Setenv(IsolatedHomeEnv, dir)
	return func() { os.RemoveAll(dir) }, nil
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
