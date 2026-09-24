package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestBootstrapCmdRefusesUnprobeableSessionLock pins that `munsu bootstrap`
// surfaces a session lock probe it cannot answer instead of reading it as
// "no session holds the lock" and running mutating setup sweeps. The session
// lock path is a directory, so opening it fails for every user.
func TestBootstrapCmdRefusesUnprobeableSessionLock(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(home.SessionLockPath(homeDir), 0755); err != nil {
		t.Fatal(err)
	}
	previous := homeOverride
	homeOverride = homeDir
	t.Cleanup(func() { homeOverride = previous })

	cmd := newBootstrapCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "session lock probe") {
		t.Fatalf("bootstrap error = %v, want the session lock probe failure", err)
	}
}
