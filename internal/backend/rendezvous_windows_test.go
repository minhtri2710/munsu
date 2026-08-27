//go:build windows

package backend

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
)

// newRendezvous builds the blocking point out of a loopback TCP socket rather
// than a POSIX FIFO. A FIFO is the wrong primitive here even though the fake
// runs under a POSIX shell: that shell's file operations live in the MSYS2
// emulation namespace, so a native Win32 process (this test binary) cannot open
// the shell's FIFO and vice versa — the two worlds never share the pipe. That
// is exactly why the earlier ConnectNamedPipe attempt received no client and
// hung. A loopback socket is in the IP namespace both sides share, so the
// shell's `cat < /dev/tcp/...` client connects to the native Go listener.
//
// The rendezvous is CANCELLABLE — there is no blocking syscall without a
// cancellation path:
//
//   - If the context is already cancelled on entry, await returns immediately
//     without opening the listener end, so it can never block.
//   - Otherwise Accept runs in a goroutine and the select also waits on
//     ctx.Done(). If cancellation arrives while Accept is pending, closeRV()
//     closes the listener, which unblocks the pending Accept; the goroutine's
//     Accept then returns an error we discard, and await returns ctx.Err().
//     Close is guarded by a sync.Once so the t.Cleanup path never double-closes.
//
// The accepted connection is held open as the test's end of the rendezvous;
// closing it (registered as cleanup) would hand `cat` EOF and let the fake exit
// before cancel() fires, so it is kept open until the test ends.
func newRendezvous(t *testing.T) rendezvous {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen rendezvous: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	// Bash's /dev/tcp client spells the target as /dev/tcp/<host>/<port>; this
	// is the argument the fake's `exec cat` redirects from, so the fake blocks
	// on the SAME socket the test listens on.
	path := fmt.Sprintf("/dev/tcp/127.0.0.1/%d", addr.Port)
	closeOnce := sync.Once{}
	closeRV := func() { closeOnce.Do(func() { _ = ln.Close() }) }
	t.Cleanup(closeRV)
	return rendezvous{
		path:     path,
		blockCmd: "  exec cat < \"" + path + "\"",
		await: func(ctx context.Context) (io.Closer, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			type res struct {
				conn net.Conn
				err  error
			}
			ch := make(chan res, 1)
			go func() {
				c, e := ln.Accept()
				ch <- res{conn: c, err: e}
			}()
			select {
			case r := <-ch:
				if r.err != nil {
					return nil, r.err
				}
				// Keep the connection open: closing it now would hand `cat`
				// EOF and let the fake exit before cancel() fires.
				return r.conn, nil
			case <-ctx.Done():
				// Unblock the pending Accept so the listener can be closed
				// without a stuck syscall; the goroutine's Accept then
				// returns an error we drop.
				closeRV()
				return nil, ctx.Err()
			}
		},
	}
}
