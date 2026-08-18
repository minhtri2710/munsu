package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Return tests ---

func TestReturn_NoDaemonNoDigest(t *testing.T) {
	tmp := t.TempDir()
	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("Return on clean home: %v", err)
	}
	if report.HasActionable() {
		t.Fatal("Return on clean home: HasActionable() = true, want false")
	}
	if report.DigestedCount != 0 {
		t.Errorf("DigestedCount = %d, want 0", report.DigestedCount)
	}
	if len(report.Escalations) != 0 {
		t.Errorf("Escalations = %d, want 0", len(report.Escalations))
	}
	if IsActive(tmp) {
		t.Error("IsActive() = true after Return(), want false")
	}
}

func TestReturn_WithPendingDigest(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a digest with escalations.
	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "afk", Key: "task-1", Payload: "done: PR merged", Type: EscalationReviewReady, At: time.Now()},
			{Kind: "afk", Key: "task-2", Payload: "blocked: missing auth token", Type: EscalationFailure, At: time.Now()},
			{Kind: "afk", Key: "task-3", Payload: "routine update", Type: EscalationRoutine, At: time.Now()},
		},
		WedgeAlarm: &WedgeAlarm{
			Reason:     "watcher beat stale: age=10m0s",
			DetectedAt: time.Now(),
		},
		EscalatedCount: 2,
		RoutineCount:   1,
		FirstAt:        time.Now().Add(-5 * time.Minute),
		LastAt:         time.Now(),
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("Return with digest: %v", err)
	}

	if !report.HasActionable() {
		t.Fatal("Return with escalations: HasActionable() = false, want true")
	}
	if report.DigestedCount != 3 {
		t.Errorf("DigestedCount = %d, want 3", report.DigestedCount)
	}

	// Should have 2 escalations (routine not included).
	if len(report.Escalations) != 2 {
		t.Errorf("Escalations = %d, want 2", len(report.Escalations))
	}

	// Should have wedge alarm.
	if len(report.WedgeAlarms) != 1 {
		t.Errorf("WedgeAlarms = %d, want 1", len(report.WedgeAlarms))
	}
	if !strings.Contains(report.WedgeAlarms[0], "watcher beat stale") {
		t.Errorf("WedgeAlarms[0] = %q, want 'watcher beat stale'", report.WedgeAlarms[0])
	}

	// Should have blocked item.
	if len(report.BlockedItems) != 1 {
		t.Errorf("BlockedItems = %d, want 1", len(report.BlockedItems))
	}
	if !strings.Contains(report.BlockedItems[0], "missing auth token") {
		t.Errorf("BlockedItems[0] = %q, want 'missing auth token'", report.BlockedItems[0])
	}

	// Digest should be drained.
	if _, err := os.Stat(filepath.Join(stateDir, ".afk-digest")); err == nil {
		t.Error("digest file still exists after drain")
	}
}

func TestReturn_DigestRoutineOnly(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "check", Key: "health", Payload: "all green", Type: EscalationRoutine, At: time.Now()},
		},
		RoutineCount:   1,
		EscalatedCount: 0,
		FirstAt:        time.Now().Add(-1 * time.Minute),
		LastAt:         time.Now(),
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("Return with routine-only digest: %v", err)
	}

	if report.HasActionable() {
		t.Fatal("Routine-only digest: HasActionable() = true, want false")
	}
	if report.DigestedCount != 1 {
		t.Errorf("DigestedCount = %d, want 1", report.DigestedCount)
	}
	if len(report.Escalations) != 0 {
		t.Errorf("Escalations = %d for routine-only, want 0", len(report.Escalations))
	}
}

