package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// mustCanonical opens the canonical home and constructs the canonical Task
// Authority over it, failing the test on errors.
func mustCanonical(t *testing.T, homeDir string) *tauth.Canonical {
	t.Helper()
	h, err := mhome.Open(homeDir)
	if err != nil {
		t.Fatalf("home.Open(%s): %v", homeDir, err)
	}
	c, err := tauth.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical(%s): %v", homeDir, err)
	}
	return c
}

// mustTaskID returns a validated typed Task identity.
func mustTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%s): %v", value, err)
	}
	return id
}

// mustOp builds a validated Operation from the given id and intent, deriving
// the digest from the typed intent.
func mustOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

// mustProjectID returns a validated typed Project identity.
func mustProjectID(t *testing.T, value string) domain.ProjectID {
	t.Helper()
	id, err := domain.NewProjectID(value)
	if err != nil {
		t.Fatalf("NewProjectID(%s): %v", value, err)
	}
	return id
}

// taPrecondition builds a typed expectation for the given generation/revision.
func taPrecondition(gen, rev uint64) domain.Precondition {
	return domain.Of(gen, rev)
}

// seedHandoffTaskV2 creates a canonical task at generation 7 (each generation
// cycled through complete+reopen) plus its .meta/.status/brief projections,
// and appends the given meta entries. The task ends queued at generation 7.
func seedHandoffTaskV2(t *testing.T, homeDir, taskID string, meta map[string]string) {
	t.Helper()
	description := taskID
	if meta != nil && meta["description"] != "" {
		description = meta["description"]
	}
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	c := mustCanonical(t, homeDir)
	create := tauth.CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      mustTaskID(t, taskID),
		Owner:       "general",
		Description: description,
		Kind:        "ship",
		Project:     mustProjectID(t, "munsu"),
		Reason:      "seed",
	}
	if _, err := c.Create(mustOp(t, "seed-create-"+taskID, create), create); err != nil {
		t.Fatal(err)
	}
	for gen := tauth.Generation(1); gen < 7; gen++ {
		complete := tauth.CanonicalCompleteRequest{
			HomeID:       c.HomeID(),
			TaskID:       mustTaskID(t, taskID),
			Precondition: taPrecondition(uint64(gen), 1),
			To:           tauth.PhaseDone,
			Reason:       "seed",
		}
		if _, err := c.Complete(mustOp(t, fmt.Sprintf("seed-complete-%s-%d", taskID, gen), complete), complete); err != nil {
			t.Fatal(err)
		}
		reopen := tauth.CanonicalReopenRequest{
			HomeID:       c.HomeID(),
			TaskID:       mustTaskID(t, taskID),
			Precondition: taPrecondition(uint64(gen), 2),
			Reason:       "seed",
		}
		if _, err := c.Reopen(mustOp(t, fmt.Sprintf("seed-reopen-%s-%d", taskID, gen), reopen), reopen); err != nil {
			t.Fatal(err)
		}
	}
	if meta == nil {
		meta = map[string]string{"description": taskID, "generation": "7"}
	}
	if err := mhome.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(homeDir, taskID, "queued: ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(homeDir, taskID)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(homeDir, taskID), []byte("# "+taskID+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// writeV1AggregateDoc plants a legacy v2-identity task document directly on
// disk in a canonical home. The canonical surface fails closed on this shape
// (ADR-0008 §11: internal-history v2 identities are never accepted), so every
// canonical read fails closed instead of serving the legacy record.
func writeV1AggregateDoc(t *testing.T, homeDir, taskID, owner, definition string) {
	t.Helper()
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	doc := `{"home_revision":1,"aggregate":{"schema_version":"munsu.task-authority/v2","task_id":"` + taskID + `","generation":1,"revision":1,"current":true,"definition":{"owner":"` + owner + `","description":"` + definition + `"},"phase":"queued"}}`
	if err := os.WriteFile(filepath.Join(dir, "current.json"), []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
}

func seedHandoffTaskV2Default(t *testing.T, homeDir, taskID string) {
	t.Helper()
	seedHandoffTaskV2(t, homeDir, taskID, nil)
}

// mustCurrentAggregate reads the canonical current aggregate of a task,
// failing the test when the home does not own it.
func mustCurrentAggregate(t *testing.T, homeDir, taskID string) tauth.Aggregate {
	t.Helper()
	agg, err := mustCanonical(t, homeDir).Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("home %s does not own %s: %v", homeDir, taskID, err)
	}
	return agg
}

// mustNoCurrentAggregate fails the test when the home still owns the task.
func mustNoCurrentAggregate(t *testing.T, homeDir, taskID string) {
	t.Helper()
	if _, err := mustCanonical(t, homeDir).Get(mustTaskID(t, taskID)); !errors.Is(err, tauth.ErrNotFound) {
		t.Fatalf("home %s still owns %s: %v", homeDir, taskID, err)
	}
}

// stubHandoffBackend installs the testing tasks-axi backend and path.
func stubHandoffBackend(t *testing.T, homeDir, fake string) {
	t.Helper()
	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	captainLookPath = func(string) (string, error) { return fake, nil }
	isTasksAxiBackend = func(string) bool { return true }
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })
}

