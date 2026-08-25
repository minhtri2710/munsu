package testutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		"MUNSU_BACKEND_OVERRIDE",
		"MUNSU_SOLDIER_HARNESS_OVERRIDE",
		"MUNSU_CAPTAIN_HARNESS_OVERRIDE",
		"HERDR_ENV",
	}
	for _, key := range envVars {
		if val, set := os.LookupEnv(key); set {
			t.Setenv(key, "")
			_ = val
		}
	}
}

// FakeSessionBackend is an in-memory session backend adapter for fast unit testing.
type FakeSessionBackend struct {
	mu      sync.Mutex
	Windows map[string]*FakeWindow
	NextID  int
}

type FakeWindow struct {
	ID       string
	Name     string
	Session  string
	Keys     []string
	Captured string
	IsAlive  bool
}

func NewFakeSessionBackend() *FakeSessionBackend {
	return &FakeSessionBackend{
		Windows: make(map[string]*FakeWindow),
		NextID:  1,
	}
}

func (b *FakeSessionBackend) NewWindow(session, name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	winID := "@fake_" + name
	b.Windows[winID] = &FakeWindow{
		ID:      winID,
		Name:    name,
		Session: session,
		IsAlive: true,
	}
	return winID, nil
}

func (b *FakeSessionBackend) SendKeys(windowID, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	win, ok := b.Windows[windowID]
	if !ok {
		win = &FakeWindow{ID: windowID, IsAlive: true}
		b.Windows[windowID] = win
	}
	win.Keys = append(win.Keys, text)
	return nil
}

func (b *FakeSessionBackend) Capture(windowID string, lines int) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if win, ok := b.Windows[windowID]; ok {
		return win.Captured, nil
	}
	return "", nil
}

func (b *FakeSessionBackend) CheckAlive(windowID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if win, ok := b.Windows[windowID]; ok {
		return win.IsAlive, nil
	}
	return false, nil
}

func (b *FakeSessionBackend) Teardown(windowID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if win, ok := b.Windows[windowID]; ok {
		win.IsAlive = false
	}
	return nil
}

// PathInMessage reports whether message names path.
//
// A message that renders a path with %q, or carries one inside a JSON
// document, escapes every separator in it: a Windows home written as
// C:\Users\x appears in the text as C:\\Users\\x, and a substring search for
// the path as spelled finds nothing. On Unix there is no separator to escape,
// so the two renderings are the same string and searching for the raw path
// worked by coincidence.
//
// Exactly those two renderings are accepted. The path must still appear in
// full, so this answers the same question as a raw substring search rather
// than a weaker one.
func PathInMessage(message, path string) bool {
	if path == "" {
		return false
	}
	if strings.Contains(message, path) {
		return true
	}
	quoted := strconv.Quote(path)
	return strings.Contains(message, quoted[1:len(quoted)-1])
}

// SetUserHome points os.UserHomeDir at dir for the duration of the test.
//
// os.UserHomeDir reads $HOME on Unix and %USERPROFILE% on windows, so a
// fixture that sets HOME directly moves nothing on windows: the product goes
// on resolving the real user profile and the test compares it against a temp
// directory it thought it had installed.
//
// The postcondition is checked rather than assumed, so a platform whose rule
// differs from either of these fails here, naming the variable, instead of
// surfacing as an unrelated path mismatch somewhere downstream.
func SetUserHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(userHomeEnv, dir)
	got, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir after setting %s=%s: %v", userHomeEnv, dir, err)
	}
	if got != dir {
		t.Fatalf("os.UserHomeDir = %q after setting %s=%q; this platform reads some other variable", got, userHomeEnv, dir)
	}
}
