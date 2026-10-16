package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Digester tests ---

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

	if err := d.Flush(time.Now()); err != nil {
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
	d.Feed(nil)
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

	now := time.Now()
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

	now := time.Now()
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

// --- Stale artifact clearing tests ---

func TestClearStaleArtifacts_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	// Should not error when state dir doesn't exist.
	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts on clean home: %v", err)
	}
}

// A prior session's digest is removed unless it holds report refs: those
// entries are the only copy of their report wakes.
func TestClearStaleArtifacts_Digest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		digest   string
		wantKept bool
	}{
		{name: "holds report refs", digest: `{"entries":[{"kind":"report","key":"t1","ref":"t1:1"}]}`, wantKept: true},
		{name: "no report refs", digest: `{"entries":[{"kind":"signal","key":"t1"}]}`, wantKept: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			path := filepath.Join(tmp, digestFile)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.digest), 0644); err != nil {
				t.Fatal(err)
			}
			if err := ClearStaleArtifacts(tmp); err != nil {
				t.Fatalf("ClearStaleArtifacts: %v", err)
			}
			if _, err := os.Stat(path); (err == nil) != tc.wantKept {
				t.Fatalf("digest kept = %v, want %v", err == nil, tc.wantKept)
			}
		})
	}
}

func TestClearStaleArtifacts_DoesNotTouchOtherHomes(t *testing.T) {
	tmp := t.TempDir()
	siblingDir := filepath.Join(tmp, "..", "other-home", "state")
	os.MkdirAll(siblingDir, 0755)
	os.WriteFile(filepath.Join(siblingDir, ".afk-digest"), []byte("{}"), 0644)

	if err := ClearStaleArtifacts(tmp); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	if _, err := os.Stat(filepath.Join(siblingDir, ".afk-digest")); os.IsNotExist(err) {
		t.Error("sibling home .afk-digest was removed, should not be touched")
	}
}