// seedCaptainHome initializes a canonical home and marks it as a captain.
func seedCaptainHome(t *testing.T, homeDir, id string) {
	t.Helper()
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(homeDir, id); err != nil {
		t.Fatal(err)
	}
}

// writeBacklog writes a backlog at the given home.
func writeBacklog(t *testing.T, home string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, "data")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "backlog.md")
	content := "# Backlog\n\n"
	for _, line := range lines {
		content += "- [ ] " + line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandoffTransfersTaskToCaptain(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	srcBacklog := writeBacklog(t, parent, "TASK-1 - TASK-1")
	dstBacklog := writeBacklog(t, sm)

	fake := fakeMovingTasksAxi(t, parent)
	stubHandoffBackend(t, parent, fake)

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	// Source no longer owns the task; destination owns a fresh generation
	// bound to the captain, with the definition preserved.
	mustNoCurrentAggregate(t, parent, "TASK-1")
	agg := mustCurrentAggregate(t, sm, "TASK-1")
	if !agg.Current || agg.Generation != 1 || agg.Phase != tauth.PhaseQueued {
		t.Fatalf("destination aggregate = %+v, want current generation 1 queued", agg)
	}
	if agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination owner = %q, want captain:test-sm", agg.Definition.Owner)
	}
	if agg.Definition.Description != "TASK-1" || agg.Definition.Kind != "ship" || agg.Definition.Project != "munsu" {
		t.Fatalf("destination lost definition: %+v", agg.Definition)
	}
	if agg.Transfer == nil || agg.Transfer.SourceHome == "" || agg.Transfer.SourceGeneration != 7 {
		t.Fatalf("destination transfer provenance = %+v, want source home + generation 7", agg.Transfer)
	}
	// Projections copied verbatim and source projections removed.
	dstMeta, err := mhome.ReadMeta(sm, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if dstMeta["description"] != "TASK-1" || dstMeta["generation"] != "7" {
		t.Fatalf("destination meta = %+v", dstMeta)
	}
	if _, err := mhome.ReadMeta(parent, "TASK-1"); err == nil {
		t.Fatal("source meta still exists after handoff")
	}
	status, err := mhome.ReadStatus(sm, "TASK-1")
	if err != nil || len(status) != 1 || status[0] != "queued: ready" {
		t.Fatalf("destination status = %#v err=%v", status, err)
	}
	if !Exists(sm, "TASK-1") {
		t.Fatal("destination brief missing")
	}
	// Backlog moved.
	gotSrc, _ := os.ReadFile(srcBacklog)
	if strings.Contains(string(gotSrc), "TASK-1") {
		t.Fatalf("source backlog still contains TASK-1: %s", gotSrc)
	}
	gotDst, _ := os.ReadFile(dstBacklog)
	if !strings.Contains(string(gotDst), "TASK-1") {
		t.Fatalf("destination backlog missing TASK-1: %s", gotDst)
	}
}

func TestHandoffDestinationAlreadyOwnsFailsClosed(t *testing.T) {
	parent := t.TempDir()
	// Destination sits outside the source's captains/ tree so the candidate
	// owner resolution does not report the task as ambiguous.
	sm := filepath.Join(parent, "test-sm")
	seedHandoffTaskV2(t, parent, "TASK-1", map[string]string{"description": "source truth", "generation": "7"})
	seedCaptainHome(t, sm, "test-sm")
	// Destination already owns the same task id.
	c := mustCanonical(t, sm)
	create := tauth.CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      mustTaskID(t, "TASK-1"),
		Owner:       "captain:test-sm",
		Description: "destination truth",
		Kind:        "ship",
		Project:     mustProjectID(t, "munsu"),
		Reason:      "seed",
	}
	if _, err := c.Create(mustOp(t, "dest-create", create), create); err != nil {
		t.Fatal(err)
	}
	beforeDest, err := c.Get(mustTaskID(t, "TASK-1"))
	if err != nil {
		t.Fatal(err)
	}
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))

	err = Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected typed destination conflict")
	}
	if de, ok := err.(*domain.Error); !ok || de.Category != domain.ErrorConflict {
		t.Fatalf("error = %v, want typed conflict", err)
	}
	sourceAgg := mustCurrentAggregate(t, parent, "TASK-1")
	if sourceAgg.Generation != 7 || sourceAgg.Definition.Owner != "general" {
		t.Fatalf("source aggregate = %+v, want untouched", sourceAgg)
	}
	destAgg, err := c.Get(mustTaskID(t, "TASK-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(destAgg, beforeDest) {
		t.Fatalf("destination aggregate = %+v, want untouched %+v", destAgg, beforeDest)
	}
}

func TestHandoffProjectionPreflightLeavesAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	// A stale destination projection quarantines the transfer.
	if err := mhome.WriteMeta(sm, "TASK-1", map[string]string{"description": "stale destination projection"}); err != nil {
		t.Fatal(err)
	}
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected destination projection preflight failure")
	}
	sourceAgg := mustCurrentAggregate(t, parent, "TASK-1")
	if sourceAgg.Definition.Owner != "general" || sourceAgg.Generation != 7 {
		t.Fatalf("source aggregate = %+v, want untouched", sourceAgg)
	}
	mustNoCurrentAggregate(t, sm, "TASK-1")
}

func TestHandoffFailureLeavesTaskAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2(t, parent, "TASK-1", map[string]string{"description": "Preserve authority", "kind": "ship", "project": "munsu", "issue": "#384", "delivery_plan": "no-mistakes", "context_hints": "read AGENTS.md", "generation": "7"})
	seedCaptainHome(t, sm, "test-sm")
	beforeAgg := mustCurrentAggregate(t, parent, "TASK-1")
	beforeMeta, err := os.ReadFile(filepath.Join(parent, "state", "TASK-1.meta"))
	if err != nil {
		t.Fatal(err)
	}
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)

	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) {
		return filepath.Join(parent, "missing-tasks-axi"), nil
	}
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })

	err = Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected handoff failure")
	}
	if got := mustCurrentAggregate(t, parent, "TASK-1"); !reflect.DeepEqual(got, beforeAgg) {
		t.Fatal("source aggregate changed after failed handoff")
	}
	afterMeta, err := os.ReadFile(filepath.Join(parent, "state", "TASK-1.meta"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterMeta, beforeMeta) {
		t.Fatal("source meta changed after failed handoff")
	}
	mustNoCurrentAggregate(t, sm, "TASK-1")
}

func TestHandoffChildWithExistingParentDependencySucceeds(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-PARENT")
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeBlockedTasksAxi(t, parent))

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("handoff of child with in-source parent dependency failed: %v", err)
	}
	child := mustCurrentAggregate(t, sm, "TASK-1")
	if child.Generation != 1 {
		t.Fatalf("destination child = %+v, want generation 1", child)
	}
	mustCurrentAggregate(t, parent, "TASK-PARENT")
}

func TestHandoffHoldsGateBlocksTransfer(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))

	// A durable dispatch hold on the handoff action blocks the transfer.
	c := mustCanonical(t, parent)
	hold := tauth.CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "hold-handoff",
		Scope:   tauth.DispatchHoldScope{TaskIDs: []string{"TASK-1"}},
		Actions: []tauth.DispatchAction{tauth.DispatchActionHandoff},
		Reason:  "freeze handoff",
	}
	if _, err := c.AddHold(mustOp(t, "op-hold-handoff", hold), hold); err != nil {
		t.Fatal(err)
	}

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected dispatch-held failure")
	}
	if !errors.Is(err, tauth.ErrDispatchHeld) {
		t.Fatalf("error = %v, want ErrDispatchHeld", err)
	}
	mustCurrentAggregate(t, parent, "TASK-1")
	mustNoCurrentAggregate(t, sm, "TASK-1")
}

func TestHandoffRequiresQueuedState(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	// A tasks-axi backend that reports the task as working.
	fake := filepath.Join(parent, "fake-working-tasks-axi")
	script := "#!/bin/sh\nif [ \"$1\" = show ]; then echo 'state: working'; exit 0; fi\nexit 2\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	stubHandoffBackend(t, parent, fake)

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected only-queued refusal")
	}
	if !strings.Contains(err.Error(), "only queued items may be handed off") {
		t.Fatalf("error = %v, want queued-only refusal", err)
	}
	mustCurrentAggregate(t, parent, "TASK-1")
	mustNoCurrentAggregate(t, sm, "TASK-1")
}

