package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E2E-style tests for guard middleware using real temp home directories,
// real beat files, and real meta files. These call guardWarnWatcher()
// directly rather than going through cobra command execution, exercising
// the full home.Resolve → fleet.Snapshot → lifecycle.ReadBeatStatus chain
// with real file I/O on a temp home.

// TestGuardE2E_StaleBeatWarns tests that guardWarnWatcher emits a WARNING
// when tasks are in flight and the watcher beat is stale.
func TestGuardE2E_StaleBeatWarns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for stale beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "1 task(s) in flight") {
		t.Errorf("expected '1 task(s) in flight' in warning, got: %s", stderr)
	}
	if !strings.Contains(stderr, "stale") {
		t.Errorf("expected 'stale' status in warning, got: %s", stderr)
	}
}

// TestGuardE2E_MissingBeatWarns tests that guardWarnWatcher emits a WARNING
// when tasks are in flight but no watcher beat file exists.
func TestGuardE2E_MissingBeatWarns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "scout")
	// No beat file written — beat is missing

	stderr := captureStderr(guardWarnWatcher)

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for missing beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "missing") {
		t.Errorf("expected 'missing' status in warning, got: %s", stderr)
	}
}

// TestGuardE2E_FreshBeatSilent tests that guardWarnWatcher is silent
// when a fresh watcher beat exists.
func TestGuardE2E_FreshBeatSilent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with healthy beat, got: %s", stderr)
	}
}

// TestGuardE2E_SkipEnvSilent tests that guardWarnWatcher is silent
// when MUNSU_GUARD_SKIP=1 is set, even with stale beat and in-flight tasks.
func TestGuardE2E_SkipEnvSilent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_GUARD_SKIP", "1")

	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with MUNSU_GUARD_SKIP=1, got: %s", stderr)
	}
}

// TestGuardE2E_NoTasksSilent tests that guardWarnWatcher is silent
// when no task meta files exist.
func TestGuardE2E_NoTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// No task meta files, no beat
	stderr := captureStderr(guardWarnWatcher)

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no tasks, got: %s", stderr)
	}
}

// TestGuardE2E_NoInFlightTasksSilent tests that guardWarnWatcher is silent
// when tasks exist but none are in-flight (kind != ship/scout).
func TestGuardE2E_NoInFlightTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Task with kind=done — not in-flight
	meta := "kind=done\nwindow=test\n"
	writeMeta(t, tmpDir, "register-task", meta)
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no in-flight tasks, got: %s", stderr)
	}
}

// TestGuardE2E_MultipleInFlightWarns tests that guardWarnWatcher warns
// with the correct count when multiple tasks are in flight.
func TestGuardE2E_MultipleInFlightWarns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Two in-flight tasks: one ship, one scout
	writeTaskMeta(t, tmpDir, "ship-task", "ship")
	writeTaskMeta(t, tmpDir, "scout-task", "scout")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING for 2 in-flight tasks with stale beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "2 task(s) in flight") {
		t.Errorf("expected '2 task(s) in flight' in warning, got: %s", stderr)
	}
}

// TestGuardE2E_MixedTasksWarnsWithCorrectCount tests that guardWarnWatcher
// correctly counts only in-flight tasks (ship/scout) when mixed with non-in-flight tasks.
func TestGuardE2E_MixedTasksWarnsWithCorrectCount(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "ship-task", "ship")
	writeTaskMeta(t, tmpDir, "done-task", "done")
	writeTaskMeta(t, tmpDir, "idle-task", "idle")
	writeStaleBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING for 1 in-flight + 2 non-in-flight tasks, got: %s", stderr)
	}
	if !strings.Contains(stderr, "1 task(s) in flight") {
		t.Errorf("expected '1 task(s) in flight' in warning, got: %s", stderr)
	}
}

// TestGuardE2E_FreshBeatMultiTaskSilent tests that guardWarnWatcher is silent
// when a fresh beat exists even with multiple in-flight tasks.
func TestGuardE2E_FreshBeatMultiTaskSilent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	writeTaskMeta(t, tmpDir, "ship-task", "ship")
	writeTaskMeta(t, tmpDir, "scout-task", "scout")
	writeBeat(t, tmpDir)

	stderr := captureStderr(guardWarnWatcher)

	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with fresh beat and 2 in-flight tasks, got: %s", stderr)
	}
}

func writeMeta(t *testing.T, homeDir, id, content string) {
	t.Helper()
	metaDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, id+".meta"), []byte(content), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}
}
