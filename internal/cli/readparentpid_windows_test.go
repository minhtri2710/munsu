//go:build windows

package cli

import (
	"os"
	"testing"
)

// TestReadParentPIDWindows verifies the Windows readParentPID reports the true
// parent of the current process and walks the ancestry without looping or
// inventing a parent it did not observe.
func TestReadParentPIDWindows(t *testing.T) {
	ppid := readParentPID(os.Getpid())
	expected := os.Getppid()
	if ppid != expected {
		t.Fatalf("readParentPID(%d) = %d, want %d (os.Getppid())", os.Getpid(), ppid, expected)
	}

	// Walk the ancestry: a PID repeated within the walk means the walk loops.
	visited := map[int]bool{}
	pid := os.Getpid()
	for steps := 0; pid > 1 && steps < 8; steps++ {
		if visited[pid] {
			t.Fatalf("ancestry cycle at pid %d on step %d", pid, steps)
		}
		visited[pid] = true
		next := readParentPID(pid)
		if next < 0 {
			break
		}
		pid = next
	}

	// A PID far beyond the Windows allocation range cannot exist; returning a
	// fabricated parent would be a false success, so it must fail closed.
	if p := readParentPID(1 << 30); p >= 0 {
		t.Errorf("readParentPID(1<<30) = %d, want -1 (fail closed, no invented parent)", p)
	}
}
