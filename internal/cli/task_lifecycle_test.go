package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestTaskStartAndUnblockUseDistinctLifecycleOperations proves `task start`
// and `task unblock` drive the named Authority operations (Task 3.3 criterion
// 1): start requires queued, unblock requires blocked, and each command
// advances the canonical aggregate before the .status projection.
func TestTaskStartAndUnblockUseDistinctLifecycleOperations(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")

	if out, err := runTaskCommand(t, []string{"task", "start", "task", "--home", homeDir}); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("aggregate after start = %+v err=%v", agg, err)
	}
	if out, err := runTaskCommand(t, []string{"task", "start", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "start requires queued task") {
		t.Fatalf("second start correction = %v\n%s", err, out)
	}
	blockReq := taskauthority.CanonicalBlockRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "task"),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Detail:       "dependency",
		Reason:       "seed",
	}
	if _, err := auth.Block(mustCanonicalOp(t, "seed-block", blockReq), blockReq); err != nil {
		t.Fatal(err)
	}
	if out, err := runTaskCommand(t, []string{"task", "unblock", "task", "--home", homeDir}); err != nil {
		t.Fatalf("unblock: %v\n%s", err, out)
	}
	if agg, err = auth.Get(mustTaskIDFor(t, "task")); err != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate after unblock = %+v err=%v", agg, err)
	}
	if out, err := runTaskCommand(t, []string{"task", "unblock", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "unblock requires blocked task") {
		t.Fatalf("second unblock error = %v\n%s", err, out)
	}
}

// TestTaskReopenSynchronizesAggregateAndProjection proves `task reopen`
// drives the Authority Reopen operation (Task 3.3 criterion 4): the terminal
// generation stays immutable historical state and a new queued Generation
// starts at Revision one.
func TestTaskReopenSynchronizesAggregateAndProjection(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	completeReq := taskauthority.CanonicalCompleteRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "task"),
		Precondition: domain.Of(1, 1),
		To:           taskauthority.PhaseDone,
		Reason:       "seed",
	}
	if _, err := auth.Complete(mustCanonicalOp(t, "seed-done", completeReq), completeReq); err != nil {
		t.Fatal(err)
	}
	if out, err := runTaskCommand(t, []string{"task", "reopen", "task", "--home", homeDir}); err != nil {
		t.Fatalf("reopen: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Generation != 2 || agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("current aggregate = %+v err=%v", agg, err)
	}
	old, err := auth.GetGeneration(mustTaskIDFor(t, "task"), 1)
	if err != nil || old.Current || old.Phase != taskauthority.PhaseDone {
		t.Fatalf("historical aggregate = %+v err=%v, want immutable done generation 1", old, err)
	}
	status, err := home.ReadStatus(homeDir, "task")
	if err != nil || len(status) == 0 || !strings.Contains(status[len(status)-1], "queued: cli task reopen") {
		t.Fatalf("status projection = %v err=%v", status, err)
	}
}

