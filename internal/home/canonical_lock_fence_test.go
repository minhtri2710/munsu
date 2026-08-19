package home

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// readFenceHandle returns the bytes currently visible through h's own offset-0
// read, i.e. it reads through the caller's open handle rather than opening a
// second one. The point of the nextFence fix (#532) is precisely that the
// fence must be readable through the single handle the caller already holds —
// on windows, a byte-range lock on that handle denies access through any
// second handle, so os.ReadFile(path) is not merely slow, it fails.
func readFenceHandle(t *testing.T, h *os.File) string {
	t.Helper()
	if _, err := h.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	data, err := io.ReadAll(h)
	if err != nil {
		t.Fatalf("read through held handle: %v", err)
	}
	return string(data)
}

// TestNextFenceReadsAndAdvancesThroughHeldHandle pins nextFence's contracted
// input/output behaviour: it reads the previous token through the SAME open
// handle it is given (never a second handle), advances it by one, and persists
// the new token through that same handle, so a subsequent call on the same
// handle sees the persisted value. The empty-file case (a lock file about to
// be used for the first time) must yield token 1, not stall or error.
func TestNextFenceReadsAndAdvancesThroughHeldHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scope.lock")

	// A fresh lock file, as created by Home.Lock on first use: empty, one open
	// handle that calls nextFence. First token must be 1.
	fresh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if tok, err := nextFence(fresh); err != nil {
		t.Fatalf("nextFence on empty file: %v", err)
	} else if tok != 1 {
		t.Fatalf("first nextFence = %d, want 1", tok)
	}
	if got := readFenceHandle(t, fresh); got != "1\n" {
		t.Fatalf("fence file after first advance = %q, want %q", got, "1\n")
	}

	// A second advance through the same handle must read the persisted "1"
	// through the handle and return 2.
	if tok, err := nextFence(fresh); err != nil {
		t.Fatalf("nextFence after seed: %v", err)
	} else if tok != 2 {
		t.Fatalf("second nextFence = %d, want 2", tok)
	}
	if got := readFenceHandle(t, fresh); got != "2\n" {
		t.Fatalf("fence file after second advance = %q, want %q", got, "2\n")
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	// A later holder reopens the persisted file (value 2) and advances it: the
	// read must come from the handle's current contents, giving 3.
	reopened, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if tok, err := nextFence(reopened); err != nil {
		t.Fatalf("nextFence on persisted fence: %v", err)
	} else if tok != 3 {
		t.Fatalf("reopened nextFence = %d, want 3", tok)
	}
	if got := readFenceHandle(t, reopened); got != "3\n" {
		t.Fatalf("fence file after reopen = %q, want %q", got, "3\n")
	}
}

// TestLockFenceRoundTripsThroughHeldHandle goes through the public Lock flow:
// the fence written by a Release'd holder must be read and advanced by the
// next holder (this was reading through a second os.ReadFile handle before
// #532). Asserting on FenceToken values is behaviour, and the monotonic
// advance is what the commit-conflict machinery depends on.
func TestLockFenceRoundTripsThroughHeldHandle(t *testing.T) {
	h := newTestHome(t)
	lk1, err := h.Lock("fence-rt")
	if err != nil {
		t.Fatal(err)
	}
	first := lk1.FenceToken()
	if first == 0 {
		t.Fatal("first token is zero")
	}
	if err := lk1.Release(); err != nil {
		t.Fatal(err)
	}

	lk2, err := h.Lock("fence-rt")
	if err != nil {
		t.Fatal(err)
	}
	defer lk2.Release()
	second := lk2.FenceToken()
	if second != first+1 {
		t.Fatalf("second token = %d, want %d (fence value from prior holder)", second, first+1)
	}

	// The persisted value is exactly the second token. It must be readable
	// through lk2's own handle while the exclusive lock is still held: on
	// windows, opening a second handle (as os.ReadFile would) is denied access
	// to the byte-range-locked region, and reading through the held handle is
	// precisely what #532's nextFence fix guarantees.
	if _, err := lk2.file.Seek(0, 0); err != nil {
		t.Fatalf("seek locked handle: %v", err)
	}
	data, err := io.ReadAll(lk2.file)
	if err != nil {
		t.Fatalf("read fence through locked handle: %v", err)
	}
	if want := fmt.Sprintf("%d\n", second); string(data) != want {
		t.Fatalf("persisted fence = %q, want %q", string(data), want)
	}
}
