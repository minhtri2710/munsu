//go:build !windows

package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRefusesWeakenedDurableMechanicsDirectories(t *testing.T) {
	for _, name := range []string{JournalDirName, LockDirName} {
		t.Run(name, func(t *testing.T) {
			h := newTestHome(t)
			path := filepath.Join(h.Root(), name)
			if err := os.Chmod(path, 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(h.Root()); err == nil {
				t.Fatalf("Open accepted weakened %s directory", name)
			}
		})
	}
}