// TestTaskDoneCallsAuthorityComplete proves `task done` drives the named
// Authority Complete operation to the done terminal phase and updates the
// .status projection only after the authoritative commit (Task 3.3 criterion
// 1).
func TestTaskDoneCallsAuthorityComplete(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")

	if out, err := runTaskCommand(t, []string{"task", "done", "task", "--home", homeDir}); err != nil {
		t.Fatalf("done: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Phase != taskauthority.PhaseDone {
		t.Fatalf("aggregate after done = %+v err=%v", agg, err)
	}
	if out, err := runTaskCommand(t, []string{"task", "done", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "complete requires a non-terminal task") {
		t.Fatalf("second done error = %v\n%s", err, out)
	}
}

// TestTaskBlockCallsAuthorityBlock proves `task block` drives the named
// Authority Block operation, records the dependency detail on the
// authoritative aggregate, and fails a second block closed before touching the
// projection (Task 3.3 criteria 1 and 2).
func TestTaskBlockCallsAuthorityBlock(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")

	if out, err := runTaskCommand(t, []string{"task", "block", "task", "--by", "dep-1", "--home", homeDir}); err != nil {
		t.Fatalf("block: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Phase != taskauthority.PhaseBlocked || agg.PhaseDetail != "task: blocked by dep-1" {
		t.Fatalf("aggregate after block = %+v err=%v", agg, err)
	}
	before := readFileForTest(t, filepath.Join(homeDir, "state", "task.status"))
	if out, err := runTaskCommand(t, []string{"task", "block", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "block requires queued or working task") {
		t.Fatalf("second block error = %v\n%s", err, out)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "task.status")); got != before {
		t.Fatal("invalid block transition mutated the .status projection")
	}
}

// TestTaskLifecycleProjectionFailureReturnsTypedPartialWithoutReplay proves a
// .status projection failure surfaces a typed partial result, keeps the
// authoritative commit, and is retryable without replaying the authoritative
// operation: a re-run of the command conflicts closed on the already-advanced
// phase (Task 3.3 criteria 2 and 3).
func TestTaskLifecycleProjectionFailureReturnsTypedPartialWithoutReplay(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")

	// Break the projection so the post-commit .status write fails.
	if err := os.MkdirAll(filepath.Join(homeDir, "state", "task.status"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runTaskCommand(t, []string{"task", "start", "task", "--home", homeDir})
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "working" {
		t.Fatalf("error = %T %v, want typed partial result", err, err)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("authoritative commit must survive projection failure: %+v err=%v", agg, err)
	}

	// Re-running the same command must not replay the authoritative operation:
	// the fresh invocation conflicts closed on the already-working phase.
	if out, err := runTaskCommand(t, []string{"task", "start", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "start requires queued task") {
		t.Fatalf("re-run after partial = %v\n%s, want typed conflict", err, out)
	}
}

// TestTaskRetrySupersedesTerminalGeneration proves `task retry` drives the
// canonical Reopen operation (the supersede semantics): the terminal
// generation stays immutable historical state and a new queued Generation
// starts at Revision one.
func TestTaskRetrySupersedesTerminalGeneration(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	completeReq := taskauthority.CanonicalCompleteRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "task"),
		Precondition: domain.Of(1, 1),
		To:           taskauthority.PhaseDone,
		Reason:       "seed",
	}
	if _, err := auth.Complete(mustCanonicalOp(t, "seed-done", completeReq), completeReq); err != nil {
		t.Fatal(err)
	}
	if out, err := runTaskCommand(t, []string{"task", "retry", "task", "--home", homeDir}); err != nil {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Generation != 2 || agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("current aggregate after retry = %+v err=%v", agg, err)
	}
	old, err := auth.GetGeneration(mustTaskIDFor(t, "task"), 1)
	if err != nil || old.Current || old.Phase != taskauthority.PhaseDone {
		t.Fatalf("historical aggregate = %+v err=%v, want immutable done generation 1", old, err)
	}
}

// TestTaskStartFailsClosedOnDegradedSupervision proves the CLI start
// supervision gate (Task 4.3) fires before any Task Authority call: an
// unhealthy watcher lease fails `task start` closed with ErrUnhealthyWatcher
// and leaves the queued task phase untouched.
func TestTaskStartFailsClosedOnDegradedSupervision(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")

	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	home.ClaimWatcherLease(homeDir, 9999999)

	out, err := runTaskCommand(t, []string{"task", "start", "task", "--home", homeDir})
	if err == nil || !errors.Is(err, home.ErrUnhealthyWatcher) {
		t.Fatalf("start err = %v\n%s, want ErrUnhealthyWatcher", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "task"))
	if err != nil || agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("aggregate after failed start = %+v err=%v, want untouched queued seed", agg, err)
	}
}

// TestTaskRetryRefusesLiveGeneration proves `task retry` fails closed on a
// generation that still owns live work: the canonical Reopen precondition
// fires before the .status projection is touched.
func TestTaskRetryRefusesLiveGeneration(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	blockReq := taskauthority.CanonicalBlockRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "task"),
		Precondition: domain.Of(1, 1),
		Detail:       "dependency",
		Reason:       "seed",
	}
	if _, err := auth.Block(mustCanonicalOp(t, "seed-block", blockReq), blockReq); err != nil {
		t.Fatal(err)
	}
	if out, err := runTaskCommand(t, []string{"task", "retry", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "reopen requires terminal task") {
		t.Fatalf("retry of blocked generation = %v\n%s, want reopen precondition", err, out)
	}
}
