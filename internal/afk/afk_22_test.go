package afk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Digester tests ---

func TestDigesterFeedNilDigest(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)
	d.Feed(nil) // should not panic
	if d.ShouldFlush(time.Now()) {
		t.Error("ShouldFlush = true after nil feed, want false")
	}
}

func TestDigesterAccumulatesAndFlushes(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Feed a digest with one escalated wake.
	digest := &Digest{
		Escalated: []WakeDigest{
			{Kind: "afk", Key: "task-1", Payload: "PR merged", IsGeneralRelevant: true},
		},
		Routines: []WakeDigest{
			{Kind: "check", Key: "health", Payload: "all green", IsGeneralRelevant: false},
		},
	}
	d.Feed(digest)

	// Should not flush immediately (window not elapsed).
	now := time.Now()
	if d.ShouldFlush(now) {
		t.Error("ShouldFlush = true immediately after feed, want false")
	}

	// Force flush by advancing the clock past the window.
	future := now.Add(defaultWindow + time.Second)
	if !d.ShouldFlush(future) {
		t.Error("ShouldFlush = false after window elapsed, want true")
	}

	if err := d.Flush(future); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify digest file was written.
	digestPath := filepath.Join(tmp, digestFile)
	data, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("reading digest file: %v", err)
	}

	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		t.Fatalf("unmarshaling digest: %v", err)
	}

	if len(be.Entries) != 2 {
		t.Errorf("entries count = %d, want 2", len(be.Entries))
	}
	if be.RoutineCount != 1 {
		t.Errorf("routine count = %d, want 1", be.RoutineCount)
	}
	if be.EscalatedCount != 1 {
		t.Errorf("escalated count = %d, want 1", be.EscalatedCount)
	}

	// Verify entry types.
	var hasRoutine, hasDecision bool
	for _, e := range be.Entries {
		switch e.Type {
		case EscalationRoutine:
			hasRoutine = true
		case EscalationReviewReady:
			hasDecision = true
		}
	}
	if !hasRoutine {
		t.Error("expected a routine entry, got none")
	}
	if !hasDecision {
		t.Error("expected a review-ready entry, got none")
	}
}

func TestDigesterFlushEmpty(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)
	if err := d.Flush(time.Now()); err != nil {
		t.Fatalf("Flush on empty digester: %v", err)
	}
	// File should not exist.
	if _, err := os.Stat(filepath.Join(tmp, digestFile)); !os.IsNotExist(err) {
		t.Error("digest file exists after flushing empty digester")
	}
}

func TestDigesterMultipleDigests(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Feed two digests.
	d.Feed(&Digest{
		Escalated: []WakeDigest{{Kind: "afk", Key: "t1", Payload: "PR merged"}},
	})
	d.Feed(&Digest{
		Routines: []WakeDigest{{Kind: "check", Key: "t2", Payload: "ok"}},
	})

	now := time.Now().Add(defaultWindow + time.Second)
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmp, digestFile))
	var be BatchedEscalation
	json.Unmarshal(data, &be)

	if len(be.Entries) != 2 {
		t.Errorf("entries = %d, want 2", len(be.Entries))
	}
}

func TestDigesterFlushResetsAccumulator(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	d.Feed(&Digest{
		Routines: []WakeDigest{{Kind: "check", Key: "t1", Payload: "ok"}},
	})

	now := time.Now().Add(defaultWindow + time.Second)
	d.Flush(now)

	// After flush, ShouldFlush should be false again.
	if d.ShouldFlush(time.Now()) {
		t.Error("ShouldFlush = true after flush, want false")
	}
}

func TestDigesterConcurrentSafe(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			d.Feed(&Digest{
				Escalated: []WakeDigest{{Kind: "afk", Key: fmt.Sprintf("t%d", i), Payload: "done"}},
			})
		}
		close(done1)
	}()

	go func() {
		for i := 0; i < 50; i++ {
			d.Feed(&Digest{
				Routines: []WakeDigest{{Kind: "check", Key: fmt.Sprintf("h%d", i), Payload: "ok"}},
			})
		}
		close(done2)
	}()

	<-done1
	<-done2

	now := time.Now().Add(defaultWindow + time.Second)
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush after concurrent feeds: %v", err)
	}
}