func TestReturn_DaemonRunningStopsCleanly(t *testing.T) {
	// This test verifies Return stops a daemon and clears state.
	// We simulate a running daemon by writing a lock file and flag,
	// then verify Return clears both.
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write flag and lock as if daemon is running (PID = self for testing).
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)
	lockPath := filepath.Join(tmp, afkLockFile)
	os.WriteFile(lockPath, []byte("0\n"), 0644) // PID 0 = not alive

	if !IsActive(tmp) {
		t.Fatal("IsActive() should be true before Return")
	}

	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}

	if IsActive(tmp) {
		t.Error("IsActive() = true after Return, want false")
	}
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("lock file still exists after Return")
	}
	if report.HasActionable() {
		t.Fatal("Return on running daemon (no digest): HasActionable() = true, want false")
	}
}

// --- Return string format tests ---

func TestReturnReport_StringClean(t *testing.T) {
	r := &ReturnReport{}
	s := r.String()
	if !strings.Contains(s, "All clear") {
		t.Errorf("clean report string should contain 'All clear', got: %q", s)
	}
}

func TestReturnReport_StringActionable(t *testing.T) {
	r := &ReturnReport{
		Escalations:   []string{"[failure] task-2: build broken"},
		WedgeAlarms:   []string{"watcher beat stale"},
		BlockedItems:  []string{"missing auth token"},
		DigestedCount: 3,
	}
	s := r.String()
	if !strings.Contains(s, "Actionable items remain") {
		t.Errorf("actionable report string missing 'Actionable items remain', got: %q", s)
	}
	if !strings.Contains(s, "build broken") {
		t.Errorf("report string missing escalation detail: %q", s)
	}
	if !strings.Contains(s, "watcher beat stale") {
		t.Errorf("report string missing wedge detail: %q", s)
	}
}

// TestReturnReport_LossyStopRefusesAllClear covers the #530 contract: a
// report that records a lossy stop must never read "All clear", even when the
// digest itself drained empty -- the stop may have dropped up to one window of
// entries that never reached the digest, so a clean digest is not evidence of
// a clean record. HasActionable must surface the loss, and the string form
// must name it.
func TestReturnReport_LossyStopRefusesAllClear(t *testing.T) {
	r := &ReturnReport{LossyStop: true}
	if !r.HasActionable() {
		t.Fatal("lossy report: HasActionable() = false, want true even with an empty digest")
	}
	s := r.String()
	if strings.Contains(s, "All clear") {
		t.Errorf("lossy report string claims 'All clear', got: %q", s)
	}
	if !strings.Contains(s, "Lossy stop") {
		t.Errorf("lossy report string missing the lossy-stop notice, got: %q", s)
	}
}

// TestReturnReport_StringLossyStop still reports the actionable summary after
// the lossy notice, so a caller reading the tail of the report is not told
// "All clear" there either.
func TestReturnReport_StringLossyStop(t *testing.T) {
	r := &ReturnReport{LossyStop: true, DigestedCount: 2}
	s := r.String()
	if !strings.Contains(s, "Actionable items remain") {
		t.Errorf("lossy report string missing 'Actionable items remain', got: %q", s)
	}
}

// TestReturnReport_StringCleanUnaffected pins that the lossy branch leaves the
// clean path alone: a report without LossyStop still reads "All clear".
func TestReturnReport_StringCleanUnaffected(t *testing.T) {
	r := &ReturnReport{DigestedCount: 1}
	s := r.String()
	if !strings.Contains(s, "All clear") {
		t.Errorf("clean report string missing 'All clear', got: %q", s)
	}
	if strings.Contains(s, "Lossy stop") {
		t.Errorf("clean report string mentions a lossy stop, got: %q", s)
	}
}

// --- IsClean tests ---

func TestIsClean_NoDigest(t *testing.T) {
	tmp := t.TempDir()
	if !IsClean(tmp) {
		t.Error("IsClean() = false without digest, want true")
	}
}

func TestIsClean_CleanDigest(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Routine-only digest = clean.
	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "check", Key: "health", Payload: "all green", Type: EscalationRoutine, At: time.Now()},
		},
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	if !IsClean(tmp) {
		t.Error("IsClean() = false for routine-only digest, want true")
	}
}

