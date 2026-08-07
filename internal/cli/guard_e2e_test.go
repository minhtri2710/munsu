//go:build integration

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuardE2E_StaleBeatWarns(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
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

func TestGuardE2E_MissingBeatWarns(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "test-task", "scout")
	stderr := captureStderr(guardWarnWatcher)
	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for missing beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "missing") {
		t.Errorf("expected 'missing' status in warning, got: %s", stderr)
	}
}

func TestGuardE2E_FreshBeatSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeBeat(t, tmpDir)
	stderr := captureStderr(guardWarnWatcher)
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with healthy beat, got: %s", stderr)
	}
}

func TestGuardE2E_SkipEnvSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(guardWarnWatcher)
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with MUNSU_GUARD_SKIP=1, got: %s", stderr)
	}
}

func TestGuardE2E_NoTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	stderr := captureStderr(guardWarnWatcher)
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no tasks, got: %s", stderr)
	}
}

func TestGuardE2E_NoInFlightTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	meta := "kind=done\nwindow=test\n"
	writeMeta(t, tmpDir, "register-task", meta)
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(guardWarnWatcher)
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no in-flight tasks, got: %s", stderr)
	}
}

func TestGuardE2E_MultipleInFlightWarns(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
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

func TestGuardE2E_MixedTasksWarnsWithCorrectCount(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
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

func TestGuardE2E_FreshBeatMultiTaskSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "ship-task", "ship")
	writeTaskMeta(t, tmpDir, "scout-task", "scout")
	writeBeat(t, tmpDir)
	stderr := captureStderr(guardWarnWatcher)
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with fresh beat and 2 in-flight tasks, got: %s", stderr)
	}
}

func TestGuardE2E_CooldownSuppressesIdenticalState(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(guardWarnWatcher)
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	captain := captureStderr(guardWarnWatcher)
	if strings.Contains(captain, "WARNING:") {
		t.Errorf("captain call with same state expected suppressed WARNING, got: %s", captain)
	}
}

func TestGuardE2E_CooldownExpiredReWarns(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(guardWarnWatcher)
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	cdPath := guardCooldownPath(tmpDir)
	oldTs := time.Now().Add(-6 * time.Minute).Unix()
	_ = os.WriteFile(cdPath, []byte("stale:1\n"+fmt.Sprint(oldTs)+"\n"), 0644)
	captain := captureStderr(guardWarnWatcher)
	if !strings.Contains(captain, "WARNING:") {
		t.Errorf("captain call after cooldown expiry expected WARNING, got: %s", captain)
	}
}

func TestGuardE2E_CooldownStateChangeReWarns(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "task-a", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(guardWarnWatcher)
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	writeTaskMeta(t, tmpDir, "task-b", "scout")
	captain := captureStderr(guardWarnWatcher)
	if !strings.Contains(captain, "WARNING:") {
		t.Errorf("captain call with state change expected WARNING, got: %s", captain)
	}
	if !strings.Contains(captain, "2 task(s) in flight") {
		t.Errorf("expected '2 task(s) in flight' after state change, got: %s", captain)
	}
}

func TestGuardE2E_HealthyTransitionClearsCooldown(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(guardWarnWatcher)
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	writeBeat(t, tmpDir)
	healthy := captureStderr(guardWarnWatcher)
	if strings.Contains(healthy, "WARNING:") {
		t.Errorf("healthy call unexpected WARNING, got: %s", healthy)
	}
	writeStaleBeat(t, tmpDir)
	third := captureStderr(guardWarnWatcher)
	if !strings.Contains(third, "WARNING:") {
		t.Errorf("third call after healthy->unhealthy transition expected WARNING, got: %s", third)
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