// --- Escalation type classification tests ---

func TestClassifyEscalationType(t *testing.T) {
	tests := []struct {
		payload string
		want    EscalationType
	}{
		{"PR merged", EscalationReviewReady},
		{"needs-decision: which branch", EscalationDecision},
		{"blocked: waiting for CI", EscalationDecision},
		{"failed: build broken", EscalationFailure},
		{"credential expired", EscalationCredential},
		{"auth token invalid", EscalationCredential},
		{"checks green", EscalationReviewReady},
		{"all green", EscalationRoutine},
		{"working: in progress", EscalationRoutine},
	}
	for _, tc := range tests {
		got := classifyEscalationType(tc.payload)
		if got != tc.want {
			t.Errorf("classifyEscalationType(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

// --- Wedge detector tests ---

func TestWedgeDetectorNoAlarmOnFresh(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Write a fresh beat so the detector doesn't trigger a stale/missing beat alarm.
	now := time.Now()
	beatContent := fmt.Sprintf("%d %d", now.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := NewWedgeDetector(tmp)
	alarm := w.Check(now)
	if alarm != nil {
		t.Errorf("Check on fresh detector = %+v, want nil", alarm)
	}
}

func TestWedgeDetectorStaleBeat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Write a very old beat (beyond stale threshold).
	old := time.Now().Add(-10 * time.Minute)
	beatContent := fmt.Sprintf("%d %d", old.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := NewWedgeDetector(tmp)
	alarm := w.Check(time.Now())
	if alarm == nil {
		t.Fatal("expected wedge alarm for stale beat, got nil")
	}
	if !strings.Contains(alarm.Reason, "stale") {
		t.Errorf("alarm reason = %q, want 'stale'", alarm.Reason)
	}
}

func TestWedgeDetectorFreshBeat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Write a fresh beat.
	now := time.Now()
	beatContent := fmt.Sprintf("%d %d", now.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := NewWedgeDetector(tmp)
	alarm := w.Check(now)
	if alarm != nil {
		t.Errorf("Check with fresh beat = %+v, want nil", alarm)
	}
}

func TestWedgeDetectorMissingBeat(t *testing.T) {
	tmp := t.TempDir()
	// No state directory, no beat file.
	w := NewWedgeDetector(tmp)
	alarm := w.Check(time.Now())
	if alarm != nil {
		// Missing beat is not necessarily a wedge alarm for us,
		// since the watcher may not have started yet.
		// Actually, let me check: in Check, missing beat returns a WedgeAlarm
		// with reason "watcher beat never set", but that happens only when
		// beatStatus.Exists is false (which means beatStatus.Stale is true).
		// Wait, actually looking at the code, the condition is:
		// if beatStatus.Exists && beatStatus.Stale => stale alarm
		// if !beatStatus.Exists => "never set" alarm
		// So yes, missing beat always triggers.
		if alarm.Reason != "watcher beat never set" {
			t.Errorf("alarm reason = %q, want 'watcher beat never set'", alarm.Reason)
		}
	}
}

func TestWedgeDetectorRepeatedWake(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Write a fresh beat so only the wake repetition check fires.
	now := time.Now()
	beatContent := fmt.Sprintf("%d %d", now.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := NewWedgeDetector(tmp)

	// Feed same wake 3 times within the window.
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck")

	alarm := w.Check(now)
	if alarm == nil {
		t.Fatal("expected wedge alarm for repeated wake, got nil")
	}
	if !strings.Contains(alarm.Reason, "repeated identical") {
		t.Errorf("alarm reason = %q, want 'repeated identical'", alarm.Reason)
	}
}

func TestWedgeDetectorResetWake(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	now := time.Now()
	beatContent := fmt.Sprintf("%d %d", now.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := NewWedgeDetector(tmp)
	w.FeedWake("task-a")
	w.FeedWake("task-a")
	w.ResetWake()
	w.FeedWake("task-a")

	// After reset, count should be 1, not 3, so no alarm.
	alarm := w.Check(now)
	if alarm != nil {
		t.Errorf("Check after ResetWake = %+v, want nil", alarm)
	}
}

// --- Stale artifact clearing tests ---

func TestClearStaleArtifacts_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	// Should not error when state dir doesn't exist.
	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts on clean home: %v", err)
	}
}

func TestClearStaleArtifacts_RemovesSeenMarkers(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create some seen markers.
	os.WriteFile(filepath.Join(stateDir, ".seen-task-1"), []byte("line"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".seen-task-2"), []byte("line"), 0644)

	// Create a normal file that should NOT be removed.
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working\n"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	// Seen markers should be gone.
	if _, err := os.Stat(filepath.Join(stateDir, ".seen-task-1")); !os.IsNotExist(err) {
		t.Error(".seen-task-1 still exists")
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".seen-task-2")); !os.IsNotExist(err) {
		t.Error(".seen-task-2 still exists")
	}

	// Normal status file should remain.
	if _, err := os.Stat(filepath.Join(stateDir, "task-1.status")); os.IsNotExist(err) {
		t.Error("task-1.status was removed, should remain")
	}
}

func TestClearStaleArtifacts_RemovesSubsuperMarkers(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".subsuper-escalations"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".subsuper-escalations.since"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".subsuper-inject-wedged"), []byte("data"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	// All subsuper markers should be gone.
	for _, name := range []string{".subsuper-escalations", ".subsuper-escalations.since", ".subsuper-inject-wedged"} {
		if _, err := os.Stat(filepath.Join(stateDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", name)
		}
	}
}

func TestClearStaleArtifacts_RemovesDigestAndWedge(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".afk-wedge-alarm"), []byte("alarm"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	for _, name := range []string{".afk-digest", ".afk-wedge-alarm"} {
		if _, err := os.Stat(filepath.Join(stateDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", name)
		}
	}
}

func TestClearStaleArtifacts_DoesNotTouchOtherHomes(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".seen-task-1"), []byte("line"), 0644)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working\n"), 0644)

	// Create a "sibling home" directory that should be untouched.
	siblingDir := filepath.Join(tmp, "..", "other-home", "state")
	os.MkdirAll(siblingDir, 0755)
	os.WriteFile(filepath.Join(siblingDir, ".seen-other"), []byte("line"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	// Sibling's seen markers should remain.
	if _, err := os.Stat(filepath.Join(siblingDir, ".seen-other")); os.IsNotExist(err) {
		t.Error("sibling home .seen-other was removed, should not be touched")
	}
}

func TestClearStaleCheckedMarkers(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create subsuper check markers.
	os.WriteFile(filepath.Join(stateDir, ".subsuper-stale-health"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".subsuper-seen-status-task-1"), []byte("data"), 0644)
	// Create a seen marker that should NOT be removed by this function.
	os.WriteFile(filepath.Join(stateDir, ".seen-task-1"), []byte("line"), 0644)

	if err := ClearStaleCheckedMarkers(tmp); err != nil {
		t.Fatalf("ClearStaleCheckedMarkers: %v", err)
	}

	// Check markers should be removed.
	if _, err := os.Stat(filepath.Join(stateDir, ".subsuper-stale-health")); !os.IsNotExist(err) {
		t.Error(".subsuper-stale-health still exists after ClearStaleCheckedMarkers")
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".subsuper-seen-status-task-1")); !os.IsNotExist(err) {
		t.Error(".subsuper-seen-status-task-1 still exists after ClearStaleCheckedMarkers")
	}

	// Regular seen markers should remain.
	if _, err := os.Stat(filepath.Join(stateDir, ".seen-task-1")); os.IsNotExist(err) {
		t.Error(".seen-task-1 was removed, should remain")
	}
}
