//go:build darwin || linux

package fleet

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeTool puts a tool named name on PATH that runs body, a POSIX sh script,
// and leaves no other tool reachable.
func fakeTool(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestLsofProcessDetails(t *testing.T) {
	const script = `case "$*" in
*cwd*) printf 'p7\nn/private/tmp/dev\n'; exit 0;;
*) printf 'p7\nn*:8080\nn127.0.0.1:3000\nn[::1]:3000\n'; exit 0;;
esac`
	t.Run("cwd and sorted de-duplicated listening ports", func(t *testing.T) {
		fakeTool(t, "lsof", script)
		got := lsofProcessDetails([]int{7, 8})
		if want := (processDetail{cwd: "/private/tmp/dev", ports: "3000,8080"}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
		if want := (processDetail{cwd: unknownProcessField, ports: "none"}); got[8] != want {
			t.Fatalf("pid 8 = %+v, want %+v", got[8], want)
		}
	})
	t.Run("lsof finding nothing for a pid means no listeners, cwd unknown", func(t *testing.T) {
		fakeTool(t, "lsof", "exit 1")
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: "none"}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
	t.Run("a failing lsof leaves every field unknown", func(t *testing.T) {
		fakeTool(t, "lsof", "exit 2")
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: unknownProcessField}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
	t.Run("an absent lsof leaves every field unknown", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: unknownProcessField}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
}

func TestLsofProcessDetailsGivesUpOnAHungLsof(t *testing.T) {
	old := taskProcessExecTimeout
	taskProcessExecTimeout = 100 * time.Millisecond
	t.Cleanup(func() { taskProcessExecTimeout = old })
	fakeTool(t, "lsof", "PATH=/bin:/usr/bin exec sleep 5")
	start := time.Now()
	got := lsofProcessDetails([]int{7})
	if want := (processDetail{cwd: unknownProcessField, ports: unknownProcessField}); got[7] != want {
		t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("lsof held the report for %v", elapsed)
	}
}

func TestTeardownLineage(t *testing.T) {
	t.Run("a table without the teardown pid still names the parent", func(t *testing.T) {
		fakeTool(t, "ps", "exit 0")
		if lineage := teardownLineage(); !lineage[os.Getpid()] || !lineage[os.Getppid()] {
			t.Fatalf("lineage = %v, want self and parent", lineage)
		}
	})
}

func TestSurvivingTaskProcessStepsRunsNoPsWithoutACandidate(t *testing.T) {
	homeDir := t.TempDir()
	ran := filepath.Join(t.TempDir(), "ps-ran")
	fakeTool(t, "ps", "echo ran > "+ran)
	useTaskProcessFakes(t, fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(800, "T-2", homeDir)}}}, nil)
	if got := survivingTaskProcessSteps(homeDir, "T-1", nil); len(got) != 0 {
		t.Fatalf("steps = %q, want none", got)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("ps ran although no task process remained")
	}
}
