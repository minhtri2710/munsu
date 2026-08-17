package orchestrator

import (
	"fmt"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBeatFile writes a watcher liveness beat file at the given timestamp.
func writeBeatFile(t *testing.T, homeDir string, ts int64) {
	t.Helper()
	path := mhome.WatcherBeatPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := formatBeatContent(ts) + " 12345"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// writeWakeQueue writes tab-separated wake queue entries.
func writeWakeQueue(t *testing.T, homeDir string, lines []string) {
	t.Helper()
	path := QueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// formatBeatContent formats a unix timestamp as the decimal + pid string
// expected by ReadBeat.
func formatBeatContent(ts int64) string {
	// Use the same format as WriteBeat: "<unix_ts> <pid>"
	return fmt.Sprintf("%d %d", ts, 12345)
}

// TestEvaluateGuard_QueuedWakesPendingHasCode verifies queued wakes condition has a stable code.
func TestEvaluateGuard_QueuedWakesPendingHasCode(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	writeWakeQueue(t, home, []string{"1780000000\t1\tsignal\tkey\tpayload"})

	result := EvaluateGuard(home, 0, time.Now())

	if len(result.Conditions) == 0 {
		t.Fatal("expected conditions, got none")
	}

	found := false
	for _, c := range result.Conditions {
		if c.Code == ConditionQueuedWakesPending {
			found = true
			if !strings.Contains(c.Message, "QUEUED WAKES PENDING") {
				t.Errorf("message should mention QUEUED WAKES PENDING, got: %s", c.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected condition code %q, got codes: %v", ConditionQueuedWakesPending, result.Conditions)
	}
}

// TestEvaluateGuard_CodesAreStable verifies condition codes are stable string constants.
func TestEvaluateGuard_CodesAreStable(t *testing.T) {
	// Verify codes are not ad-hoc sprintf strings — they're typed constants
	if string(ConditionQueuedWakesPending) != "queued_wakes_pending" {
		t.Errorf("ConditionQueuedWakesPending should be 'queued_wakes_pending', got %q", ConditionQueuedWakesPending)
	}
	if string(ConditionWatcherAbsent) != "watcher_absent" {
		t.Errorf("ConditionWatcherAbsent should be 'watcher_absent', got %q", ConditionWatcherAbsent)
	}
	if string(ConditionWatcherStale) != "watcher_stale" {
		t.Errorf("ConditionWatcherStale should be 'watcher_stale', got %q", ConditionWatcherStale)
	}
}

// TestEvaluateGuard_UnknownExplicit verifies conditions are explicit, even when empty.
func TestEvaluateGuard_UnknownExplicit(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	result := EvaluateGuard(home, 0, time.Now())

	// All clear should have no conditions, not a sentinel value
	if len(result.Conditions) != 0 {
		t.Errorf("expected no conditions for all-clear, got %d", len(result.Conditions))
	}
}

// TestEvaluateGuard_WatcherAbsentNotInEvaluateGuard verifies EvaluateGuard doesn't emit watcher codes.
func TestEvaluateGuard_WatcherAbsentNotInEvaluateGuard(t *testing.T) {
	home := t.TempDir()
	// No beat file written

	result := EvaluateGuard(home, 1, time.Now())

	// EvaluateGuard only produces queued_wakes_pending; watcher_absent comes from the CLI layer
	for _, c := range result.Conditions {
		if c.Code == ConditionWatcherAbsent || c.Code == ConditionWatcherStale {
			t.Errorf("EvaluateGuard should not emit watcher codes (those come from the CLI layer), got %q", c.Code)
		}
	}
}

// TestEvaluateGuard_AgedWakeProducesAgedWakeCondition proves that material
// wake entries older than MaterialWakeAgeThreshold produce the
// ConditionAgedWakePending code, making the guard unhealthy.
func TestEvaluateGuard_AgedWakeProducesAgedWakeCondition(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	// Enqueue a material wake with an old timestamp by manipulating the queue file.
	// Direct EnqueueWake adds current time, so write a TSV line manually.
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	line := fmt.Sprintf("%d	%d\tsignal\ttask-1\tdone: PR merged\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	result := EvaluateGuard(home, 1, time.Now())

	foundAgedWake := false
	for _, c := range result.Conditions {
		if c.Code == ConditionAgedWakePending {
			foundAgedWake = true
			if !strings.Contains(c.Message, "aged") {
				t.Errorf("aged wake condition message should contain 'aged', got: %s", c.Message)
			}
			break
		}
	}
	if !foundAgedWake {
		codes := make([]string, len(result.Conditions))
		for i, c := range result.Conditions {
			codes[i] = string(c.Code)
		}
		t.Errorf("expected ConditionAgedWakePending, got codes: %v", codes)
	}
}

func TestEvaluateGuard_FreshWakeNoAgedCondition(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	// Enqueue a fresh material wake.
	EnqueueWake(home, "signal", "task-fresh", "done: just finished")

	result := EvaluateGuard(home, 1, time.Now())

	for _, c := range result.Conditions {
		if c.Code == ConditionAgedWakePending {
			t.Errorf("fresh material wake should not produce aged condition")
		}
	}
}

func TestHasAgedMaterialWake_Threshold(t *testing.T) {
	home := t.TempDir()

	// Old material wake
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	line := fmt.Sprintf("%d	%d\tsignal\ttask-old\tdone: very old\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	if !HasAgedMaterialWake(home, time.Now()) {
		t.Fatal("HasAgedMaterialWake should be true for old material wake")
	}
}

func TestHasAgedMaterialWake_Fresh(t *testing.T) {
	home := t.TempDir()
	EnqueueWake(home, "signal", "task-fresh", "done: fresh")

	if HasAgedMaterialWake(home, time.Now()) {
		t.Fatal("HasAgedMaterialWake should be false for fresh wake")
	}
}

func TestHasAgedMaterialWake_NonMaterialWakes(t *testing.T) {
	home := t.TempDir()
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	// Routine wake, not material.
	line := fmt.Sprintf("%d	%d\tstale\ttask-routine\tworking: in progress\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	if HasAgedMaterialWake(home, time.Now()) {
		t.Fatal("HasAgedMaterialWake should be false for non-material (routine) wake")
	}
}

func TestConditionAgedWakePending_Constant(t *testing.T) {
	if string(ConditionAgedWakePending) != "aged_wake_pending" {
		t.Errorf("ConditionAgedWakePending = %q, want 'aged_wake_pending'", ConditionAgedWakePending)
	}
}

func TestMaterialWakeAgeThreshold_Constant(t *testing.T) {
	if MaterialWakeAgeThreshold != 5*time.Minute {
		t.Errorf("MaterialWakeAgeThreshold = %v, want 5m0s", MaterialWakeAgeThreshold)
	}
}
