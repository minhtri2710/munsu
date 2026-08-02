package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

func TestBacklogAddCreatesQueuedAggregate(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	agg, err := testAuthorityFor(t, homeDir).Get("task")
	if err != nil {
		t.Fatalf("authority Get: %v", err)
	}
	if agg.Phase != taskauthority.PhaseQueued || agg.Generation != 1 {
		t.Fatalf("aggregate = %+v", agg)
	}
}

func TestDuplicateBacklogAddPreservesExistingState(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "original", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	beforeAggregate := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "task", "1.json"))
	beforePointer := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "task", "current"))
	beforeBacklog := readFileForTest(t, filepath.Join(homeDir, "data", "md"))
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "changed", "--home", homeDir}); err == nil {
		t.Fatalf("duplicate add succeeded: %s", out)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "task", "1.json")); got != beforeAggregate {
		t.Fatal("duplicate add changed aggregate")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "task", "current")); got != beforePointer {
		t.Fatal("duplicate add changed current pointer")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "data", "md")); got != beforeBacklog {
		t.Fatal("duplicate add changed backlog")
	}
}

// seedAuthorityTask creates one queued task through the concrete Authority so
// lifecycle tests drive canonical Task Authority records (ADR-0007), never
// legacy v1 aggregates.
func seedAuthorityTask(t *testing.T, auth *taskauthority.Authority, id string) {
	t.Helper()
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: newTaskAuthorityOperationID("seed-" + id),
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      id,
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Reason:      "test seed",
	}); err != nil {
		t.Fatal(err)
	}
}

// TestBacklogStartAndUnblockUseDistinctLifecycleOperations proves `backlog
// start` and `backlog unblock` drive the named Authority operations (Task 3.3
// criterion 1): start requires queued, unblock requires blocked, and each
// command advances the canonical aggregate before the backlog projection.
func TestBacklogStartAndUnblockUseDistinctLifecycleOperations(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	seedBacklogFileForTest(t, homeDir, "- [ ] task: work\n")

	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir}); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("aggregate after start = %+v err=%v", agg, err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "start requires queued task") {
		t.Fatalf("second start correction = %v\n%s", err, out)
	}
	if _, err := auth.Block(taskauthority.BlockRequest{
		OperationID:        newTaskAuthorityOperationID("seed-block"),
		Actor:              taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:             "task",
		ExpectedGeneration: agg.Generation,
		Detail:             "dependency",
		Reason:             "seed",
	}); err != nil {
		t.Fatal(err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "unblock", "task", "--home", homeDir}); err != nil {
		t.Fatalf("unblock: %v\n%s", err, out)
	}
	if agg, err = auth.Get("task"); err != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate after unblock = %+v err=%v", agg, err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "unblock", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "unblock requires blocked task") {
		t.Fatalf("second unblock error = %v\n%s", err, out)
	}
}

// TestBacklogReopenSynchronizesAggregateAndProjection proves `backlog reopen`
// drives the Authority Reopen operation (Task 3.3 criterion 4): the terminal
// generation stays immutable historical state, a new queued Generation starts
// at Revision one, and the backlog projection updates after the authoritative
// commit.
func TestBacklogReopenSynchronizesAggregateAndProjection(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	if _, err := auth.Complete(taskauthority.CompleteRequest{
		OperationID:        newTaskAuthorityOperationID("seed-done"),
		Actor:              taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:             "task",
		ExpectedGeneration: 1,
		To:                 taskauthority.PhaseDone,
		Reason:             "seed",
	}); err != nil {
		t.Fatal(err)
	}
	seedBacklogFileForTest(t, homeDir, "- [x] task: work\n")
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "reopen", "task", "--home", homeDir}); err != nil {
		t.Fatalf("reopen: %v\n%s", err, out)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Generation != 2 || agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("current aggregate = %+v err=%v", agg, err)
	}
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	old, ok := view.Aggregate("task", 1)
	if !ok || old.Current || old.Phase != taskauthority.PhaseDone {
		t.Fatalf("historical aggregate = %+v ok=%v, want immutable done generation 1", old, ok)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[ ] task") {
		t.Fatalf("backlog after reopen = %q err=%v", backlog, err)
	}
}

