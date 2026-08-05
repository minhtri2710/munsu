package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// runTaskCommand executes one CLI command and returns the serialized output
// plus the raw error, mirroring runBacklogLifecycleCommand.
func runTaskCommand(t *testing.T, args []string) (string, error) {
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

// initCLITestHome initializes a fresh canonical home for CLI fixtures.
func initCLITestHome(t *testing.T, homeDir string) {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
}

// testAuthorityFor composes the concrete canonical Authority over the opened
// home for the given home so tests read canonical records through the public
// interface, never through private file layout. The home is initialized when
// needed.
func testAuthorityFor(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	if _, err := os.Stat(filepath.Join(homeDir, home.IdentityFileName)); err != nil {
		initCLITestHome(t, homeDir)
	}
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

// mustCanonicalOp builds one typed canonical operation for test fixtures.
func mustCanonicalOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

// TestTaskAddCreatesCanonicalQueuedGeneration proves one `task add` produces
// exactly one queued Task Generation at Revision one with owner, definition,
// kind, and project, and that .meta is written only as a post-commit
// projection (ADR-0007 §7).
func TestTaskAddCreatesCanonicalQueuedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--kind", "ship", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	agg, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "beta"))
	if err != nil {
		t.Fatalf("authority Get: %v", err)
	}
	if agg.Generation != 1 || agg.Revision != taskauthority.FirstRevision || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate = %+v", agg)
	}
	if agg.Definition.Owner != filepath.Base(homeDir) || agg.Definition.Description != "ship beta" || agg.Definition.Kind != "ship" || agg.Definition.Project != "munsu" {
		t.Fatalf("definition = %+v", agg.Definition)
	}
	meta, err := home.ReadMeta(homeDir, "beta")
	if err != nil {
		t.Fatalf("meta projection: %v", err)
	}
	if meta["description"] != "ship beta" || meta["kind"] != "ship" || meta["project"] != "munsu" || meta["repo"] != "munsu" {
		t.Fatalf("meta projection = %v", meta)
	}
}

// TestTaskAddDuplicateReturnsTypedConflictWithoutProjection proves duplicate
// creation returns a typed conflict and creates no projection duplicate and
// no additional authoritative record (ADR-0007 §6, Task 3.2 criterion 2).
func TestTaskAddDuplicateReturnsTypedConflictWithoutProjection(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "original", "--home", homeDir}); err != nil {
		t.Fatalf("first add: %v\n%s", err, out)
	}
	beforeMeta := readFileForTest(t, filepath.Join(homeDir, "state", "beta.meta"))
	beforeCurrent := readFileForTest(t, filepath.Join(homeDir, "state", "task-authority", "tasks", "beta", "current.json"))
	if _, err := runTaskCommand(t, []string{"task", "add", "beta", "changed", "--home", homeDir}); err == nil {
		t.Fatal("duplicate task add succeeded")
	} else if !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("duplicate add error = %v, want task conflict", err)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "beta.meta")); got != beforeMeta {
		t.Fatal("duplicate add rewrote meta projection")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "task-authority", "tasks", "beta", "current.json")); got != beforeCurrent {
		t.Fatal("duplicate add changed authoritative aggregate")
	}
}