func TestIsClean_EscalatedDigest(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "afk", Key: "t1", Payload: "done: PR merged", Type: EscalationReviewReady, At: time.Now()},
		},
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	if IsClean(tmp) {
		t.Error("IsClean() = true for escalation digest, want false")
	}
}

func TestIsClean_WedgeAlarm(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	be := &BatchedEscalation{
		WedgeAlarm: &WedgeAlarm{
			Reason:     "repeated stale wake",
			DetectedAt: time.Now(),
		},
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	if IsClean(tmp) {
		t.Error("IsClean() = true with wedge alarm, want false")
	}
}

func TestIsClean_BlockedItem(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "afk", Key: "t1", Payload: "blocked: missing credentials", Type: EscalationFailure, At: time.Now()},
		},
	}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	if IsClean(tmp) {
		t.Error("IsClean() = true with blocked item, want false")
	}
}

// --- drainDigest tests ---

func TestDrainDigest_NoFile(t *testing.T) {
	tmp := t.TempDir()
	be, err := drainDigest(tmp)
	if err != nil {
		t.Fatalf("drainDigest with no file: %v", err)
	}
	if be != nil {
		t.Fatal("drainDigest returned non-nil for non-existent file")
	}
}

func TestDrainDigest_RemovesFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	be := &BatchedEscalation{Entries: nil}
	data, _ := json.Marshal(be)
	digestPath := filepath.Join(stateDir, ".afk-digest")
	os.WriteFile(digestPath, data, 0644)

	_, err := drainDigest(tmp)
	if err != nil {
		t.Fatalf("drainDigest: %v", err)
	}
	if _, err := os.Stat(digestPath); err == nil {
		t.Error("digest file still exists after drain")
	}
}

// --- readDaemonPID tests ---

func TestReadDaemonPID_NoLock(t *testing.T) {
	tmp := t.TempDir()
	if pid := readDaemonPID(tmp); pid != 0 {
		t.Errorf("readDaemonPID = %d, want 0", pid)
	}
}

func TestReadDaemonPID_ValidLock(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".lock"), []byte("12345\t2024-01-01T00:00:00Z\n"), 0644)

	if pid := readDaemonPID(tmp); pid != 12345 {
		t.Errorf("readDaemonPID = %d, want 12345", pid)
	}
}

// --- Daemon stop clears lock + flag tests ---

func TestReturn_IdempotentCleanState(t *testing.T) {
	tmp := t.TempDir()

	// Return on a completely clean home.
	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("first Return: %v", err)
	}
	if report.HasActionable() {
		t.Fatal("first Return: HasActionable() = true, want false")
	}

	// Captain Return should also be clean.
	report, err = Return(tmp)
	if err != nil {
		t.Fatalf("captain Return: %v", err)
	}
	if report.HasActionable() {
		t.Fatal("captain Return: HasActionable() = true, want false")
	}
}

func TestReturnReport_HasActionable(t *testing.T) {
	tests := []struct {
		name     string
		report   *ReturnReport
		expected bool
	}{
		{"empty", &ReturnReport{}, false},
		{"escalations", &ReturnReport{Escalations: []string{"[failure] t1"}}, true},
		{"wedge", &ReturnReport{WedgeAlarms: []string{"stale beat"}}, true},
		{"blocked", &ReturnReport{BlockedItems: []string{"missing auth"}}, true},
		{"digest count only", &ReturnReport{DigestedCount: 5}, false},
		{"all", &ReturnReport{Escalations: []string{"e1"}, WedgeAlarms: []string{"w1"}, BlockedItems: []string{"b1"}, DigestedCount: 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.report.HasActionable()
			if got != tt.expected {
				t.Errorf("HasActionable() = %v, want %v", got, tt.expected)
			}
		})
	}
}
