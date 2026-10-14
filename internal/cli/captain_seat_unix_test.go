//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// `munsu captain seat -- argv` runs the captain from the home and exits with
// the captain's code.
func TestCaptainSeatCommandExitsWithTheCaptainsCode(t *testing.T) {
	dir := t.TempDir()
	if _, err := home.Init(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", dir)
	got := -1
	oldExit := exitWithCode
	exitWithCode = func(code int) { got = code }
	t.Cleanup(func() { exitWithCode = oldExit })
	root := NewRootCommand()
	root.SetArgs([]string{"captain", "seat", "--", "/bin/sh", "-c", "exit 4"})
	if err := root.Execute(); err != nil || got != 4 {
		t.Fatalf("captain seat = %d, %v; want the captain's exit 4", got, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "state", ".captain-seat.sock")); !os.IsNotExist(err) {
		t.Fatalf("seat socket left behind: %v", err)
	}
}
