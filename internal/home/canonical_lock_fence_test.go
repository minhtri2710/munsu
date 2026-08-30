package home

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestNextFenceAdvancesInSiblingFile pins nextFence's contract: it reads the
// previous token from the sibling .fence file, advances it by one, and persists
// the new token atomically. A missing file (a scope locked for the first time)
// yields token 1, and each subsequent call reads the persisted value and returns
// one more.
func TestNextFenceAdvancesInSiblingFile(t *testing.T) {
	dir := t.TempDir()
	fence := filepath.Join(dir, "scope.fence")

	// No fence file yet, as on a scope's first lock. First token must be 1.
	if tok, err := nextFence(fence); err != nil {
		t.Fatalf("nextFence on missing file: %v", err)
	} else if tok != 1 {
		t.Fatalf("first nextFence = %d, want 1", tok)
	}
	if got := readFileString(t, fence); got != "1\n" {
		t.Fatalf("fence file after first advance = %q, want %q", got, "1\n")
	}

	// A second advance must read the persisted "1" and return 2.
	if tok, err := nextFence(fence); err != nil {
		t.Fatalf("nextFence after seed: %v", err)
	} else if tok != 2 {
		t.Fatalf("second nextFence = %d, want 2", tok)
	}
	if got := readFileString(t, fence); got != "2\n" {
		t.Fatalf("fence file after second advance = %q, want %q", got, "2\n")
	}

	// A later holder advances the persisted file (value 2), giving 3.
	if tok, err := nextFence(fence); err != nil {
		t.Fatalf("nextFence on persisted fence: %v", err)
	} else if tok != 3 {
		t.Fatalf("reopened nextFence = %d, want 3", tok)
	}
	if got := readFileString(t, fence); got != "3\n" {
		t.Fatalf("fence file after third advance = %q, want %q", got, "3\n")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestLockFenceRoundTripsThroughSiblingFile goes through the public Lock flow:
// the fence written by a Release'd holder must be read and advanced by the next
// holder. The monotonic advance is what the commit-conflict machinery depends
// on. The counter lives in the sibling .fence file; the .lock file stays
// content-free so a crash mid-write can never truncate the fence to an empty or
// short value.
func TestLockFenceRoundTripsThroughSiblingFile(t *testing.T) {
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

	// The persisted fence value is exactly the second token.
	if got, want := readFileString(t, h.fencePath("fence-rt")), fmt.Sprintf("%d\n", second); got != want {
		t.Fatalf("persisted fence = %q, want %q", got, want)
	}

	// The lock file itself carries no content: it is a pure flock target.
	info, err := os.Stat(lk2.path)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("lock file size = %d, want 0 (content-free)", info.Size())
	}
}
