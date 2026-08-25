//go:build !windows

package backend

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// newRendezvous builds the blocking point out of a POSIX FIFO. A FIFO open for
// reading completes only when a writer opens the same FIFO, so the write-end
// open returning is proof that the fake's read is already in progress.
func newRendezvous(t *testing.T) rendezvous {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blocked.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
	return rendezvous{
		path: path,
		await: func() (io.Closer, error) {
			return os.OpenFile(path, os.O_WRONLY, 0)
		},
	}
}
