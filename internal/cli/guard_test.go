//go:build integration

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStderr runs f and returns everything written to os.Stderr during f.
func captureStderr(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w

	f()

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// writeTaskMeta creates a minimal task meta file with the given kind.
func writeTaskMeta(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	meta := fmt.Sprintf("kind=%s\nwindow=test\n", kind)
	metaDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, id+".meta"), []byte(meta), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}
}

// writeBeat creates a fresh watcher beat file.
func writeBeat(t *testing.T, homeDir string) {
	t.Helper()
	beat := fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beat), 0644); err != nil {
		t.Fatalf("writing beat: %v", err)
	}
}

// writeStaleBeat creates a watcher beat from 10 minutes ago.
func writeStaleBeat(t *testing.T, homeDir string) {
	t.Helper()
	beat := fmt.Sprintf("%d %d", time.Now().Add(-10*time.Minute).Unix(), os.Getpid())
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beat), 0644); err != nil {
		t.Fatalf("writing stale beat: %v", err)
	}
}

// TestGuardWarningOnStaleBeat verifies the guard warns when tasks are in flight
// but the watcher beat is stale.
func TestGuardWarningOnStaleBeat(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for stale beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "1 task(s) in flight") {
		t.Errorf("expected '1 task(s) in flight' in warning, got: %s", stderr)
	}
}

// TestGuardWarningOnMissingBeat verifies the guard warns when tasks are in flight
// but the watcher beat file does not exist.
func TestGuardWarningOnMissingBeat(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "scout")
	// No beat file written

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for missing beat, got: %s", stderr)
	}
}

// TestGuardSilenceHealthyBeat verifies no warning when watcher beat is fresh.
func TestGuardSilenceHealthyBeat(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeBeat(t, tmpDir)

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with healthy beat, got: %s", stderr)
	}
}

// TestGuardSilenceSkipEnv verifies no warning when MUNSU_GUARD_SKIP=1 is set.
func TestGuardSilenceSkipEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_GUARD_SKIP", "1")

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with MUNSU_GUARD_SKIP=1, got: %s", stderr)
	}
}

// TestGuardSilenceNoTasks verifies no warning when no tasks exist.
func TestGuardSilenceNoTasks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no tasks, got: %s", stderr)
	}
}

// TestGuardSilenceNoInFlightTasks verifies no warning when tasks exist but none are in-flight.
func TestGuardSilenceNoInFlightTasks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a task without kind=ship/scout — not in-flight
	meta := "kind=done\nwindow=test\n"
	metaDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "test-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"doctor"})
		if err := root.Execute(); err != nil {
			t.Errorf("doctor: unexpected error: %v", err)
		}
	})

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no in-flight tasks, got: %s", stderr)
	}
}

// captureStdout runs f and returns everything written to os.Stdout during f.
func captureStdout(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// writeWakeQueue writes wake queue entries to the home state dir.
func writeWakeQueue(t *testing.T, homeDir string, lines []string) {
	t.Helper()
	data := strings.Join(lines, "\n")
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".wake-queue"), []byte(data), 0644); err != nil {
		t.Fatalf("writing wake queue: %v", err)
	}
}

// TestGuardContractOutput_StableCodes verifies the contract guard command emits stable condition codes.
func TestGuardContractOutput_StableCodes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	t.Run("watcher_absent_code", func(t *testing.T) {
		// No beat file — should emit watcher_absent code
		out := captureStdout(func() {
			root := NewRootCommand()
			root.SetArgs([]string{"guard"})
			if err := root.Execute(); err != nil {
				t.Errorf("guard: unexpected error: %v", err)
			}
		})

		if !strings.Contains(out, "watcher_absent") {
			t.Errorf("expected 'watcher_absent' code in output, got: %s", out)
		}
		if !strings.Contains(out, "WATCHER NEVER STARTED") {
			t.Errorf("expected human-readable message in output, got: %s", out)
		}
	})

	t.Run("watcher_stale_code", func(t *testing.T) {
		writeStaleBeat(t, tmpDir)

		out := captureStdout(func() {
			root := NewRootCommand()
			root.SetArgs([]string{"guard"})
			if err := root.Execute(); err != nil {
				t.Errorf("guard: unexpected error: %v", err)
			}
		})

		if !strings.Contains(out, "watcher_stale") {
			t.Errorf("expected 'watcher_stale' code in output, got: %s", out)
		}
		if !strings.Contains(out, "WATCHER BEACON STALE") {
			t.Errorf("expected human-readable message in output, got: %s", out)
		}
	})

	t.Run("queued_wakes_pending_code", func(t *testing.T) {
		writeBeat(t, tmpDir)
		writeWakeQueue(t, tmpDir, []string{"1780000000\t1\tsignal\tkey\tpayload"})

		out := captureStdout(func() {
			root := NewRootCommand()
			root.SetArgs([]string{"guard"})
			if err := root.Execute(); err != nil {
				t.Errorf("guard: unexpected error: %v", err)
			}
		})

		if !strings.Contains(out, "queued_wakes_pending") {
			t.Errorf("expected 'queued_wakes_pending' code in output, got: %s", out)
		}
		if !strings.Contains(out, "QUEUED WAKES PENDING") {
			t.Errorf("expected human-readable message in output, got: %s", out)
		}
	})
}
