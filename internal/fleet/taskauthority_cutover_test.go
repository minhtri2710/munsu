//go:build integration

package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// canonicalHomeAuthority opens the canonical home and constructs the canonical
// Task Authority over it, so canonical-read tests seed records the read path
// finds. The home must be initialized first.
func canonicalHomeAuthority(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// taPrecondition is domain.Of.
func taPrecondition(gen, rev uint64) domain.Precondition { return domain.Of(gen, rev) }

// seedCanonicalShipTask creates one ship task through the canonical surface
// and advances it to the requested phase.
func seedCanonicalShipTask(t *testing.T, homeDir, id, phase string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	auth := canonicalHomeAuthority(t, homeDir)
	create := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      mustTaskID(t, id),
		Owner:       "owner",
		Description: "work on " + id,
		Kind:        "ship",
		Project:     mustProjectID(t, "proj-x"),
		Reason:      "test",
	}
	if _, err := auth.Create(mustOp(t, "op-create-"+id, create), create); err != nil {
		t.Fatal(err)
	}
	if phase == "working" || phase == "done" {
		start := taskauthority.CanonicalStartRequest{
			HomeID:       auth.HomeID(),
			TaskID:       mustTaskID(t, id),
			Precondition: taPrecondition(1, 1),
			Reason:       "spawned",
		}
		if _, err := auth.Start(mustOp(t, "op-start-"+id, start), start); err != nil {
			t.Fatal(err)
		}
	}
	if phase == "done" {
		complete := taskauthority.CanonicalCompleteRequest{
			HomeID:       auth.HomeID(),
			TaskID:       mustTaskID(t, id),
			Precondition: taPrecondition(1, 2),
			To:           taskauthority.PhaseDone,
			Reason:       "done",
		}
		if _, err := auth.Complete(mustOp(t, "op-complete-"+id, complete), complete); err != nil {
			t.Fatal(err)
		}
	}
	return auth
}

// TestSnapshotIncludesQueuedCanonicalTasks proves a queued canonical task is
// part of the fleet snapshot.
func TestSnapshotIncludesQueuedCanonicalTasks(t *testing.T) {
	homeDir := t.TempDir()
	seedCanonicalShipTask(t, homeDir, "no-meta", "queued")
	snap, err := Snapshot(homeDir, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.ID != "no-meta" || ts.Kind != "ship" || ts.CurrentState != "queued" {
		t.Fatalf("task = %+v", ts)
	}
}

// TestReadWithProbeFailsClosedWithoutCanonicalRecord proves observation fails
// closed on a task with no canonical record.
func TestReadWithProbeFailsClosedWithoutCanonicalRecord(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	_, err := ReadWithProbe(homeDir, "t1", nil)
	if err == nil {
		t.Fatal("ReadSoldierState over a task with no canonical record = nil, want fail-closed")
	}
	if !testutil.PathInMessage(err.Error(), homeDir) {
		t.Errorf("clean-break error must carry home context, got: %v", err)
	}
}

// TestSummarizeCaptainHomePrefersCanonicalPhase proves the captain-home
// summary prefers canonical Authority state: a stale status line showing
// working cannot classify a canonically done child as active.
func TestSummarizeCaptainHomePrefersCanonicalPhase(t *testing.T) {
	homeDir := t.TempDir()
	// Initialize the canonical home, then a canonically done child with a
	// stale working status line: the authoritative phase wins.
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	seedChild(t, homeDir, "t1", true)
	if err := home.AppendStatus(homeDir, "t1", "working: implementing"); err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCaptainHome(homeDir)
	if sum.Counts.ActiveChildren != 0 {
		t.Fatalf("ActiveChildren = %d, want 0 (canonical done supersedes stale working status)", sum.Counts.ActiveChildren)
	}
	if sum.State == "active_child_work" {
		t.Fatalf("state = %q, want non-active (canonical phase wins)", sum.State)
	}
}