// TestBacklogDoneCallsAuthorityComplete proves `backlog done` drives the
// named Authority Complete operation to the done terminal phase and updates
// the backlog projection only after the authoritative commit (Task 3.3
// criterion 1).
func TestBacklogDoneCallsAuthorityComplete(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	seedBacklogFileForTest(t, homeDir, "- [ ] task: work\n")

	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "done", "task", "--home", homeDir}); err != nil {
		t.Fatalf("done: %v\n%s", err, out)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Phase != taskauthority.PhaseDone {
		t.Fatalf("aggregate after done = %+v err=%v", agg, err)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[x] task") {
		t.Fatalf("backlog after done = %q err=%v", backlog, err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "done", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "complete requires a non-terminal task") {
		t.Fatalf("second done error = %v\n%s", err, out)
	}
}

// TestBacklogBlockCallsAuthorityBlock proves `backlog block` drives the named
// Authority Block operation, records the dependency detail on the
// authoritative aggregate, and fails a second block closed before touching the
// projection (Task 3.3 criteria 1 and 2).
func TestBacklogBlockCallsAuthorityBlock(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	seedBacklogFileForTest(t, homeDir, "- [ ] task: work\n")

	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "block", "task", "--by", "dep-1", "--home", homeDir}); err != nil {
		t.Fatalf("block: %v\n%s", err, out)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Phase != taskauthority.PhaseBlocked || agg.PhaseDetail != "backlog: blocked by dep-1" {
		t.Fatalf("aggregate after block = %+v err=%v", agg, err)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[!] task") {
		t.Fatalf("backlog after block = %q err=%v", backlog, err)
	}
	before := readFileForTest(t, filepath.Join(homeDir, "data", "md"))
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "block", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "block requires queued or working task") {
		t.Fatalf("second block error = %v\n%s", err, out)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "data", "md")); got != before {
		t.Fatal("invalid block transition mutated the backlog projection")
	}
}