func TestHandoffSameHomeRefused(t *testing.T) {
	parent := t.TempDir()
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))
	if err := Handoff(parent, parent, []string{"TASK-1"}); err == nil {
		t.Fatal("expected same-home refusal")
	}
}

func TestHandoffDestinationUnmarkedRefused(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	// The destination is not marked as a captain home.
	if _, err := mhome.Init(sm); err != nil {
		t.Fatal(err)
	}
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))
	if err := Handoff(parent, sm, []string{"TASK-1"}); err == nil {
		t.Fatal("expected unmarked destination refusal")
	}
}

func TestHandoffFailsClosedOnV1Homes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		seedSource bool
		seedDest   bool
	}{
		{"v1 source", true, false},
		{"v1 destination", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			sm := filepath.Join(parent, "captains", "test-sm")
			if tc.seedSource {
				// Initialize the parent home before any captain subdir is created.
				writeV1AggregateDoc(t, parent, "TASK-1", "general", "legacy")
			}
			seedCaptainHome(t, sm, "test-sm")
			if tc.seedDest {
				writeV1AggregateDoc(t, sm, "TASK-1", "captain:test-sm", "legacy")
			}
			writeBacklog(t, parent, "TASK-1 - TASK-1")
			writeBacklog(t, sm)
			stubHandoffBackend(t, parent, fakeQueuedTasksAxi(t, parent))

			err := Handoff(parent, sm, []string{"TASK-1"})
			if err == nil {
				t.Fatal("expected fail-closed on legacy v2 home")
			}
			if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName)); !os.IsNotExist(err) {
				t.Fatal("fail-closed left handoff journal state behind")
			}
		})
	}
}

func TestHandoffProjectionFailureReturnsPartialAndPreservesOwnership(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedCaptainHome(t, sm, "test-sm")
	writeBacklog(t, parent, "TASK-1 - TASK-1")
	writeBacklog(t, sm)
	stubHandoffBackend(t, parent, fakeMovingTasksAxi(t, parent))

	// Make the source meta unreadable so the post-transfer projection copy
	// fails after the authoritative transfer commits.
	metaPath := filepath.Join(parent, "state", "TASK-1.meta")
	if err := os.Chmod(metaPath, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(metaPath, 0600) })

	err := Handoff(parent, sm, []string{"TASK-1"})
	var partial *HandoffPartialError
	if !errors.As(err, &partial) {
		t.Fatalf("Handoff err = %v, want typed HandoffPartialError", err)
	}
	if !reflect.DeepEqual(partial.Transferred, []string{"TASK-1"}) {
		t.Fatalf("partial.Transferred = %v, want [TASK-1]", partial.Transferred)
	}
	// Ownership truth is unchanged by the projection failure: the destination
	// owns the complete generation, the source is superseded, and nothing was
	// rolled back.
	agg := mustCurrentAggregate(t, sm, "TASK-1")
	if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination aggregate after projection failure = %+v", agg)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
}

func TestRecoverTaskHandoffsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := RecoverTaskHandoffs(dir); err != nil {
		t.Fatalf("RecoverTaskHandoffs should be a no-op: %v", err)
	}
}

func fakeBlockedTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-blocked-tasks-axi")
	script := `#!/bin/sh
if [ "$1" = show ]; then
  echo 'state: queued'
  echo 'blocked-by: TASK-PARENT'
  exit 0
fi
if [ "$1" = mv ]; then
  to=""
  file=""
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --to) to="$2"; shift 2 ;;
      --file) file="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  cp "$file" "$file.tmp"
  cp "$to" "$to.tmp"
  printf '# Backlog\n\n' > "$file"
  cat "$file.tmp" >> "$to"
  rm -f "$file.tmp" "$to.tmp"
  exit 0
fi
echo unexpected >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeQueuedTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-tasks-axi")
	script := "#!/bin/sh\nif [ \"$1\" = show ]; then echo 'state: queued'; exit 0; fi\nif [ \"$1\" = mv ]; then exit 0; fi\necho unexpected >&2\nexit 2\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeMovingTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-moving-tasks-axi")
	script := `#!/bin/sh
if [ "$1" = show ]; then
  echo 'state: queued'
  exit 0
fi
if [ "$1" = mv ]; then
  to=""
  file=""
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --to) to="$2"; shift 2 ;;
      --file) file="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  cp "$file" "$file.tmp"
  cp "$to" "$to.tmp"
  printf '# Backlog\n\n' > "$file"
  cat "$file.tmp" >> "$to"
  rm -f "$file.tmp" "$to.tmp"
  exit 0
fi
echo unexpected >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
