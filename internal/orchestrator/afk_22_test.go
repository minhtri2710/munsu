package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
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
		key     string
		payload string
		want    EscalationType
	}{
		{"t1", "PR merged", EscalationReviewReady},
		{"t1", "needs-decision: which branch", EscalationDecision},
		{"t1", "blocked: waiting for CI", EscalationDecision},
		{"t1", "failed: build broken", EscalationFailure},
		{"t1", "t1: " + domain.ReportStatusLine("failed", "lint red", "decision-log"), EscalationFailure},
		{"t1", "t1: " + domain.ReportStatusLine("needs-decision", "lint red", "decision-log"), EscalationDecision},
		{"t1", "credential expired", EscalationCredential},
		{"t1", "auth token invalid", EscalationCredential},
		{"t1", "checks green", EscalationReviewReady},
		{"t1", "all green", EscalationRoutine},
		{"t1", "working: in progress", EscalationRoutine},
	}
	for _, tc := range tests {
		got := classifyEscalationType(tc.key, tc.payload)
		if got != tc.want {
			t.Errorf("classifyEscalationType(%q, %q) = %v, want %v", tc.key, tc.payload, got, tc.want)
		}
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

func TestClearStaleArtifacts_RemovesDigest(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), []byte("{}"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	for _, name := range []string{".afk-digest"} {
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
