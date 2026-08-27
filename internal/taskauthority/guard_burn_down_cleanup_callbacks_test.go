package taskauthority

import "testing"

// The fenced-cleanup entry points refuse before they take the task scope: a
// missing callback and an invalid terminal status are caller errors, and taking
// a lock to discover one would hold the task fence for a refusal that never
// needed it. Each case asserts the message of the branch under test, because
// these functions refuse several ways with a bare error and `err != nil` would
// stay green with the guard deleted.

func TestReconcileRetirementCleanupRequiresWork(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := mustTaskID(t, "cleanup-callbacks")
	gen := mustCreate(t, c, id.Value()).Generation
	ok := func() error { return nil }

	// Control: the callback present reaches the fence and refuses for state,
	// not for the callback, so the refusal below is attributable.
	err := c.ReconcileRetirementCleanup(id, gen, CleanupCompleted, ok)
	wantErrSubstring(t, err, "cleanup claim is not active", "callback present on a task with no claim")

	wantErrSubstring(t, c.ReconcileRetirementCleanup(id, gen, CleanupCompleted, nil),
		"cleanup callback is required", "nil work")
}

func TestReconcileRetirementCleanupRejectsNonTerminalStatus(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := mustTaskID(t, "cleanup-terminal")
	gen := mustCreate(t, c, id.Value()).Generation
	ok := func() error { return nil }

	// CleanupActive is a real status, and the one a caller would reach for to
	// mean "resume": reconciliation commits terminal state only.
	wantErrSubstring(t, c.ReconcileRetirementCleanup(id, gen, CleanupActive, ok),
		"invalid cleanup terminal status", "reconciling to an active claim")
}

func TestReconcileCompletedCleanupRequiresWork(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := mustTaskID(t, "completed-callback")
	gen := mustCreate(t, c, id.Value()).Generation

	_, err := c.ReconcileCompletedCleanup(id, gen, nil)
	wantErrSubstring(t, err, "cleanup callback is nil", "nil completed-cleanup work")
}

func TestReconcileCompletedCleanupRefusesUnretiredGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := mustTaskID(t, "completed-fence")
	gen := mustCreate(t, c, id.Value()).Generation

	// Control: the task exists at this exact generation and is current, so the
	// refusal below is attributable to the phase alone.
	if _, err := c.Get(id); err != nil {
		t.Fatalf("control Get: %v", err)
	}
	called := false
	_, err := c.ReconcileCompletedCleanup(id, gen, func() error { called = true; return nil })
	wantErrSubstring(t, err, "cleanup is not completed", "repairing projections for a task that never retired")
	if called {
		t.Fatal("projection work ran for a task whose cleanup never completed")
	}
}

func TestWriteTaskDataArtifactRequiresWriter(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	wantErrSubstring(t, c.WriteTaskDataArtifactByID("write-callback", nil),
		"task-data callback is nil", "nil task-data writer")
}

func TestReclaimReleasedTaskArtifactsRequiresReclaimer(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	_, err := c.ReclaimReleasedTaskArtifactsByID("reclaim-callback", nil)
	wantErrSubstring(t, err, "reclaim callback is nil", "nil reclaimer")
}
