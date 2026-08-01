package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestBacklogAddCreatesQueuedAggregate(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok || agg.State != "queued" {
		t.Fatalf("aggregate = %+v ok=%v err=%v", agg, ok, err)
	}
}

func TestDuplicateBacklogAddPreservesExistingState(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "original", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	beforeAggregate := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "task", "1.json"))
	beforePointer := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "task", "current"))
	beforeBacklog := readFileForTest(t, filepath.Join(homeDir, "data", "md"))
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "changed", "--home", homeDir}); err == nil {
		t.Fatalf("duplicate add succeeded: %s", out)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "task", "1.json")); got != beforeAggregate {
		t.Fatal("duplicate add changed aggregate")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "task", "current")); got != beforePointer {
		t.Fatal("duplicate add changed current pointer")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "data", "md")); got != beforeBacklog {
		t.Fatal("duplicate add changed backlog")
	}
}

func TestBacklogStartAndUnblockUseDistinctLifecycleOperations(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir}); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "start", "task", "--home", homeDir}); err == nil || !strings.Contains(out, "start requires queued task") {
		t.Fatalf("second start correction = %v\n%s", err, out)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "unblock", "task", "--home", homeDir}); err != nil {
		t.Fatalf("unblock: %v\n%s", err, out)
	}
	if _, err := home.UnblockTask(homeDir, "task"); !errors.Is(err, home.ErrTaskLifecyclePrecondition) {
		t.Fatalf("unblock queued error = %v", err)
	}
}

func TestBacklogReopenSynchronizesAggregateAndProjection(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "done", "merged"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "md"), []byte("# Backlog\n\n- [x] task: work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "reopen", "task", "--home", homeDir}); err != nil {
		t.Fatalf("reopen: %v\n%s", err, out)
	}
	old, err := home.ReadTaskAggregate(homeDir, "task", "1")
	if err != nil || old.Current || old.State != "done" {
		t.Fatalf("old aggregate = %+v err=%v", old, err)
	}
	current, ok, err := home.ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok || current.Generation != "2" || current.State != "queued" {
		t.Fatalf("current aggregate = %+v ok=%v err=%v", current, ok, err)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil || !strings.Contains(string(backlog), "[ ] task") {
		t.Fatalf("backlog after reopen = %q err=%v", backlog, err)
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

func TestBacklogRetrySupersedesFailedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "failed", "soldier failed"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "md"), []byte("# Backlog\n\n## In flight\n- [-] task: work\n"), 0600); err != nil {
		t.Fatal(err)
	}
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

func TestBacklogRetryRefusesLiveGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "retry", "task", "--home", homeDir}); err == nil {
		t.Fatalf("retry of blocked generation succeeded: %s", out)
	}
}
