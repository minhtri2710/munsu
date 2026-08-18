//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// setHomeEnv sets MUNSU_HOME for the duration of a test.
func setHomeEnv(t *testing.T, path string) {
	t.Helper()
	os.Setenv("MUNSU_HOME", path)
	t.Cleanup(func() { os.Unsetenv("MUNSU_HOME") })
}

// seedCanonicalPhase creates one canonical task at the given phase through
// the concrete Task Authority (ADR-0008), never legacy backlog state.
func seedCanonicalPhase(t *testing.T, homeDir, taskID string, phase taskauthority.Phase) {
	t.Helper()
	auth := canonicalAtHome(t, homeDir)
	tid := mustTaskID(t, taskID)
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "general", Description: "work", Kind: "ship", Reason: "test",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-create-"+taskID), createReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, createReq); err != nil {
		t.Fatal(err)
	}
	switch phase {
	case taskauthority.PhaseBlocked:
		blockReq := taskauthority.CanonicalBlockRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Detail: "dep", Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-block-"+taskID), blockReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Block(op, blockReq); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseWorking:
		startReq := taskauthority.CanonicalStartRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-start-"+taskID), startReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Start(op, startReq); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseDone:
		completeReq := taskauthority.CanonicalCompleteRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), To: taskauthority.PhaseDone, Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-done-"+taskID), completeReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Complete(op, completeReq); err != nil {
			t.Fatal(err)
		}
	default:
		// queued stays as created
	}
}

// aliveProbe is a test endpoint probe that always reports the pane alive.
type aliveProbe struct{}

func (aliveProbe) Probe(string, map[string]string) (bool, error) { return true, nil }

// deadProbe is a test endpoint probe that always reports the pane dead.
type deadProbe struct{}

func (deadProbe) Probe(string, map[string]string) (bool, error) { return false, nil }

// TestRead_MissingCanonicalFailsClosed proves a task without a canonical Task
// Authority record is an operation error with task/home context, never a
// soft/projection fallback (clean break).
func TestRead_MissingCanonicalFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)

	_, err := ReadWithProbe(tmp, "does-not-exist", nil)
	if err == nil {
		t.Fatal("ReadSoldierState over a missing canonical record = nil, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") || !strings.Contains(err.Error(), tmp) {
		t.Errorf("error must carry task and home context, got: %v", err)
	}
}

// TestRead_MetaOnlyTaskFailsClosed proves a task-facing .meta without a
// canonical record is not observed as lifecycle state: it fails closed.
func TestRead_MetaOnlyTaskFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	if err := home.WriteMeta(tmp, "meta-only", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, "meta-only", "done: completed without canonical record"); err != nil {
		t.Fatal(err)
	}

	_, err := ReadWithProbe(tmp, "meta-only", nil)
	if err == nil {
		t.Fatal("meta-only task observation = nil, want fail-closed")
	}
	if !strings.Contains(err.Error(), "meta-only") || !strings.Contains(err.Error(), tmp) {
		t.Errorf("error must carry task and home context, got: %v", err)
	}
}

// TestRead_CanonicalPhaseWinsOverStaleStatus proves the canonical phase is the
// only lifecycle authority: a stale .status tail cannot change it.
func TestRead_CanonicalPhaseWinsOverStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-done", taskauthority.PhaseDone)
	if err := home.AppendStatus(tmp, "canonical-done", "working: stale work"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadWithProbe(tmp, "canonical-done", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != string(taskauthority.PhaseDone) {
		t.Errorf("status = %q, want %q (canonical overrides stale working)", s.Status, taskauthority.PhaseDone)
	}
	if !s.StatusLogSuperseded {
		t.Error("StatusLogSuperseded should be true (canonical supersedes the log)")
	}
}

// TestRead_CanonicalWithoutMetaSucceeds proves a canonical task with no .meta
// projection is still observed from its authoritative phase.
func TestRead_CanonicalWithoutMetaSucceeds(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "no-meta", taskauthority.PhaseWorking)

	s, err := ReadWithProbe(tmp, "no-meta", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != string(taskauthority.PhaseWorking) {
		t.Errorf("status = %q, want %q", s.Status, taskauthority.PhaseWorking)
	}
}

// TestRead_CanonicalWorkingUnaffectedByDeadProbe proves endpoint liveness is
// diagnostic only and never changes the canonical lifecycle phase.
func TestRead_CanonicalWorkingUnaffectedByDeadProbe(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "working", taskauthority.PhaseWorking)
	if err := home.WriteMeta(tmp, "working", map[string]string{"window": "@win"}); err != nil {
		t.Fatal(err)
	}

	s, err := ReadWithProbe(tmp, "working", deadProbe{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != string(taskauthority.PhaseWorking) {
		t.Errorf("status = %q, want %q (dead probe is diagnostic, never lifecycle)", s.Status, taskauthority.PhaseWorking)
	}
	if s.PaneAlive {
		t.Error("PaneAlive = true, want false (diagnostic)")
	}
}

// TestRead_CanonicalQueuedWhenUnknown proves a canonical queued phase is
// surfaced when no higher work state exists.
func TestRead_CanonicalQueuedWhenUnknown(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-queued", taskauthority.PhaseQueued)

	s, err := ReadWithProbe(tmp, "canonical-queued", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != string(taskauthority.PhaseQueued) {
		t.Errorf("status = %q, want %q", s.Status, taskauthority.PhaseQueued)
	}
}

// TestRead_CorruptCanonicalFailsClosed proves a malformed/corrupt canonical
// record is an operation error with task/home context.
func TestRead_CorruptCanonicalFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "corrupt", taskauthority.PhaseQueued)
	// Corrupt the canonical current.json so reads fail closed.
	cur := filepath.Join(tmp, "state", "task-authority", "tasks", "corrupt", "current.json")
	if err := os.WriteFile(cur, []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadWithProbe(tmp, "corrupt", nil)
	if err == nil {
		t.Fatal("ReadSoldierState over a corrupt canonical record = nil, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "corrupt") || !strings.Contains(err.Error(), tmp) {
		t.Errorf("error must carry task and home context, got: %v", err)
	}
}

// TestCheckNoMistakesRun_BranchMismatch proves checkNoMistakesRun safely
// returns false for a branch mismatch (it never panics).
func TestCheckNoMistakesRun_BranchMismatch(t *testing.T) {
	step, outcome, ok := checkNoMistakesRun("/tmp", "other-branch")
	if ok {
		t.Error("expected false for branch mismatch, but got ok=true")
	}
	_ = step
	_ = outcome
}