// TestTaskListReadsCanonicalAuthorityRecords proves `task list` serves only
// canonical Authority records: meta-only tasks are absent and tampered meta
// cannot override kind, project, or phase (Task 3.2 criterion 3).
func TestTaskListReadsCanonicalAuthorityRecords(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	for _, args := range [][]string{
		{"task", "add", "alpha", "first", "--kind", "scout", "--repo", "proj-a", "--home", homeDir},
		{"task", "add", "beta", "second", "--kind", "ship", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// A meta-only task with no canonical record must not be listed.
	if err := home.WriteMeta(homeDir, "ghost", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	// A tampered meta projection must not override canonical records.
	if err := home.WriteMeta(homeDir, "alpha", map[string]string{"kind": "hacked", "project": "hacked"}); err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{"task", "list", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("task list: %v\n%s", err, out)
	}
	var resp Response[[]TaskEntry]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("list = %+v, want 2 canonical tasks", resp.Data)
	}
	byID := map[string]TaskEntry{}
	for _, e := range resp.Data {
		byID[e.ID] = e
	}
	if e := byID["alpha"]; e.Kind != "scout" || e.Project != "proj-a" || e.Status != "queued" {
		t.Fatalf("alpha entry = %+v", e)
	}
	if e := byID["beta"]; e.Kind != "ship" || e.Project != "-" || e.Status != "queued" {
		t.Fatalf("beta entry = %+v", e)
	}
	if _, ok := byID["ghost"]; ok {
		t.Fatal("meta-only task ghost leaked into canonical task list")
	}
}

// TestTaskShowReadsCanonicalAuthorityRecords proves `task show` reads the
// canonical Aggregate and that a tampered .meta cannot override authoritative
// fields (Task 3.2 criterion 3).
func TestTaskShowReadsCanonicalAuthorityRecords(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "canonical description", "--kind", "ship", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	if err := home.WriteMeta(homeDir, "beta", map[string]string{"description": "fake description", "kind": "scout", "project": "fake-proj"}); err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{"task", "show", "beta", "--home", homeDir})
	if err != nil {
		t.Fatalf("task show: %v\n%s", err, out)
	}
	for _, want := range []string{
		"description: canonical description",
		"kind: ship",
		"project: munsu",
		"state: queued",
		"owner: " + filepath.Base(homeDir),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("task show missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fake description") || strings.Contains(out, "scout") {
		t.Fatalf("task show leaked tampered meta:\n%s", out)
	}
}

// TestBacklogAddCreatesCanonicalQueuedGeneration proves `backlog add` creates
// one queued Task Generation and then appends the backlog projection.
func TestBacklogAddCreatesCanonicalQueuedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--kind", "ship", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	agg, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "task"))
	if err != nil {
		t.Fatalf("authority Get: %v", err)
	}
	if agg.Generation != 1 || agg.Revision != taskauthority.FirstRevision || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate = %+v", agg)
	}
	if agg.Definition.Owner != filepath.Base(homeDir) || agg.Definition.Description != "work" || agg.Definition.Kind != "ship" || agg.Definition.Project != "munsu" {
		t.Fatalf("definition = %+v", agg.Definition)
	}
	backlog, err := os.ReadFile(filepath.Join(homeDir, "data", "md"))
	if err != nil {
		t.Fatalf("backlog projection: %v", err)
	}
	if !strings.Contains(string(backlog), "task") {
		t.Fatalf("backlog projection missing task:\n%s", backlog)
	}
}

// TestBacklogAddProjectionFailureReturnsTypedPartial proves backlog add's
// projection failure surfaces a typed partial result while the authoritative
// Task Generation survives. A directory at the backlog file path forces the
// projection to fail.
func TestBacklogAddProjectionFailureReturnsTypedPartial(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "data", "md"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runBacklogLifecycleCommand(t, []string{"backlog", "add", "task", "work", "--home", homeDir})
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "queued" {
		t.Fatalf("error = %T %v, want typed partial result", err, err)
	}
	agg, getErr := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "task"))
	if getErr != nil {
		t.Fatalf("authoritative commit must survive projection failure: %v", getErr)
	}
	if agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate = %+v", agg)
	}
}

// TestTaskObserveCanonicalTaskWithoutMeta proves `task observe` resolves a
// task from its canonical Authority record even when no .meta projection
// exists yet (Task 7.8 canonical-read preference): the projection is display
// fallback, never the existence gate.
func TestTaskObserveCanonicalTaskWithoutMeta(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	tid, err := domain.NewTaskID("obs")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "owner",
		Description: "observe me",
		Kind:        "ship",
		Reason:      "test",
	}
	op := mustCanonicalOp(t, "op-create-obs", req)
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{"task", "observe", "obs", "--home", homeDir})
	if err != nil {
		t.Fatalf("task observe: %v\n%s", err, out)
	}
	if !strings.Contains(out, "task_id: obs") {
		t.Fatalf("task observe did not resolve the canonical task:\n%s", out)
	}
}

// mustTaskIDFor converts a test task ID into a typed identity.
func mustTaskIDFor(t *testing.T, value string) domain.TaskID {
	t.Helper()
	tid, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatal(err)
	}
	return tid
}