// TestBacklogLifecycleProjectionFailureReturnsTypedPartialWithoutReplay
// proves a backlog projection failure surfaces a typed partial result, keeps
// the authoritative commit, and is retryable without replaying the
// authoritative operation: a re-run of the command conflicts closed on the
// already-advanced phase, and retrying only the projection verb leaves the
// authoritative record untouched (Task 3.3 criteria 2 and 3).
func TestBacklogLifecycleProjectionFailureReturnsTypedPartialWithoutReplay(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	seedBacklogFileForTest(t, homeDir, "- [ ] task: work\n")

	// Break the projection so the post-commit projection write fails.
	if err := os.RemoveAll(filepath.Join(homeDir, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data"), []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir})
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "working" {
		t.Fatalf("error = %T %v, want typed partial result", err, err)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("authoritative commit must survive projection failure: %+v err=%v", agg, err)
	}
	revisionAfterPartial := agg.Revision

	// Re-running the same command must not replay the authoritative operation:
	// the fresh invocation conflicts closed on the already-working phase.
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "start requires queued task") {
		t.Fatalf("re-run after partial = %v\n%s, want typed conflict", err, out)
	}

	// Repair the projection and retry only the projection verb: no
	// authoritative operation is replayed and the committed record is
	// untouched.
	if err := os.Remove(filepath.Join(homeDir, "data")); err != nil {
		t.Fatal(err)
	}
	seedBacklogFileForTest(t, homeDir, "- [ ] task: work\n")
	if err := fleet.Run(homeDir, false, "start", []string{"task"}); err != nil {
		t.Fatalf("projection retry: %v", err)
	}
	after, err := auth.Get("task")
	if err != nil || after.Phase != taskauthority.PhaseWorking || after.Revision != revisionAfterPartial {
		t.Fatalf("projection retry replayed authority: %+v err=%v", after, err)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[-] task") {
		t.Fatalf("repaired projection = %q err=%v", backlog, err)
	}
}

func TestLegacyBacklogReadyReturnsExactCorrection(t *testing.T) {
	homeDir := t.TempDir()
	root := NewRootCommand()
	root.SetArgs([]string{"backlog", "ready", "task", "--home", homeDir})
	err := root.Execute()
	var correction *CommandCorrectionError
	if !errors.As(err, &correction) || correction.Code != "unsupported_input" || correction.Command != "munsu backlog unblock 'task'" {
		t.Fatalf("correction = %T %v", err, err)
	}
	var out bytes.Buffer
	if code := WriteContractError(&out, err, []string{"backlog", "ready", "task", "--output", "json"}); code != 2 {
		t.Fatalf("serialized correction code=%d output=%s", code, out.String())
	}
	var response ErrorResponse
	if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if response.Error.Action != "Run `munsu backlog unblock 'task'`" {
		t.Fatalf("action = %q", response.Error.Action)
	}
}

func TestLegacyBacklogAddStartReturnsExactCorrection(t *testing.T) {
	homeDir := t.TempDir()
	root := NewRootCommand()
	root.SetArgs([]string{"backlog", "add", "task id", "work here", "--start", "--home", homeDir})
	err := root.Execute()
	var correction *CommandCorrectionError
	want := "munsu backlog add 'task id' 'work here' --kind 'ship' && munsu backlog start 'task id'"
	if !errors.As(err, &correction) || correction.Code != "unsupported_input" || correction.Command != want {
		t.Fatalf("correction = %T %v, want %q", err, err, want)
	}
	var out bytes.Buffer
	if code := WriteContractError(&out, err, []string{"backlog", "add", "task id", "work here", "--start", "--output", "json"}); code != 2 {
		t.Fatalf("serialized correction code=%d output=%s", code, out.String())
	}
	var response ErrorResponse
	if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if response.Error.Action != "Run `munsu backlog add 'task id' 'work here' --kind 'ship' && munsu backlog start 'task id'`" {
		t.Fatalf("action = %q", response.Error.Action)
	}
}

func TestBacklogAddHelpHidesStartFlag(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"backlog", "add", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "--start") {
		t.Fatalf("help exposes deprecated --start: %s", out.String())
	}
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runBacklogLifecycleCommand(t *testing.T, args []string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		WriteContractError(&out, err, args)
	}
	return out.String(), err
}

// TestBacklogRetrySupersedesFailedGeneration seeds the legacy v1 aggregate
// directly until Task 3.3 cuts retry over to the Authority.
func TestBacklogRetrySupersedesFailedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.CreateTaskAggregate(homeDir, "task", "", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "failed", "soldier failed"); err != nil {
		t.Fatal(err)
	}
	seedBacklogFileForTest(t, homeDir, "## In flight\n- [-] task: work\n")
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "retry", "task", "--home", homeDir}); err != nil {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	current, ok, err := home.ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok || current.Generation != "2" || current.State != "queued" {
		t.Fatalf("current aggregate after retry = %+v ok=%v err=%v", current, ok, err)
	}
	old, err := home.ReadTaskAggregate(homeDir, "task", "1")
	if err != nil || old.State != "failed" || old.Current {
		t.Fatalf("historical aggregate = %+v err=%v", old, err)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[ ] task") {
		t.Fatalf("backlog after retry = %q err=%v", backlog, err)
	}
}

// seedBacklogFileForTest writes a runtime backlog file under homeDir/data,
// replicating the projection `backlog add` creates so v1-seeded lifecycle
// tests can drive the backlog projection backend.
func seedBacklogFileForTest(t *testing.T, homeDir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "md"), []byte("# Backlog\n\n"+body), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestBacklogRetryRefusesLiveGeneration seeds the legacy v1 aggregate
// directly until Task 3.3 cuts retry over to the Authority.
func TestBacklogRetryRefusesLiveGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.CreateTaskAggregate(homeDir, "task", "", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "retry", "task", "--home", homeDir}); err == nil {
		t.Fatalf("retry of blocked generation succeeded: %s", out)
	}
}
