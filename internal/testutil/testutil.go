package testutil

import (
	"os"
	"path/filepath"
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
