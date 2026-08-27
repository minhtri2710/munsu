//go:build !windows

package backend

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRendezvous builds the blocking point out of a POSIX FIFO. A FIFO open for
// reading completes only when a writer opens the same FIFO, so the write-end
// open returning is proof that the fake's read is already in progress — see
// awaitFakeBlocking. The fake's `exec cat <path>` opens that read end itself,
// after the exec, so the surviving process is the blocked reader exec.Command
// will kill on cancellation.
//
// The POSIX utility is used rather than syscall.Mkfifo only to keep behaviour
// identical to the pre-#656 fixture that the unix host already exercises; the
// command is an explicit runtime PATH dependency of this blocking fixture.
func newRendezvous(t *testing.T) rendezvous {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blocked.fifo")
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return rendezvous{
		path:     path,
		blockCmd: "  exec cat \"" + path + "\"",
		await: func(ctx context.Context) (io.Closer, error) {
			return os.OpenFile(path, os.O_WRONLY, 0)
		},
	}
}
