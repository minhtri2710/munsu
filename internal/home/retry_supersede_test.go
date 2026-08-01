package home

import (
	"os"
	"path/filepath"
	"testing"
)

// The retry/supersede workflow must provide an explicit safe generation
// transition and prevent stale pane/worktree/status ownership. These tests are
// the RED contract: current main fails every assertion below.

// TestSupersedeTaskRefusesLiveGeneration: supersede must refuse a generation
// that still owns live work (queued/working/blocked). Only failed or terminal
// generations may be superseded.
func TestSupersedeTaskRefusesLiveGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "blocked"} {
		if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "task", state, state); err != nil {
			t.Fatal(err)
		}
		if _, err := SupersedeTask(homeDir, "task"); err == nil {
			t.Fatalf("supersede must refuse live state %q", state)
		}
	}
	// "working" requires an endpoint binding to be persisted.
	if err := BindTaskEndpoint(homeDir, "task", "1", TaskEndpointBinding{
		TaskGeneration: "1", Backend: "herdr", Handle: "w1:p1",
		LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "task", "working", "spawned"); err != nil {
		t.Fatal(err)
	}
	if _, err := SupersedeTask(homeDir, "task"); err == nil {
		t.Fatal("supersede must refuse a live working generation")
	}
}

// TestSupersedeTaskClearsStalePaneAndWorktreeOwnership: a failed or terminal
// generation may carry an endpoint (pane) and worktree binding. The new
// generation must NOT inherit them, and the current aggregate must remain
// readable (the stale-binding carry-over currently breaks validation).
func TestSupersedeTaskClearsStalePaneAndWorktreeOwnership(t *testing.T) {
	homeDir := t.TempDir()
	agg := TaskAggregate{
		SchemaVersion: taskAggregateSchema,
		TaskID:        "task",
		Generation:    "1",
		Current:       true,
		Owner:         "owner",
		Definition:    "work",
		State:         "failed",
		StateDetail:   "soldier failed",
		Endpoint: &TaskEndpointBinding{
			TaskGeneration: "1", Backend: "herdr", Handle: "w1:p1",
			LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
		},
		Worktree: &TaskWorktreeBinding{
			TaskGeneration: "1", RepositoryIdentity: "repo", Path: "/wt",
			GitDir: "/wt/.git", CommonDir: "/repo", Head: "abc",
			LeaseID: "wl1", FenceToken: "wf1", BoundAtUnix: 1000,
		},
	}
	if err := WriteTaskAggregate(homeDir, agg); err != nil {
		t.Fatal(err)
	}

	superseded, err := SupersedeTask(homeDir, "task")
	if err != nil {
		t.Fatalf("supersede failed generation: %v", err)
	}
	if superseded.Generation != "2" || superseded.State != "queued" {
		t.Fatalf("superseded aggregate = %+v, want generation 2 queued", superseded)
	}
	if superseded.Endpoint != nil || superseded.Worktree != nil {
		t.Fatalf("new generation must not carry stale pane/worktree bindings: endpoint=%+v worktree=%+v", superseded.Endpoint, superseded.Worktree)
	}
	// The current aggregate must be readable after supersede (stale bindings
	// currently fail validateTaskAggregate on read).
	cur, ok, err := ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil {
		t.Fatalf("current aggregate unreadable after supersede: %v", err)
	}
	if !ok || cur.Generation != "2" || cur.Endpoint != nil || cur.Worktree != nil {
		t.Fatalf("current = %+v ok=%v", cur, ok)
	}
	// The old generation is preserved as historical with its evidence.
	old, err := ReadTaskAggregate(homeDir, "task", "1")
	if err != nil {
		t.Fatal(err)
	}
	if old.State != "failed" || old.Current || old.Endpoint == nil || old.Worktree == nil {
		t.Fatalf("historical aggregate = %+v", old)
	}
}

// TestSupersedeTaskResetsStatusOwnership: after supersede the new generation
// must not inherit the old generation's status log lines; observation must
// start clean.
func TestSupersedeTaskResetsStatusOwnership(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus(homeDir, "task", "working: spawned"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "task", "failed", "tests failed"); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus(homeDir, "task", "failed: tests not passing"); err != nil {
		t.Fatal(err)
	}

	if _, err := SupersedeTask(homeDir, "task"); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// The status log must be reset so the old generation's lines are not
	// attributed to the new generation.
	lines, err := ReadStatus(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("status ownership not reset: old generation lines remain: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", "task.status")); err == nil {
		t.Fatal("status file should be removed after supersede")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestSupersedeTaskPreservesTerminalHistory: superseding a terminal generation
// keeps the prior generation immutable and queued starts the new one.
func TestSupersedeTaskPreservesTerminalHistory(t *testing.T) {
	homeDir := t.TempDir()
	original := TaskAggregate{
		SchemaVersion: taskAggregateSchema,
		TaskID:        "task",
		Generation:    "1",
		Current:       true,
		Owner:         "owner",
		Definition:    "completed work",
		State:         "done",
		StateDetail:   "merged",
		Kind:          "ship",
		Project:       "munsu",
	}
	if err := WriteTaskAggregate(homeDir, original); err != nil {
		t.Fatal(err)
	}
	superseded, err := SupersedeTask(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Generation != "2" || !superseded.Current || superseded.State != "queued" {
		t.Fatalf("superseded aggregate = %+v", superseded)
	}
	old, err := ReadTaskAggregate(homeDir, "task", "1")
	if err != nil {
		t.Fatal(err)
	}
	if old.Generation != "1" || old.State != "done" || old.StateDetail != "merged" || old.Current {
		t.Fatalf("historical aggregate = %+v", old)
	}
}
