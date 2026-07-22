package waker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// writeBeatFile writes a watcher liveness beat file at the given timestamp.
func writeBeatFile(t *testing.T, homeDir string, ts int64) {
	t.Helper()
	path := lifecycle.BeatPath(homeDir)
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
	path := lifecycle.QueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// formatBeatContent formats a unix timestamp as the decimal + pid string
// expected by lifecycle.ReadBeat.
func formatBeatContent(ts int64) string {
	// Use the same format as lifecycle.WriteBeat: "<unix_ts> <pid>"
	return fmt.Sprintf("%d %d", ts, 12345)
}

func TestCheckGuard_NeverStarted(t *testing.T) {
	home := t.TempDir()
	warnings := CheckGuard(home)

	if len(warnings) == 0 {
		t.Fatal("expected warnings, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "WATCHER NEVER STARTED") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning containing 'WATCHER NEVER STARTED', got %v", warnings)
	}
}

func TestCheckGuard_StaleWatcher(t *testing.T) {
	home := t.TempDir()
	// Write a beat file with a stale timestamp (>300s in the past)
	writeBeatFile(t, home, time.Now().Add(-400*time.Second).Unix())

	warnings := CheckGuard(home)

	if len(warnings) == 0 {
		t.Fatal("expected warnings, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "WATCHER BEACON STALE") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning containing 'WATCHER BEACON STALE', got %v", warnings)
	}
}

func TestCheckGuard_QueuedWake(t *testing.T) {
	home := t.TempDir()
	// Write a fresh beat so we only trigger on queued wakes
	writeBeatFile(t, home, time.Now().Unix())
	writeWakeQueue(t, home, []string{"1780000000\t1\tsignal\tkey\tpayload"})

	warnings := CheckGuard(home)

	if len(warnings) == 0 {
		t.Fatal("expected warnings, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "QUEUED WAKES PENDING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning containing 'QUEUED WAKES PENDING', got %v", warnings)
	}
}

func TestCheckGuard_AllClear(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	warnings := CheckGuard(home)

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestCheckGuard_EmptyWakeQueue(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	// Write an empty wake queue file
	writeWakeQueue(t, home, []string{""})

	warnings := CheckGuard(home)

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty queue, got %v", warnings)
	}
}

func TestPrintRecords_Empty(t *testing.T) {
	// Should not panic or produce output
	PrintRecords(nil)
	PrintRecords([]Record{})
}

func TestPrintRecords_NonEmpty(t *testing.T) {
	records := []Record{
		{Epoch: "1780000000", Seq: "1", Kind: "signal", Key: "task-abc", Payload: "build done"},
		{Epoch: "1780000001", Seq: "2", Kind: "stale", Key: "task-xyz", Payload: "watcher dead"},
	}

	// Capture stdout
	r := captureStdout(t, func() {
		PrintRecords(records)
	})

	// Verify each record is printed as a tab-separated line
	for _, rec := range records {
		expected := rec.Epoch + "\t" + rec.Seq + "\t" + rec.Kind + "\t" + rec.Key + "\t" + rec.Payload
		if !strings.Contains(r, expected) {
			t.Errorf("expected output to contain %q, got %q", expected, r)
		}
	}
}

// captureStdout runs fn and returns its stdout output as a string.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	w.Close()

	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
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
	queuePath := lifecycle.QueuePath(home)
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
	lifecycle.EnqueueWake(home, "signal", "task-fresh", "done: just finished")

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
	queuePath := lifecycle.QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	line := fmt.Sprintf("%d	%d\tsignal\ttask-old\tdone: very old\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	if !HasAgedMaterialWake(home, time.Now()) {
		t.Fatal("HasAgedMaterialWake should be true for old material wake")
	}
}

func TestHasAgedMaterialWake_Fresh(t *testing.T) {
	home := t.TempDir()
	lifecycle.EnqueueWake(home, "signal", "task-fresh", "done: fresh")

	if HasAgedMaterialWake(home, time.Now()) {
		t.Fatal("HasAgedMaterialWake should be false for fresh wake")
	}
}

func TestHasAgedMaterialWake_NonMaterialWakes(t *testing.T) {
	home := t.TempDir()
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := lifecycle.QueuePath(home)
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
