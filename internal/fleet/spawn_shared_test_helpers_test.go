package fleet

import (
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

type fakeBackend struct {
	newWindow func(session, name string) (string, error)
	sendKeys  func(windowID, text string) error
	capture   func(windowID string, lines int) (string, error)
	alive     func(windowID string) bool
	teardown  func(windowID string) error
}

func (f *fakeBackend) NewWindow(session, name string) (string, error) {
	if f.newWindow != nil {
		return f.newWindow(session, name)
	}
	return "win-1", nil
}

func (f *fakeBackend) SendKeys(windowID, text string) error {
	if f.sendKeys != nil {
		return f.sendKeys(windowID, text)
	}
	return nil
}

func (f *fakeBackend) Capture(windowID string, lines int) (string, error) {
	if f.capture != nil {
		return f.capture(windowID, lines)
	}
	return "> ready", nil
}

func (f *fakeBackend) CheckAlive(windowID string) (bool, error) {
	if f.alive != nil {
		return f.alive(windowID), nil
	}
	return true, nil
}

func (f *fakeBackend) Teardown(windowID string) error {
	if f.teardown != nil {
		return f.teardown(windowID)
	}
	return nil
}

// createFakeNoMistakesVersion creates a fake no-mistakes binary that reports
// the given semver version string.
func createFakeNoMistakesVersion(t *testing.T, version string) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\necho \"no-mistakes version v" + version + " (test)\"\nexit 0\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	return tmpDir
}
