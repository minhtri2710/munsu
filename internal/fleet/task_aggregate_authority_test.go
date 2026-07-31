package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestSoldierStateUsesTaskAggregateOverBacklogAndStatusProjections(t *testing.T) {
	homeDir := t.TempDir()
	writeFleetAggregateTestFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\ngeneration=7\nstate=working\nstate_detail=aggregate authority\n")
	writeFleetAggregateTestFile(t, filepath.Join(homeDir, "state", "ship-1.status"), "done: stale status\n")
	writeFleetAggregateTestFile(t, filepath.Join(homeDir, "data", "backlog.md"), "# Backlog\n\n- [x] ship-1 - legacy\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}

	state, err := ReadSoldierState(homeDir, "ship-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "working" || state.Description != "aggregate authority" || !state.StatusLogSuperseded {
		t.Fatalf("state = %+v", state)
	}
}

func TestSnapshotUsesTaskAggregateCurrentStateProjection(t *testing.T) {
	homeDir := t.TempDir()
	writeFleetAggregateTestFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\ngeneration=7\nkind=ship\nstate=working\nstate_detail=aggregate authority\n")
	writeFleetAggregateTestFile(t, filepath.Join(homeDir, "state", "ship-1.status"), "done: stale status\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].CurrentState != "working" || snap.Tasks[0].CurrentDescription != "aggregate authority" || !snap.Tasks[0].StatusLogSuperseded {
		t.Fatalf("snapshot = %+v", snap.Tasks)
	}
}

func writeFleetAggregateTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
