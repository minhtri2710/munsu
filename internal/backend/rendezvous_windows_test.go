//go:build windows

package backend

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

var rendezvousSeq atomic.Uint64

// newRendezvous builds the blocking point out of a windows named pipe. A FIFO
// is the wrong primitive here even though the fake runs under a POSIX shell:
// the shell's FIFOs are an emulation private to its own runtime, and this test
// binary is a native windows process that cannot open one. A named pipe is the
// primitive both ends share, and it carries the same proof -- the server's
// connect completes only once the client has opened the pipe, so a returning
// connect is proof that the fake's read is already in progress (#549 group 13).
func newRendezvous(t *testing.T) rendezvous {
	t.Helper()
	name := fmt.Sprintf(`munsu-rendezvous-%d-%d`, os.Getpid(), rendezvousSeq.Add(1))
	wide, err := windows.UTF16PtrFromString(`\\.\pipe\` + name)
	if err != nil {
		t.Fatalf("pipe name %s: %v", name, err)
	}
	handle, err := windows.CreateNamedPipe(wide,
		windows.PIPE_ACCESS_OUTBOUND,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatalf("CreateNamedPipe %s: %v", name, err)
	}
	t.Cleanup(func() { windows.CloseHandle(handle) })
	return rendezvous{
		// A POSIX shell spells a windows named pipe with the UNC-style prefix
		// its path conversion understands.
		path: "//./pipe/" + name,
		await: func() (io.Closer, error) {
			if err := windows.ConnectNamedPipe(handle, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
				return nil, err
			}
			// The pipe handle is the test's end and stays open until cleanup
			// closes it; ending the rendezvous early would hand the fake EOF.
			return io.NopCloser(nil), nil
		},
	}
}
