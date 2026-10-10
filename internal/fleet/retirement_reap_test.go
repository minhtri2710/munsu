//go:build integration

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeFuser puts a fuser(1) on PATH that runs body, a POSIX sh script, and
// leaves no other tool reachable.
func fakeFuser(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fuser"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestReapSleeperHelper is the process startMarkedSleeper re-executes the test
// binary as: a test binary is not system-protected, so its environment is
// readable to the process inventory on every host.
func TestReapSleeperHelper(t *testing.T) {
	if os.Getenv("MUNSU_REAP_SLEEPER") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
}

// startMarkedSleeper starts a long sleep whose launch environment carries the
// given ownership markers, reaps it in the background, and returns its pid.
func startMarkedSleeper(t *testing.T, taskID, homeDir string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestReapSleeperHelper$")
	cmd.Env = append(os.Environ(), "MUNSU_REAP_SLEEPER=1", "MUNSU_TASK_ID="+taskID, "MUNSU_HOME="+homeDir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd.Process.Pid
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	return err == nil && proc.Signal(syscall.Signal(0)) == nil
}

func TestWorktreeHolderPIDs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  string
		want    []int
		wantErr string
	}{
		{name: "no holder is exit 1 with no output", script: "exit 1"},
		{name: "holders are the listed pids", script: "echo '  12  34'", want: []int{12, 34}},
		{name: "exit 1 with output is not clear", script: "echo 5; exit 1", wantErr: "enumerating holders"},
		{name: "another failure is never clear", script: "exit 2", wantErr: "enumerating holders"},
		{name: "an unparseable pid is refused", script: "echo 12x", wantErr: `unparseable pid "12x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeFuser(t, tc.script)
			got, err := worktreeHolderPIDs(t.TempDir())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("worktreeHolderPIDs = %v, %v; want error %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || len(got) != len(tc.want) || (len(got) > 0 && (got[0] != tc.want[0] || got[1] != tc.want[1])) {
				t.Fatalf("worktreeHolderPIDs = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	t.Run("fuser absent is an error, not clear", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got, err := worktreeHolderPIDs(t.TempDir()); err == nil {
			t.Fatalf("worktreeHolderPIDs = %v, nil; want an error when fuser cannot run", got)
		}
	})
}

func TestTaskOwnedPIDsNamesOnlyThisTasksProcessesInThisHome(t *testing.T) {
	homeDir := t.TempDir()
	otherHome := t.TempDir()
	mine := startMarkedSleeper(t, "T-1", homeDir)
	otherTask := startMarkedSleeper(t, "T-2", homeDir)
	otherHomeTask := startMarkedSleeper(t, "T-1", otherHome)
	unresolvedHome := startMarkedSleeper(t, "T-1", filepath.Join(homeDir, "missing"))
	owned, err := taskOwnedPIDs(homeDir, "T-1")
	if err != nil {
		t.Fatalf("taskOwnedPIDs: %v", err)
	}
	if !owned[mine] {
		t.Fatalf("owned = %v, want the task's own process %d", owned, mine)
	}
	for name, pid := range map[string]int{"another task": otherTask, "another home": otherHomeTask, "an unresolvable home": unresolvedHome} {
		if owned[pid] {
			t.Errorf("owned = %v, includes the process of %s (%d)", owned, name, pid)
		}
	}
	if _, err := taskOwnedPIDs(filepath.Join(homeDir, "missing"), "T-1"); err == nil {
		t.Fatal("taskOwnedPIDs accepted a home that does not resolve")
	}
}

func TestReapWorktreeHolders(t *testing.T) {
	aliveScript := func(pid int) string {
		return "if kill -0 " + strconv.Itoa(pid) + " 2>/dev/null; then echo " + strconv.Itoa(pid) + "; else exit 1; fi"
	}
	t.Run("nothing holds the path", func(t *testing.T) {
		fakeFuser(t, "exit 1")
		if killed, err := reapWorktreeHolders(t.TempDir(), "T-1", "/wt"); len(killed) != 0 || err != nil {
			t.Fatalf("reapWorktreeHolders = %v, %v; want none, nil", killed, err)
		}
	})
	t.Run("a holder proven to be the task's is killed and the path clears", func(t *testing.T) {
		homeDir := t.TempDir()
		pid := startMarkedSleeper(t, "T-1", homeDir)
		fakeFuser(t, aliveScript(pid))
		killed, err := reapWorktreeHolders(homeDir, "T-1", "/wt")
		if n := len(killed); n != 1 || err != nil {
			t.Fatalf("reapWorktreeHolders = %d, %v; want 1, nil", n, err)
		}
		if processAlive(pid) {
			t.Fatalf("holder %d is still alive after the reap", pid)
		}
	})
	t.Run("a holder that is not provably the task's is never signalled", func(t *testing.T) {
		homeDir := t.TempDir()
		stranger := startMarkedSleeper(t, "T-2", homeDir)
		fakeFuser(t, aliveScript(stranger))
		killed, err := reapWorktreeHolders(homeDir, "T-1", "/wt")
		if n := len(killed); n != 0 || err == nil || !strings.Contains(err.Error(), "not provably task T-1's; not signalled") {
			t.Fatalf("reapWorktreeHolders = %d, %v; want the not-provable refusal", n, err)
		}
		if !processAlive(stranger) {
			t.Fatalf("the stranger %d was signalled", stranger)
		}
	})
	t.Run("a fuser failure is returned", func(t *testing.T) {
		fakeFuser(t, "exit 2")
		if killed, err := reapWorktreeHolders(t.TempDir(), "T-1", "/wt"); len(killed) != 0 || err == nil || !strings.Contains(err.Error(), "enumerating holders") {
			t.Fatalf("reapWorktreeHolders = %v, %v; want the enumeration error", killed, err)
		}
	})
	t.Run("an attribution failure keeps the holder unsignalled", func(t *testing.T) {
		fakeFuser(t, "echo 1")
		killed, err := reapWorktreeHolders(filepath.Join(t.TempDir(), "missing"), "T-1", "/wt")
		if n := len(killed); n != 0 || err == nil || !strings.Contains(err.Error(), "attributing holders of /wt") {
			t.Fatalf("reapWorktreeHolders = %d, %v; want the attribution refusal", n, err)
		}
	})
}
