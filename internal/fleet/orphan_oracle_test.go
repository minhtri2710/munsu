package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
)

// canonicalHomeWithTask initializes a canonical home and creates one task in
// it, so the L1 oracle resolves against real task authority state rather than
// a fake. It returns the canonical scan home.
func canonicalHomeWithTask(t *testing.T, taskID string) (string, *tauth.Canonical) {
	t.Helper()
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	auth, err := tauth.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	if taskID != "" {
		canonicalCreateTask(t, auth, taskID, "ship", "")
	}
	scanHome, err := canonicalHome(homeDir)
	if err != nil {
		t.Fatalf("canonicalHome: %v", err)
	}
	return scanHome, auth
}

func retireTask(t *testing.T, auth *tauth.Canonical, taskID string) {
	t.Helper()
	current, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("Get(%s): %v", taskID, err)
	}
	req := tauth.CanonicalRetireRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Reason: "orphan oracle test",
		Precondition: domain.Precondition{Generation: uint64(current.Generation), Revision: uint64(current.Revision)},
	}
	op, err := domain.NewOperation(mustOpID(t, "op-retire-"+taskID), req)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if _, err := auth.Retire(op, req); err != nil {
		t.Fatalf("Retire(%s): %v", taskID, err)
	}
}

func markedMunsuProcess(taskID, munsuHome string) MarkedProcess {
	return MarkedProcess{PID: 4242, Markers: map[string]string{MarkerMunsuTask: taskID, MarkerMunsuHome: munsuHome}}
}

// TestMunsuTaskOracleResolvesAgainstTaskAuthority covers every branch of the
// L1 oracle against real canonical state. The branch that matters most is the
// last one: a task with no record is UNRESOLVED, not ended. Both oracles reach
// GARBAGE only from positive evidence, so a live soldier whose record has not
// committed yet is never handed to a member as a leftover.
func TestMunsuTaskOracleResolvesAgainstTaskAuthority(t *testing.T) {
	t.Run("munsu home outside the scanned home is unresolved", func(t *testing.T) {
		scanHome, _ := canonicalHomeWithTask(t, "task-active")
		liveness, reason := OSRunOracle{}.RunLiveness(scanHome, markedMunsuProcess("task-active", t.TempDir()))
		if liveness != RunUnresolved {
			t.Fatalf("expected RunUnresolved for a foreign home, got %v (%s)", liveness, reason)
		}
	})

	t.Run("unreadable task authority is unresolved", func(t *testing.T) {
		// A directory that was never initialized as a canonical home: the
		// authority read fails closed and the oracle declines to conclude.
		plain := t.TempDir()
		scanHome, err := canonicalHome(plain)
		if err != nil {
			t.Fatalf("canonicalHome: %v", err)
		}
		liveness, reason := OSRunOracle{}.RunLiveness(scanHome, markedMunsuProcess("task-active", plain))
		if liveness != RunUnresolved {
			t.Fatalf("expected RunUnresolved when the authority cannot be read, got %v (%s)", liveness, reason)
		}
	})

	t.Run("live task is owned", func(t *testing.T) {
		scanHome, _ := canonicalHomeWithTask(t, "task-active")
		liveness, reason := OSRunOracle{}.RunLiveness(scanHome, markedMunsuProcess("task-active", scanHome))
		if liveness != RunAlive {
			t.Fatalf("expected RunAlive for a task that is still current, got %v (%s)", liveness, reason)
		}
	})

	t.Run("retired task is garbage", func(t *testing.T) {
		scanHome, auth := canonicalHomeWithTask(t, "task-retired")
		retireTask(t, auth, "task-retired")
		liveness, reason := OSRunOracle{}.RunLiveness(scanHome, markedMunsuProcess("task-retired", scanHome))
		if liveness != RunEnded {
			t.Fatalf("expected RunEnded for a retired task, got %v (%s)", liveness, reason)
		}
	})

	t.Run("task with no record is unresolved, never ended", func(t *testing.T) {
		scanHome, _ := canonicalHomeWithTask(t, "")
		liveness, reason := OSRunOracle{}.RunLiveness(scanHome, markedMunsuProcess("task-never-created", scanHome))
		if liveness == RunEnded {
			t.Fatalf("absence of a record must never be read as evidence the run ended (%s)", reason)
		}
		if liveness != RunUnresolved {
			t.Fatalf("expected RunUnresolved for a task with no record, got %v (%s)", liveness, reason)
		}
	})
}

// TestMunsuTaskOracleClassifiesAbsentRecordAsUnknown pins the same decision at
// the level a member reads: the report group, not the oracle enum.
func TestMunsuTaskOracleClassifiesAbsentRecordAsUnknown(t *testing.T) {
	scanHome, _ := canonicalHomeWithTask(t, "")
	fence := CompositeWriterFence{
		Marked: fakeMarkerInventory{scan: MarkerScan{Total: 1, Marked: []MarkedProcess{markedMunsuProcess("task-never-created", scanHome)}}},
		Oracle: OSRunOracle{},
	}
	report, err := fence.InspectOrphans(scanHome)
	if err != nil {
		t.Fatalf("InspectOrphans: %v", err)
	}
	if len(report.Garbage) != 0 {
		t.Fatalf("a task with no record must not reach the GARBAGE group, got %+v", report.Garbage)
	}
	if len(report.Unknown) != 1 || report.Unknown[0].Layer != LayerMunsuTask {
		t.Fatalf("expected the process in UNKNOWN as an L1 entry, got %+v", report.Unknown)
	}
}
