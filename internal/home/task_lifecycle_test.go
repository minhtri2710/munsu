package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestQueryTaskReadinessIsPureAndReportsDistinctReasons(t *testing.T) {
	homeDir := t.TempDir()
	queued, err := CreateTaskAggregate(homeDir, "queued", "owner", "queued work", "ship", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTaskAggregate(homeDir, "blocked", "owner", "blocked work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "blocked", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}

	beforeAggregate := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateRelPath(queued.TaskID, queued.Generation)))
	beforeCurrent := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateDir, queued.TaskID, taskCurrentFile))

	ready, err := QueryTaskReadiness(homeDir, "queued")
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || len(ready.BlockingReasons) != 0 {
		t.Fatalf("queued readiness = %+v", ready)
	}
	blocked, err := QueryTaskReadiness(homeDir, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Ready || len(blocked.BlockingReasons) != 1 || blocked.BlockingReasons[0] != ReadinessBlocked {
		t.Fatalf("blocked readiness = %+v", blocked)
	}

	if got := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateRelPath(queued.TaskID, queued.Generation))); got != beforeAggregate {
		t.Fatal("readiness query changed aggregate")
	}
	if got := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateDir, queued.TaskID, taskCurrentFile)); got != beforeCurrent {
		t.Fatal("readiness query changed current pointer")
	}
}

func TestQueryTaskReadinessReportsEachBlockingReason(t *testing.T) {
	homeDir := t.TempDir()
	cases := []struct {
		id    string
		state string
		want  ReadinessReason
	}{
		{"blocked", "blocked", ReadinessBlocked},
		{"working", "working", ReadinessInFlight},
		{"done", "done", ReadinessTerminal},
	}
	for _, tc := range cases {
		if _, err := CreateTaskAggregate(homeDir, tc.id, "owner", tc.id, "ship", ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := UpdateCurrentTaskAggregateState(homeDir, tc.id, tc.state, "reason"); err != nil {
			t.Fatal(err)
		}
		readiness, err := QueryTaskReadiness(homeDir, tc.id)
		if err != nil || len(readiness.BlockingReasons) != 1 || readiness.BlockingReasons[0] != tc.want {
			t.Fatalf("%s readiness = %+v err=%v", tc.id, readiness, err)
		}
	}
	missing, err := QueryTaskReadiness(homeDir, "missing")
	if err != nil || len(missing.BlockingReasons) != 1 || missing.BlockingReasons[0] != ReadinessReason("not-found") {
		t.Fatalf("missing readiness = %+v err=%v", missing, err)
	}
}

func TestTaskLifecycleOperationsHaveDistinctAtomicPreconditions(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}

	started, err := StartTask(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if started.State != "working" {
		t.Fatalf("started aggregate = %+v", started)
	}
	if _, err := StartTask(homeDir, "task"); !errors.Is(err, ErrTaskLifecyclePrecondition) {
		t.Fatalf("second start error = %v, want lifecycle precondition", err)
	}

	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "task", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}
	if _, err := UnblockTask(homeDir, "task"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := ReadCurrentTaskAggregate(homeDir, "task"); err != nil || got.State != "queued" {
		t.Fatalf("unblocked aggregate = %+v err=%v", got, err)
	}
	if _, err := UnblockTask(homeDir, "task"); !errors.Is(err, ErrTaskLifecyclePrecondition) {
		t.Fatalf("unblock queued error = %v, want lifecycle precondition", err)
	}
}

func TestReopenCreatesNewGenerationAndPreservesTerminalHistory(t *testing.T) {
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

	reopened, err := ReopenTask(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Generation != "2" || !reopened.Current || reopened.State != "queued" {
		t.Fatalf("reopened aggregate = %+v", reopened)
	}
	old, err := ReadTaskAggregate(homeDir, "task", "1")
	if err != nil {
		t.Fatal(err)
	}
	if old.Generation != original.Generation || old.State != "done" || old.StateDetail != "merged" || old.Definition != original.Definition || old.Current {
		t.Fatalf("historical aggregate = %+v", old)
	}
	if current, ok, err := ReadCurrentTaskAggregate(homeDir, "task"); err != nil || !ok || current.Generation != "2" {
		t.Fatalf("current aggregate = %+v ok=%v err=%v", current, ok, err)
	}
}

func TestConcurrentAggregateMutationsRemainValid(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 3)
	go func() { _, _ = StartTask(homeDir, "task"); done <- struct{}{} }()
	go func() {
		_, _, _ = UpdateCurrentTaskAggregateState(homeDir, "task", "blocked", "dependency")
		done <- struct{}{}
	}()
	go func() { _, _ = UnblockTask(homeDir, "task"); done <- struct{}{} }()
	for range 3 {
		<-done
	}
	current, ok, err := ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok || (current.State != "working" && current.State != "blocked" && current.State != "queued") {
		t.Fatalf("concurrent result = %+v ok=%v err=%v", current, ok, err)
	}
}

func readLifecycleFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
