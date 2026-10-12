//go:build !windows

package orchestrator

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A soldier can put a symlink, a FIFO or a directory at its status file. The
// forced-retirement evidence copy copies nothing through any of them, without
// blocking, and copies the nearest valid case, a regular file.
func TestForcedRetirementEvidenceCopiesOnlyARegularStatus(t *testing.T) {
	for _, tt := range []struct {
		name  string
		plant func(path, other string) error
		copy  bool
	}{
		{"a symlink to another state file", func(path, other string) error { return os.Symlink(other, path) }, false},
		{"a FIFO", func(path, _ string) error { return syscall.Mkfifo(path, 0o600) }, false},
		{"a directory", func(path, _ string) error { return os.Mkdir(path, 0o700) }, false},
		{"a regular file", func(path, _ string) error { return os.WriteFile(path, []byte("working: x\n"), 0o600) }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			state := filepath.Join(homeDir, "state")
			if err := os.Mkdir(state, 0o700); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(state, "other")
			if err := os.WriteFile(other, []byte("working: x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tt.plant(filepath.Join(state, "t1.status"), other); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := PrepareForcedRetirementEvidence(homeDir, "t1")
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("the evidence copy blocked on %s", tt.name)
			}
			b, err := os.ReadFile(filepath.Join(state, ".backup", "t1", "t1.status"))
			if tt.copy && (err != nil || string(b) != "working: x\n") {
				t.Fatalf("backup = %q, %v; want the status copied", b, err)
			}
			if !tt.copy && err == nil {
				t.Fatalf("backup = %q; want nothing copied through %s", b, tt.name)
			}
		})
	}
}
