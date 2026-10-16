package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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

// registerCLITestProject registers name, over a fleet base, as a project at
// an empty directory, so task add can resolve it and write its brief.
func registerCLITestProject(t *testing.T, homeDir, name string) {
	t.Helper()
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.FleetBaseConfig{Backend: "tmux"}}); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "project", "add", name, t.TempDir(), "--home", homeDir); err != nil {
		t.Fatalf("project add %s: %v\n%s", name, err, out)
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
// kind, and project.
func TestTaskAddCreatesCanonicalQueuedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")
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
}

// TestTaskAddDuplicateReturnsTypedConflict proves duplicate creation returns a
// typed conflict and changes no authoritative record.
func TestTaskAddDuplicateReturnsTypedConflict(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "original", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("first add: %v\n%s", err, out)
	}
	beforeCurrent := readFileForTest(t, filepath.Join(homeDir, "state", "task-authority", "tasks", "beta", "current.json"))
	if _, err := runTaskCommand(t, []string{"task", "add", "beta", "changed", "--repo", "munsu", "--home", homeDir}); err == nil {
		t.Fatal("duplicate task add succeeded")
	} else if !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("duplicate add error = %v, want task conflict", err)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "task-authority", "tasks", "beta", "current.json")); got != beforeCurrent {
		t.Fatal("duplicate add changed authoritative aggregate")
	}
}

// TestTaskListReadsCanonicalAuthorityRecords proves `task list` serves the
// canonical Authority records' kind, project, and phase.
func TestTaskListReadsCanonicalAuthorityRecords(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "proj-a")
	registerCLITestProject(t, homeDir, "munsu")
	for _, args := range [][]string{
		{"task", "add", "alpha", "first", "--kind", "scout", "--scope", "investigate proj-a", "--repo", "proj-a", "--home", homeDir},
		{"task", "add", "beta", "second", "--kind", "ship", "--repo", "munsu", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
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
	if e := byID["beta"]; e.Kind != "ship" || e.Project != "munsu" || e.Status != "queued" {
		t.Fatalf("beta entry = %+v", e)
	}
}

// TestTaskShowReadsCanonicalAuthorityRecords proves `task show` reads the
// canonical Aggregate.
func TestTaskShowReadsCanonicalAuthorityRecords(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "canonical description", "--kind", "ship", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
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
	// Each task field is printed once, from the Aggregate.
	for _, field := range []string{"description: ", "kind: ship", "project: "} {
		if n := strings.Count(out, field); n != 1 {
			t.Fatalf("task show prints %q %d times, want once:\n%s", field, n, out)
		}
	}
}

// TestTaskObserveCanonicalTask proves `task observe` resolves a task from its
// canonical Authority record.
func TestTaskObserveCanonicalTask(t *testing.T) {
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

// seedAuthorityTask creates one queued task through the concrete canonical
// Authority so lifecycle tests drive canonical Task Authority records, never
// legacy v1 aggregates.
func seedAuthorityTask(t *testing.T, auth *taskauthority.Canonical, id string) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Project:     mustProjectID(t, "munsu"),
		Reason:      "test seed",
	}
	op := mustCanonicalOp(t, "op-create-seed-"+id, req)
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}
}

// readFileForTest reads one test fixture file, failing the test on error.
func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

func mustProjectID(t *testing.T, name string) domain.ProjectID {
	t.Helper()
	pid, err := domain.NewProjectID(name)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
