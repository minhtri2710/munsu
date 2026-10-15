//go:build integration

package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGuardE2E_StaleBeatWarns(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING on stderr for stale beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "1 task(s) working") {
		t.Errorf("expected '1 task(s) working' in warning, got: %s", stderr)
	}
	if !strings.Contains(stderr, "stale") {
		t.Errorf("expected 'stale' status in warning, got: %s", stderr)
	}
}

// A missing beat warns the dispatcher; a soldier, which cannot repair its
// dispatcher's watcher, skips the pre-run guard without opening the home.
func TestGuardE2E_MissingBeatWarns(t *testing.T) {
	for _, tc := range []struct {
		role string
		warn bool
	}{
		{"general", true},
		{"soldier", false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			tmpDir := t.TempDir()
			initCLITestHome(t, tmpDir)
			t.Setenv("MUNSU_HOME", tmpDir)
			t.Setenv("MUNSU_ROLE", tc.role)
			writeWorkingTaskMeta(t, tmpDir, "test-task", "scout")
			stderr := captureStderr(func() { _ = guardWarnWatcher() })
			if got := strings.Contains(stderr, "WARNING:") && strings.Contains(stderr, "missing"); got != tc.warn {
				t.Errorf("missing-beat warning = %v, want %v; stderr: %s", got, tc.warn, stderr)
			}
		})
	}
}

func TestGuardE2E_FreshBeatSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with healthy beat, got: %s", stderr)
	}
}

func TestGuardE2E_SkipEnvSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with MUNSU_GUARD_SKIP=1, got: %s", stderr)
	}
}

func TestGuardE2E_NoTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no tasks, got: %s", stderr)
	}
}

func TestGuardE2E_NoInFlightTasksSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	// A canonical task with a non-ship/scout kind is not in-flight.
	initHomeWithTask(t, tmpDir, "register-task", "done")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(stderr, "WARNING:") {
		t.Errorf("unexpected WARNING with no in-flight tasks, got: %s", stderr)
	}
}

func TestGuardE2E_MultipleInFlightWarns(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "ship-task", "ship")
	writeWorkingTaskMeta(t, tmpDir, "scout-task", "scout")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING for 2 in-flight tasks with stale beat, got: %s", stderr)
	}
	if !strings.Contains(stderr, "2 task(s) working") {
		t.Errorf("expected '2 task(s) working' in warning, got: %s", stderr)
	}
}

func TestGuardE2E_MixedTasksWarnsWithCorrectCount(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "ship-task", "ship")
	initHomeWithTask(t, tmpDir, "done-task", "done")
	initHomeWithTask(t, tmpDir, "idle-task", "idle")
	writeStaleBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("expected WARNING for 1 in-flight + 2 non-in-flight tasks, got: %s", stderr)
	}
	if !strings.Contains(stderr, "1 task(s) working") {
		t.Errorf("expected '1 task(s) working' in warning, got: %s", stderr)
	}
}

func TestGuardE2E_FreshBeatMultiTaskSilent(t *testing.T) {
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "ship-task", "ship")
	writeWorkingTaskMeta(t, tmpDir, "scout-task", "scout")
	writeBeat(t, tmpDir)
	stderr := captureStderr(func() { _ = guardWarnWatcher() })
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
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	second := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(second, "WARNING:") {
		t.Errorf("second call with same state expected suppressed WARNING, got: %s", second)
	}
}

func TestGuardE2E_CooldownExpiredReWarns(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	cdPath := guardCooldownPath(tmpDir)
	oldTs := time.Now().Add(-6 * time.Minute).Unix()
	_ = os.WriteFile(cdPath, []byte("stale:1\n"+fmt.Sprint(oldTs)+"\n"), 0644)
	second := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(second, "WARNING:") {
		t.Errorf("second call after cooldown expiry expected WARNING, got: %s", second)
	}
}

func TestGuardE2E_CooldownStateChangeReWarns(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "task-a", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	writeWorkingTaskMeta(t, tmpDir, "task-b", "scout")
	second := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(second, "WARNING:") {
		t.Errorf("second call with state change expected WARNING, got: %s", second)
	}
	if !strings.Contains(second, "2 task(s) working") {
		t.Errorf("expected '2 task(s) working' after state change, got: %s", second)
	}
}

func TestGuardE2E_HealthyTransitionClearsCooldown(t *testing.T) {
	original := guardCooldown
	guardCooldown = 5 * time.Minute
	defer func() { guardCooldown = original }()
	tmpDir := t.TempDir()
	initCLITestHome(t, tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	writeWorkingTaskMeta(t, tmpDir, "test-task", "ship")
	writeStaleBeat(t, tmpDir)
	first := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(first, "WARNING:") {
		t.Errorf("first call expected WARNING, got: %s", first)
	}
	writeBeat(t, tmpDir)
	healthy := captureStderr(func() { _ = guardWarnWatcher() })
	if strings.Contains(healthy, "WARNING:") {
		t.Errorf("healthy call unexpected WARNING, got: %s", healthy)
	}
	writeStaleBeat(t, tmpDir)
	third := captureStderr(func() { _ = guardWarnWatcher() })
	if !strings.Contains(third, "WARNING:") {
		t.Errorf("third call after healthy->unhealthy transition expected WARNING, got: %s", third)
	}
}
