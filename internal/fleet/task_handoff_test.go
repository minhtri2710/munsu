package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// seedHandoffTaskV2 creates a canonical v2 task at generation 7 (matching the
// legacy v1 handoff fixtures) plus its .meta/.status/brief projections, and
// appends the given meta entries. The task ends queued.
func seedHandoffTaskV2(t *testing.T, homeDir, taskID string, meta map[string]string) {
	t.Helper()
	description := taskID
	if meta != nil && meta["description"] != "" {
		description = meta["description"]
	}
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	auth := taskauthority.New(store)
	actor := taskauthority.Actor{ID: "general", Rank: "general"}
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "seed-create-" + taskID,
		Actor:       actor,
		TaskID:      taskID,
		Owner:       "general",
		Description: description,
		Kind:        "ship",
		Project:     "munsu",
		Reason:      "seed",
	}); err != nil {
		t.Fatal(err)
	}
	gen := taskauthority.Generation(1)
	for gen < 7 {
		if _, err := auth.Complete(taskauthority.CompleteRequest{
			OperationID:        fmt.Sprintf("seed-complete-%s-%d", taskID, gen),
			Actor:              actor,
			TaskID:             taskID,
			ExpectedGeneration: gen,
			To:                 taskauthority.PhaseDone,
			Reason:             "seed",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Reopen(taskauthority.ReopenRequest{
			OperationID:        fmt.Sprintf("seed-reopen-%s-%d", taskID, gen),
			Actor:              actor,
			TaskID:             taskID,
			ExpectedGeneration: gen,
			Reason:             "seed",
		}); err != nil {
			t.Fatal(err)
		}
		gen++
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

// writeV1AggregateDoc writes one legacy v1 aggregate document and its current
// pointer exactly as the deleted v1 aggregate store left them, so the
// task-authority v2 migration detects the home as a v1 home and every
// canonical read fails closed with ErrMigrationRequired (Task 8.2: the v1
// store writer is gone; the fixture writes the document shape directly).
func writeV1AggregateDoc(t *testing.T, homeDir, taskID, owner, definition string) {
	t.Helper()
	dir := filepath.Join(homeDir, "state", ".task-authority", "aggregates", taskID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"schema_version": "munsu.task-aggregate/v1",
		"task_id":        taskID,
		"generation":     "1",
		"current":        true,
		"owner":          owner,
		"definition":     definition,
		"state":          "queued",
		"kind":           "ship",
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func seedHandoffTaskV2Default(t *testing.T, homeDir, taskID string) {
	t.Helper()
	seedHandoffTaskV2(t, homeDir, taskID, nil)
}

// mustAuthority returns the composed Authority over the filesystem store for
// one home, failing the test on construction errors.
func mustAuthority(t *testing.T, homeDir string) *taskauthority.Authority {
	t.Helper()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return taskauthority.New(store)
}

// mustCurrentAggregate reads the canonical current aggregate of a task,
// failing the test when the home does not own it.
func mustCurrentAggregate(t *testing.T, homeDir, taskID string) taskauthority.Aggregate {
	t.Helper()
	agg, err := mustAuthority(t, homeDir).Get(taskID)
	if err != nil {
		t.Fatalf("home %s does not own %s: %v", homeDir, taskID, err)
	}
	return agg
}

// mustNoCurrentAggregate fails the test when the home still owns the task.
func mustNoCurrentAggregate(t *testing.T, homeDir, taskID string) {
	t.Helper()
	if _, err := mustAuthority(t, homeDir).Get(taskID); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("home %s still owns %s: %v", homeDir, taskID, err)
	}
}

// mustStoreView returns one canonical view of the home's store.
func mustStoreView(t *testing.T, homeDir string) taskauthority.View {
	t.Helper()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		boundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
		handoffCrashHook = func(got string) {
			if got == boundary {
				os.Exit(92)
			}
		}
		keys := []string{"TASK-1"}
		if os.Getenv("MUNSU_HANDOFF_CRASH_AFTER") != "journal" {
			keys = []string{"TASK-1", "TASK-2"}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), keys); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	srcBacklog := filepath.Join(parent, "data", "backlog.md")
	dstBacklog := filepath.Join(sm, "data", "backlog.md")
	if err := os.WriteFile(srcBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstBacklog, []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(parent, "tasks-axi")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
if [ "$1" = show ]; then echo 'state: queued'; exit 0; fi
if [ "$1" = mv ]; then
  to=""; file=""
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
exit 2
`), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	captainLookPath = func(string) (string, error) { return fake, nil }
	isTasksAxiBackend = func(string) bool { return true }
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=journal",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"PATH="+filepath.Dir(fake)+":"+os.Getenv("PATH"),
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 92 {
		t.Fatalf("helper exited at wrong boundary: %v\n%s", err, output)
	}

	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("reading durable handoff journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one crash-resumable journal", len(entries))
	}
	mustCurrentAggregate(t, parent, "TASK-1")
	mustNoCurrentAggregate(t, sm, "TASK-1")

	// Resuming re-runs the full saga; the destination receive replays the
	// same durable intent without duplicate task creation.
	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("resume handoff: %v", err)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
	agg := mustCurrentAggregate(t, sm, "TASK-1")
	if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination aggregate = %+v, want generation 7 captain owner", agg)
	}
	view := mustStoreView(t, sm)
	if len(view.Aggregates) != 1 {
		t.Fatalf("destination aggregates = %d, want exactly one (no duplicate creation)", len(view.Aggregates))
	}
}

func TestHandoffRecoveryRejectsCorruptStageAndRetainsJournal(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captain")
	if err := os.MkdirAll(filepath.Join(parent, "state", taskHandoffDirName, "tx", "stage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(parent, "state", taskHandoffDirName, "tx", "stage", "post")
	if err := os.WriteFile(stagePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := &taskHandoffJournal{
		Version: 3, ID: "tx", Phase: "commit-decided", SourceHome: parent, DestinationHome: captain,
		SourceBacklogPost: handoffFile{Target: "data/backlog.md", Stage: "stage/post", Exists: true, Mode: 0644, Size: 5, SHA256: digestHandoff([]byte("valid"))},
		DestBacklogPost:   handoffFile{Target: "data/backlog.md", Stage: "stage/post", Exists: true, Mode: 0644, Size: 5, SHA256: digestHandoff([]byte("valid"))},
	}
	if err := writeHandoffJournal(filepath.Join(parent, "state", taskHandoffDirName, "tx"), journal); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected corrupt stage recovery failure")
	}
	if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName, "tx", "journal.json")); err != nil {
		t.Fatalf("corrupt journal was not retained: %v", err)
	}
}

func TestHandoffRecoveryRejectsStaleSourceBeforeCommit(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captain")
	backlog := filepath.Join(parent, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlog), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlog, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	pre, err := inventoryHandoffFile(parent, backlog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlog, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	journal := &taskHandoffJournal{Version: 3, ID: "stale", Phase: "prepared", SourceHome: parent, DestinationHome: captain, SourceBacklogPre: pre}
	if err := os.MkdirAll(filepath.Join(parent, "state", taskHandoffDirName, "stale", "stage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeHandoffJournal(filepath.Join(parent, "state", taskHandoffDirName, "stale"), journal); err != nil {
		t.Fatal(err)
	}
	if err := revalidateHandoff(journal); err == nil {
		t.Fatal("expected stale source rejection")
	}
	if _, err := os.Stat(backlog); err != nil {
		t.Fatal(err)
	}
}

func TestHandoffLocksOppositeOrdersFailFastWithoutDeadlock(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	results := make(chan error, 2)
	go func() {
		unlock, err := acquireHandoffLocks(a, b)
		if unlock != nil {
			defer unlock()
		}
		results <- err
	}()
	go func() {
		unlock, err := acquireHandoffLocks(b, a)
		if unlock != nil {
			defer unlock()
		}
		results <- err
	}()
	for range 2 {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("opposite ordered handoff locks deadlocked")
		}
	}
}

func TestPublicDestinationWorkflowsRecoverCommittedHandoff(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captain, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set(captain, "parent-home", parent); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedHandoffTaskV2Default(t, parent, "TASK-2")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captain, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	sourceBacklog := filepath.Join(parent, "data", "backlog.md")
	destinationBacklog := filepath.Join(captain, "data", "backlog.md")
	if err := os.WriteFile(sourceBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n- [ ] TASK-2 - TASK-2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationBacklog, []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := fakeMovingTasksAxi(t, parent)
	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	captainLookPath = func(string) (string, error) { return fake, nil }
	isTasksAxiBackend = func(string) bool { return true }
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation$", "--")
	cmd.Env = append(os.Environ(), "MUNSU_HANDOFF_CRASH_HELPER=1", "MUNSU_HANDOFF_CRASH_AFTER=commit-decided", "MUNSU_HANDOFF_SOURCE="+parent, "MUNSU_HANDOFF_DEST="+captain, "MUNSU_HANDOFF_TASKS_AXI="+fake)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected committed handoff helper to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	if err := Scaffold(ScaffoldOptions{HomeDir: captain, ID: "TASK-1", Repo: "munsu", Mode: "local-only"}); err != nil {
		t.Fatalf("scaffold recovery: %v", err)
	}
	runner := NewRunner(Args{ID: "TASK-1", ProjectName: "munsu", HomeDir: captain, Mode: "local-only"})
	runner.homeDir = captain
	runner.effectiveMode = "local-only"
	// preflightBrief triggers handoff recovery of the interrupted journal and
	// converges the destination to a single owner.
	if err := runner.preflightBrief(); err != nil {
		t.Fatalf("brief preflight recovery: %v", err)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
	agg := mustCurrentAggregate(t, captain, "TASK-1")
	if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination authority after public recovery = %+v", agg)
	}
	mustNoPendingHandoffJournals(t, parent)
}

func TestRecoverTaskHandoffsRejectsMalformedParentAndHeldLock(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "config", "parent-home"), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(homeDir); err == nil {
		t.Fatal("expected malformed parent-home rejection")
	}
	parent := t.TempDir()
	captain := t.TempDir()
	unlock, err := acquireHandoffLocks(parent, captain)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected recovery to fail while source lock is held")
	}
}

func TestHandoffFailureLeavesTaskAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2(t, parent, "TASK-1", map[string]string{"description": "Preserve authority", "kind": "ship", "project": "munsu", "issue": "#384", "delivery_plan": "no-mistakes", "context_hints": "read AGENTS.md", "generation": "7"})
	beforeAgg := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "v1", "aggregates", "TASK-1", "7.json"))
	beforeMeta := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.meta"))
	beforeStatus := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.status"))

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) {
		return filepath.Join(parent, "missing-tasks-axi"), nil
	}

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected handoff failure")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "v1", "aggregates", "TASK-1", "7.json")); !bytes.Equal(got, beforeAgg) {
		t.Fatal("source aggregate changed after failed handoff")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.meta")); !bytes.Equal(got, beforeMeta) {
		t.Fatal("source meta changed after failed handoff")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.status")); !bytes.Equal(got, beforeStatus) {
		t.Fatal("source status changed after failed handoff")
	}
	mustNoCurrentAggregate(t, sm, "TASK-1")
	if _, err := mhome.ReadMeta(sm, "TASK-1"); err == nil {
		t.Fatal("destination meta exists after failed handoff")
	}
}

func TestHandoffProjectionPreflightFailureLeavesTaskAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	if err := mhome.WriteMeta(sm, "TASK-1", map[string]string{"description": "stale destination projection"}); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeQueuedTasksAxi(t, parent), nil }

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected projection collision failure")
	}
	sourceAgg := mustCurrentAggregate(t, parent, "TASK-1")
	if sourceAgg.Definition.Owner != "general" || sourceAgg.Generation != 7 {
		t.Fatalf("source aggregate = %+v, want untouched", sourceAgg)
	}
	mustNoCurrentAggregate(t, sm, "TASK-1")
	sourceMeta, err := mhome.ReadMeta(parent, "TASK-1")
	if err != nil || sourceMeta["description"] != "TASK-1" {
		t.Fatalf("source meta = %+v err=%v", sourceMeta, err)
	}
}

func TestHandoffCrashBoundariesResumeToSingleDestinationAuthority(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		boundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
		handoffCrashHook = func(got string) {
			if got == boundary {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
			os.Exit(0)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(91)
	}
	for _, boundary := range []string{"staging", "prepared", "commit-decided", "destination-receipt", "source-authority", "artifacts", "between-authority", "completed"} {
		t.Run(boundary, func(t *testing.T) {
			parent := t.TempDir()
			sm := filepath.Join(parent, "captains", "test-sm")
			if err := os.MkdirAll(sm, 0755); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(sm, "test-sm"); err != nil {
				t.Fatal(err)
			}
			seedHandoffTaskV2Default(t, parent, "TASK-1")
			seedHandoffTaskV2Default(t, parent, "TASK-2")
			if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
				t.Fatal(err)
			}
			srcBacklog := filepath.Join(parent, "data", "backlog.md")
			dstBacklog := filepath.Join(sm, "data", "backlog.md")
			if err := os.WriteFile(srcBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - work 1\n- [ ] TASK-2 - work 2\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dstBacklog, []byte("# Backlog\n\n"), 0644); err != nil {
				t.Fatal(err)
			}
			beforeSrcBacklog := mustReadFleetTestFile(t, srcBacklog)
			beforeDstBacklog := mustReadFleetTestFile(t, dstBacklog)

			origPath := captainLookPath
			origBackend := isTasksAxiBackend
			defer func() {
				captainLookPath = origPath
				isTasksAxiBackend = origBackend
			}()
			isTasksAxiBackend = func(string) bool { return true }
			fake := fakeMovingTasksAxi(t, parent)
			captainLookPath = func(name string) (string, error) { return fake, nil }
			cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashBoundariesResumeToSingleDestinationAuthority$", "--")
			cmd.Env = append(os.Environ(), "MUNSU_HANDOFF_CRASH_HELPER=1", "MUNSU_HANDOFF_CRASH_AFTER="+boundary, "MUNSU_HANDOFF_SOURCE="+parent, "MUNSU_HANDOFF_DEST="+sm, "MUNSU_HANDOFF_TASKS_AXI="+fake)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatal("expected crash helper to exit abruptly")
			} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
				t.Fatalf("boundary %s exited unexpectedly: %v\n%s", boundary, err, output)
			}
			// Task 6.2 ordering: the destination receives the complete Task
			// Generation (one Store transaction with a durable receipt) before
			// source retirement. At the artifacts boundary the destination
			// authority is therefore already committed while the projection
			// copies have run; source ownership is already retired.
			if boundary == "artifacts" {
				for _, taskID := range []string{"TASK-1", "TASK-2"} {
					agg := mustCurrentAggregate(t, sm, taskID)
					if agg.Definition.Owner != "captain:test-sm" || agg.Generation != 7 {
						t.Fatalf("destination authority for %s at artifacts = %+v", taskID, agg)
					}
					mustNoCurrentAggregate(t, parent, taskID)
				}
			}
			if boundary == "staging" || boundary == "prepared" {
				if got := mustReadFleetTestFile(t, srcBacklog); !bytes.Equal(got, beforeSrcBacklog) {
					t.Fatalf("precommit source backlog changed at %s", boundary)
				}
				if got := mustReadFleetTestFile(t, dstBacklog); !bytes.Equal(got, beforeDstBacklog) {
					t.Fatalf("precommit destination backlog changed at %s", boundary)
				}
				if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
					t.Fatalf("resume at %s: %v", boundary, err)
				}
			} else if err := RecoverTaskHandoffs(parent); err != nil {
				t.Fatalf("recover at %s: %v", boundary, err)
			}
			if got := mustReadFleetTestFile(t, dstBacklog); len(got) == 0 || bytes.Equal(got, beforeDstBacklog) {
				t.Fatalf("destination backlog did not converge at %s", boundary)
			}
			for _, taskID := range []string{"TASK-1", "TASK-2"} {
				mustNoCurrentAggregate(t, parent, taskID)
				agg := mustCurrentAggregate(t, sm, taskID)
				if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" {
					t.Fatalf("destination aggregate for %s at %s = %+v", taskID, boundary, agg)
				}
			}
			view := mustStoreView(t, sm)
			if len(view.Aggregates) != 2 {
				t.Fatalf("destination aggregates at %s = %d, want exactly two (no duplicates)", boundary, len(view.Aggregates))
			}
		})
	}
}

func TestHandoffTransfersCompleteTaskGenerationAndDestinationIsReady(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"description": "Implement handoff", "kind": "ship", "project": "munsu", "repo": "munsu", "issue": "#384", "issue_links": "#384,#383", "delivery_plan": "no-mistakes", "context_hints": "read AGENTS.md", "generation": "7"}
	seedHandoffTaskV2(t, parent, "TASK-1", meta)
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - Implement handoff\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeMovingTasksAxi(t, parent), nil }

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatal(err)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
	got := mustCurrentAggregate(t, sm, "TASK-1")
	if got.Generation != 7 || got.Definition.Owner != "captain:test-sm" || got.Phase != taskauthority.PhaseQueued {
		t.Fatalf("destination aggregate = %+v, want complete generation 7 queued under captain", got)
	}
	if got.Definition.Description != "Implement handoff" || got.Definition.Kind != "ship" || got.Definition.Project != "munsu" {
		t.Fatalf("destination aggregate lost definition: %+v", got.Definition)
	}
	if got.DispatchInterpretationID == "" || got.DispatchInterpretationDigest == "" {
		t.Fatalf("destination aggregate lost dispatch interpretation binding: %+v", got)
	}
	view := mustStoreView(t, sm)
	var interpretation *taskauthority.DispatchInterpretation
	for i := range view.Interpretations {
		if view.Interpretations[i].ID == got.DispatchInterpretationID {
			interpretation = &view.Interpretations[i]
			break
		}
	}
	if interpretation == nil {
		t.Fatalf("destination interpretations = %+v, want transferred record %s", view.Interpretations, got.DispatchInterpretationID)
	}
	if interpretation.DependencySnapshotDigest != got.DispatchInterpretationDigest {
		t.Fatalf("destination interpretation digest mismatch: %+v vs %s", interpretation, got.DispatchInterpretationDigest)
	}
	// The transferred generation's typed audit history is present at the
	// destination alongside the receive audit.
	historySeen := false
	receiveSeen := false
	for _, ev := range view.Audit {
		if ev.TaskID == "TASK-1" && ev.Generation == 7 && strings.Contains(ev.Reason, "transferred from") {
			receiveSeen = true
		}
		if ev.TaskID == "TASK-1" && ev.Generation == 7 && ev.OperationID != "" && strings.HasPrefix(ev.OperationID, "seed-") {
			historySeen = true
		}
	}
	if !historySeen {
		t.Fatalf("destination audit lost source history: %+v", view.Audit)
	}
	if !receiveSeen {
		t.Fatalf("destination audit missing receive event: %+v", view.Audit)
	}
	dstMeta, err := mhome.ReadMeta(sm, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range meta {
		if dstMeta[key] != want {
			t.Fatalf("destination meta[%s] = %q, want %q", key, dstMeta[key], want)
		}
	}
	status, err := mhome.ReadStatus(sm, "TASK-1")
	if err != nil || len(status) != 1 || status[0] != "queued: ready" {
		t.Fatalf("destination status = %#v err=%v", status, err)
	}
	if _, err := mhome.ReadMeta(parent, "TASK-1"); err == nil {
		t.Fatal("source meta still exists after successful handoff")
	}
	if !Exists(sm, "TASK-1") {
		t.Fatal("destination brief is not ready for spawn preflight")
	}
	if err := Scaffold(ScaffoldOptions{HomeDir: sm, ID: "TASK-1", Repo: "munsu", Mode: "local-only"}); err != nil {
		t.Fatalf("destination brief render failed: %v", err)
	}
	runner := NewRunner(Args{ID: "TASK-1", ProjectName: "munsu", HomeDir: sm, Mode: "local-only"})
	runner.homeDir = sm
	runner.effectiveMode = "local-only"
	if err := runner.preflightBrief(); err != nil {
		t.Fatalf("destination brief preflight failed: %v", err)
	}
}

// TestHandoffChildWithExistingParentDependencySucceeds pins reviewer probe E:
// handing off a child alone whose backlog blocked-by references an
// in-source parent must succeed. The parent exists in canonical source state
// but is outside the requested set, so the dependency edge is not material
// ambiguity.
func TestHandoffChildWithExistingParentDependencySucceeds(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedHandoffTaskV2Default(t, parent, "TASK-PARENT")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeBlockedTasksAxi(t, parent), nil }

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("handoff of child with in-source parent dependency failed: %v", err)
	}
	child := mustCurrentAggregate(t, sm, "TASK-1")
	if child.Generation != 7 {
		t.Fatalf("destination child = %+v, want generation 7", child)
	}
	mustCurrentAggregate(t, parent, "TASK-PARENT")
}

// fakeBlockedTasksAxi reports a queued child whose backlog entry is blocked by
// TASK-PARENT, and moves like fakeMovingTasksAxi.
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

func mustReadFleetTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestHandoffIntentBindsTaskIdentityDurably crashes immediately after the
// durable journal (now carrying one validated transfer intent and the complete
// Task Generation payload per task) is written and proves the intent binds
// source/destination home identity, Task ID, exact Generation, request digest,
// and both stable Operation IDs (ADR-0007 §10, Task 6.1 acceptance 1).
func TestHandoffIntentBindsTaskIdentityDurably(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		handoffCrashHook = func(got string) {
			if got == "journal" {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	seedHandoffTaskV2Default(t, parent, "TASK-2")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n- [ ] TASK-2 - TASK-2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	fake := fakeMovingTasksAxi(t, parent)
	captainLookPath = func(name string) (string, error) { return fake, nil }

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffIntentBindsTaskIdentityDurably$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=journal",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("reading durable handoff journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one crash-resumable journal", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(journalRoot, entries[0].Name(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal taskHandoffJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.Version != 3 {
		t.Fatalf("journal version = %d, want 3", journal.Version)
	}
	if len(journal.Tasks) != 2 {
		t.Fatalf("journal tasks = %d, want 2", len(journal.Tasks))
	}
	canonicalSource, err := canonicalHandoffHome(parent)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDest, err := canonicalHandoffHome(sm)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range journal.Tasks {
		intent := task.Intent
		if err := intent.Validate(); err != nil {
			t.Fatalf("intent for %s invalid: %v", task.ID, err)
		}
		if intent.SourceHome != canonicalSource || intent.DestinationHome != canonicalDest {
			t.Fatalf("intent homes = %s -> %s, want %s -> %s", intent.SourceHome, intent.DestinationHome, canonicalSource, canonicalDest)
		}
		if intent.TaskID != task.ID {
			t.Fatalf("intent task = %s, want %s", intent.TaskID, task.ID)
		}
		if intent.Generation.String() != "7" || intent.Generation.String() != task.Generation {
			t.Fatalf("intent generation = %s, want 7", intent.Generation)
		}
		if task.Payload.Aggregate.TaskID != task.ID || task.Payload.Aggregate.Generation != intent.Generation {
			t.Fatalf("payload aggregate = %+v, want %s generation %s", task.Payload.Aggregate, task.ID, intent.Generation)
		}
		wantDigest, err := taskauthority.TransferRequest{SourceHome: canonicalSource, DestinationHome: canonicalDest, TaskID: task.ID, Generation: 7}.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if intent.RequestDigest != wantDigest {
			t.Fatalf("intent digest = %s, want %s", intent.RequestDigest, wantDigest)
		}
		if !strings.HasPrefix(intent.SourceOperationID, "handoff-source-") || !strings.HasPrefix(intent.DestinationOperationID, "handoff-dest-") {
			t.Fatalf("intent operation ids = %s / %s, want handoff-source-/handoff-dest- prefixes", intent.SourceOperationID, intent.DestinationOperationID)
		}
		if intent.SourceOperationID == intent.DestinationOperationID {
			t.Fatal("source and destination operation ids must differ")
		}
	}

	// The crash happened before any source mutation: source ownership stays
	// current and the destination owns nothing.
	mustCurrentAggregate(t, parent, "TASK-1")
	mustNoCurrentAggregate(t, sm, "TASK-1")

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
		t.Fatalf("resume handoff: %v", err)
	}
	for _, taskID := range []string{"TASK-1", "TASK-2"} {
		mustNoCurrentAggregate(t, parent, taskID)
		mustCurrentAggregate(t, sm, taskID)
	}
}

// TestHandoffSourcePreservedUntilDestinationReceipt crashes after the
// destination receive commits its durable Store receipt and before any source
// mutation, and proves the source remains current until the destination
// receipt is durable (Task 6.1 ordering invariant preserved by the v2 saga):
// at the boundary the destination owns the complete Task Generation (received
// in one Store transaction) while the source still owns it, and recovery
// converges to a single destination owner.
func TestHandoffSourcePreservedUntilDestinationReceipt(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		handoffCrashHook = func(got string) {
			if got == "destination-receipt" {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1"}); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	fake := fakeMovingTasksAxi(t, parent)
	captainLookPath = func(name string) (string, error) { return fake, nil }

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffSourcePreservedUntilDestinationReceipt$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=destination-receipt",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	// Source ownership is still current: the destination Store receipt is
	// durable (the receive committed in one transaction) but the source
	// aggregate has not been mutated.
	sourceAgg := mustCurrentAggregate(t, parent, "TASK-1")
	if sourceAgg.Definition.Owner != "general" {
		t.Fatalf("source aggregate = %+v, want untouched general owner", sourceAgg)
	}
	destAgg := mustCurrentAggregate(t, sm, "TASK-1")
	if destAgg.Generation != 7 || destAgg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination aggregate = %+v, want complete generation 7 under captain", destAgg)
	}
	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(journalRoot, entries[0].Name(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal taskHandoffJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.Phase != "commit-decided" {
		t.Fatalf("journal phase = %q, want commit-decided", journal.Phase)
	}
	if len(journal.Tasks) != 1 {
		t.Fatalf("journal tasks = %d, want 1", len(journal.Tasks))
	}
	intent := journal.Tasks[0].Intent
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	// The durable destination receipt is the Authority Store receipt for the
	// receive operation, pinned to the destination Operation ID and the
	// request digest.
	view := mustStoreView(t, sm)
	var receipt *taskauthority.Receipt
	for i := range view.Receipts {
		if view.Receipts[i].OperationID == intent.DestinationOperationID {
			receipt = &view.Receipts[i]
			break
		}
	}
	if receipt == nil {
		t.Fatalf("destination Store receipt missing for %s: %+v", intent.DestinationOperationID, view.Receipts)
	}
	if receipt.Digest != intent.RequestDigest || receipt.TaskID != "TASK-1" || receipt.Generation != 7 {
		t.Fatalf("receipt = %+v, want binding to destination operation and request digest", receipt)
	}

	// Recovery re-commits the same receipt idempotently and completes the
	// transfer to a single destination owner without duplicate creation.
	if err := RecoverTaskHandoffs(parent); err != nil {
		t.Fatalf("recover after destination receipt: %v", err)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
	agg := mustCurrentAggregate(t, sm, "TASK-1")
	if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination TASK-1 after recovery = %+v", agg)
	}
	after := mustStoreView(t, sm)
	if len(after.Aggregates) != 1 || len(after.Receipts) != 1 {
		t.Fatalf("recovery duplicated destination state: aggregates=%d receipts=%d", len(after.Aggregates), len(after.Receipts))
	}
}

// TestHandoffProjectionFailureReturnsPartialAndPreservesOwnership proves a
// post-receive projection copy failure returns a typed partial result and
// never rolls back or re-transfers ownership (Task 6.2 criterion 3): the
// destination keeps the received Task Generation, the source stays retired,
// and the pending journal converges on the next recovery.
func TestHandoffProjectionFailureReturnsPartialAndPreservesOwnership(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	seedHandoffTaskV2Default(t, parent, "TASK-1")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeMovingTasksAxi(t, parent), nil }

	restore := SetHandoffProjectionFailHookForTest(func(target string) error {
		if target == "state/TASK-1.meta" {
			return errors.New("injected projection failure")
		}
		return nil
	})

	err := Handoff(parent, sm, []string{"TASK-1"})
	var partial *HandoffPartialError
	if !errors.As(err, &partial) {
		t.Fatalf("Handoff err = %v, want typed HandoffPartialError", err)
	}
	restore()
	if !reflect.DeepEqual(partial.Transferred, []string{"TASK-1"}) {
		t.Fatalf("partial.Transferred = %v, want [TASK-1]", partial.Transferred)
	}
	// Ownership truth is unchanged by the projection failure: the destination
	// owns the complete generation, the source is retired, nothing was
	// rolled back.
	agg := mustCurrentAggregate(t, sm, "TASK-1")
	if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination aggregate after projection failure = %+v", agg)
	}
	mustNoCurrentAggregate(t, parent, "TASK-1")
	if _, err := mhome.ReadMeta(sm, "TASK-1"); err == nil {
		t.Fatal("destination meta installed despite injected projection failure")
	}

	// The pending journal converges on the next recovery: the receive replays
	// idempotently and the projection copies retry.
	if err := RecoverTaskHandoffs(parent); err != nil {
		t.Fatalf("recover after projection failure: %v", err)
	}
	if _, err := mhome.ReadMeta(sm, "TASK-1"); err != nil {
		t.Fatalf("destination meta missing after recovery convergence: %v", err)
	}
	mustCurrentAggregate(t, sm, "TASK-1")
	mustNoCurrentAggregate(t, parent, "TASK-1")
	mustNoPendingHandoffJournals(t, parent)
}

// mustNoPendingHandoffJournals fails when any handoff journal entry remains
// under the home's state dir.
func mustNoPendingHandoffJournals(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, "state", taskHandoffDirName)
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending handoff journals remain: %v", entries)
	}
}

// TestHandoffFailsClosedOnV1Homes proves the Task 6.2 cutover operates only on
// v2 homes: a source or destination that still carries legacy v1 task
// authority records fails closed with the typed migration-required error
// before any staging, with no silent v1 fallback.
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
			if err := os.MkdirAll(sm, 0755); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(sm, "test-sm"); err != nil {
				t.Fatal(err)
			}
			if tc.seedSource {
				writeV1AggregateDoc(t, parent, "TASK-1", "general", "legacy")
			}
			if tc.seedDest {
				writeV1AggregateDoc(t, sm, "TASK-1", "captain:test-sm", "legacy")
			}
			origPath := captainLookPath
			origBackend := isTasksAxiBackend
			defer func() {
				captainLookPath = origPath
				isTasksAxiBackend = origBackend
			}()
			isTasksAxiBackend = func(string) bool { return true }
			captainLookPath = func(name string) (string, error) { return fakeQueuedTasksAxi(t, parent), nil }

			err := Handoff(parent, sm, []string{"TASK-1"})
			if err == nil {
				t.Fatal("expected migration-required failure on v1 home")
			}
			if !errors.Is(err, taskauthorityfs.ErrMigrationRequired) {
				t.Fatalf("Handoff err = %v, want typed migration-required", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName)); !os.IsNotExist(err) {
				t.Fatal("v1 fail-closed left handoff journal state behind")
			}
		})
	}
}

// TestHandoffConflictDestinationOwnerFailsClosed proves a destination that
// already owns the same Task ID/Generation (or a newer generation) quarantines
// the transfer with a typed conflict and never overwrites destination truth
// (Task 6.1 acceptance 4, re-fenced by the receive operation).
func TestHandoffConflictDestinationOwnerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		destGen uint64
	}{
		{"same generation", 7},
		{"newer generation", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			// The destination must sit outside the source's captains/ tree:
			// the fleet candidate-owner resolution scans captains/ and would
			// report the two TASK-1 owners as ambiguous before the conflict
			// check runs.
			sm := filepath.Join(parent, "test-sm")
			if err := os.MkdirAll(sm, 0755); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(sm, "test-sm"); err != nil {
				t.Fatal(err)
			}
			seedHandoffTaskV2(t, parent, "TASK-1", map[string]string{"description": "source truth", "generation": "7"})
			destAuth := mustAuthority(t, sm)
			actor := taskauthority.Actor{ID: "captain:test-sm", Rank: "captain"}
			if _, err := destAuth.Create(taskauthority.CreateRequest{OperationID: "dest-seed-create", Actor: actor, TaskID: "TASK-1", Owner: "captain:test-sm", Description: "destination truth", Kind: "ship", Project: "munsu", Reason: "seed"}); err != nil {
				t.Fatal(err)
			}
			gen := taskauthority.Generation(1)
			for gen < taskauthority.Generation(tc.destGen) {
				if _, err := destAuth.Complete(taskauthority.CompleteRequest{OperationID: fmt.Sprintf("dest-seed-complete-%d", gen), Actor: actor, TaskID: "TASK-1", ExpectedGeneration: gen, To: taskauthority.PhaseDone, Reason: "seed"}); err != nil {
					t.Fatal(err)
				}
				if _, err := destAuth.Reopen(taskauthority.ReopenRequest{OperationID: fmt.Sprintf("dest-seed-reopen-%d", gen), Actor: actor, TaskID: "TASK-1", ExpectedGeneration: gen, Reason: "seed"}); err != nil {
					t.Fatal(err)
				}
				gen++
			}
			beforeDestAgg, err := destAuth.Get("TASK-1")
			if err != nil {
				t.Fatal(err)
			}

			origPath := captainLookPath
			origBackend := isTasksAxiBackend
			defer func() {
				captainLookPath = origPath
				isTasksAxiBackend = origBackend
			}()
			isTasksAxiBackend = func(string) bool { return true }
			captainLookPath = func(name string) (string, error) { return fakeQueuedTasksAxi(t, parent), nil }

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
			destAgg, err := destAuth.Get("TASK-1")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(destAgg, beforeDestAgg) {
				t.Fatalf("destination aggregate = %+v, want untouched %+v", destAgg, beforeDestAgg)
			}
		})
	}
}
