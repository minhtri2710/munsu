package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// mustTransferTaskID returns a validated task identity for tests.
func mustTransferTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%q): %v", value, err)
	}
	return id
}

// mustTransferProjectID returns a validated project identity for tests.
func mustTransferProjectID(t *testing.T, value string) domain.ProjectID {
	t.Helper()
	id, err := domain.NewProjectID(value)
	if err != nil {
		t.Fatalf("NewProjectID(%q): %v", value, err)
	}
	return id
}

// mustTransferOp builds a validated Operation from a typed intent.
func mustTransferOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	op, err := domain.NewOperation(mustTransferOperationID(t, id), intent)
	if err != nil {
		t.Fatalf("NewOperation(%q): %v", id, err)
	}
	return op
}

func mustTransferOperationID(t *testing.T, value string) domain.OperationID {
	t.Helper()
	id, err := domain.NewOperationID(value)
	if err != nil {
		t.Fatalf("NewOperationID(%q): %v", value, err)
	}
	return id
}

// seedCanonicalTransferHome initializes a canonical home and returns its
// canonical Authority.
func seedCanonicalTransferHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	h, err := mhome.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// seedCanonicalQueuedTask creates one queued task in a canonical home.
func seedCanonicalQueuedTask(t *testing.T, c *taskauthority.Canonical, taskID, owner string) {
	t.Helper()
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      mustTransferTaskID(t, taskID),
		Owner:       owner,
		Description: "work on " + taskID,
		Kind:        "ship",
		Project:     mustTransferProjectID(t, "munsu"),
		Reason:      "test seed",
	}
	if _, err := c.Create(mustTransferOp(t, "seed-create-"+taskID, req), req); err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
}

// mustTransferOwner reads the current aggregate of a task from a home, failing
// the test when the home does not own it.
func mustTransferOwner(t *testing.T, homeDir, taskID string) taskauthority.Aggregate {
	t.Helper()
	c, err := canonicalAuthorityForTest(t, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c.Get(mustTransferTaskID(t, taskID))
	if err != nil {
		t.Fatalf("home %s does not own %s: %v", homeDir, taskID, err)
	}
	return agg
}

// mustTransferNoOwner fails the test when the home still owns the task.
func mustTransferNoOwner(t *testing.T, homeDir, taskID string) {
	t.Helper()
	c, err := canonicalAuthorityForTest(t, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(mustTransferTaskID(t, taskID)); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("home %s still owns %s: %v", homeDir, taskID, err)
	}
}

// canonicalAuthorityForTest opens the canonical Authority of a home.
func canonicalAuthorityForTest(t *testing.T, homeDir string) (*taskauthority.Canonical, error) {
	t.Helper()
	h, err := mhome.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return taskauthority.NewCanonical(h)
}

// seedHandoffPair initializes a canonical parent home and a canonical captain
// home (with provenance), returning their paths.
func seedHandoffPair(t *testing.T) (parent, captain string) {
	t.Helper()
	parent = t.TempDir()
	seedCanonicalTransferHome(t, parent)
	captain = filepath.Join(parent, "captains", "test-sm")
	seedCanonicalTransferHome(t, captain)
	if err := SeedProvenance(captain, "test-sm"); err != nil {
		t.Fatal(err)
	}
	return parent, captain
}

// pendingJournalCount returns the number of pending transfer journals at a
// home's state root.
func pendingJournalCount(t *testing.T, homeDir string) int {
	t.Helper()
	root := filepath.Join(homeDir, "state", taskHandoffDirName)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestHandoffTransfersQueuedTaskToCaptain(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")

	if err := Handoff(parent, captain, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	// The destination owns the re-created generation; the source is superseded.
	agg := mustTransferOwner(t, captain, "TASK-1")
	if agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination owner = %q, want captain:test-sm", agg.Definition.Owner)
	}
	if agg.Generation != 1 {
		t.Fatalf("destination generation = %d, want 1 (one-to-one receive)", agg.Generation)
	}
	mustTransferNoOwner(t, parent, "TASK-1")
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("pending journal remains after successful transfer")
	}
}

func TestHandoffRefusesNonQueuedTask(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	c := mustAuthority(t, parent)
	seedCanonicalQueuedTask(t, c, "TASK-1", "general")
	// Advance the task out of queued so it is not transferable.
	start := taskauthority.CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTransferTaskID(t, "TASK-1"),
		Precondition: domain.Of(1, 1),
		Reason:       "start",
	}
	if _, err := c.Start(mustTransferOp(t, "op-start-1", start), start); err != nil {
		t.Fatal(err)
	}

	err := Handoff(parent, captain, []string{"TASK-1"})
	if err == nil || !strings.Contains(err.Error(), "only queued tasks may be transferred") {
		t.Fatalf("Handoff error = %v, want queued-only refusal", err)
	}
	mustTransferOwner(t, parent, "TASK-1")
	mustTransferNoOwner(t, captain, "TASK-1")
}

func TestHandoffDestinationConflictFailsClosed(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")
	// The destination already owns the task but is NOT under the source's
	// captains tree, so it is not discovered as a candidate owner and the
	// explicit destination-owner conflict fires.
	captain := filepath.Join(t.TempDir(), "captain")
	seedCanonicalTransferHome(t, captain)
	if err := SeedProvenance(captain, "captain"); err != nil {
		t.Fatal(err)
	}
	seedCanonicalQueuedTask(t, mustAuthority(t, captain), "TASK-1", "captain:captain")

	err := Handoff(parent, captain, []string{"TASK-1"})
	if err == nil || !strings.Contains(err.Error(), "destination already has current authority") {
		t.Fatalf("Handoff error = %v, want destination-owner conflict", err)
	}
	// Source ownership remains.
	mustTransferOwner(t, parent, "TASK-1")
}

func TestHandoffRefusesUnmarkedDestination(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	sm := filepath.Join(parent, "captains", "test-sm")
	// No provenance marker.
	os.MkdirAll(sm, 0755)

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil || !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Fatalf("Handoff error = %v, want unmarked-home refusal", err)
	}
}

// TestHandoffCrashRecoveryConvergesEveryStage crashes a durable transfer at
// each stage (after journal intent, after reserve, after receive, after
// commit, after activate) and proves recovery resumes the SAME Transfer with
// one truthful owner and no contradictory dual ownership.
func TestHandoffCrashRecoveryConvergesEveryStage(t *testing.T) {
	for _, boundary := range []string{"journal", "reserved", "received", "committed", "activated"} {
		t.Run(boundary, func(t *testing.T) {
			if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
				crashBoundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
				handoffCrashHook = func(got string) {
					if got == crashBoundary {
						os.Exit(92)
					}
				}
				if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
					os.Exit(0)
				}
				os.Exit(91)
			}

			parent, captain := seedHandoffPair(t)
			parentAuth := mustAuthority(t, parent)
			seedCanonicalQueuedTask(t, parentAuth, "TASK-1", "general")
			seedCanonicalQueuedTask(t, parentAuth, "TASK-2", "general")

			cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashRecoveryConvergesEveryStage$", "--")
			cmd.Env = append(os.Environ(),
				"MUNSU_HANDOFF_CRASH_HELPER=1",
				"MUNSU_HANDOFF_CRASH_AFTER="+boundary,
				"MUNSU_HANDOFF_SOURCE="+parent,
				"MUNSU_HANDOFF_DEST="+captain,
			)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("expected helper subprocess to crash at %s", boundary)
			} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
				t.Fatalf("helper exited at boundary %s: %v\n%s", boundary, err, output)
			}

			// The journal must be durable and the source still owns the task
			// (no side effect happened before the journal intent).
			if pendingJournalCount(t, parent) != 1 {
				t.Fatalf("boundary %s: want one pending journal", boundary)
			}

			// Recovery must not report both homes as current owners.
			if err := RecoverTaskHandoffs(parent); err != nil {
				t.Fatalf("boundary %s: RecoverTaskHandoffs: %v", boundary, err)
			}

			for _, taskID := range []string{"TASK-1", "TASK-2"} {
				mustTransferNoOwner(t, parent, taskID)
				agg := mustTransferOwner(t, captain, taskID)
				if agg.Generation != 1 {
					t.Fatalf("boundary %s: %s destination generation = %d, want 1 (no duplicate)", boundary, taskID, agg.Generation)
				}
				if agg.Definition.Owner != "captain:test-sm" {
					t.Fatalf("boundary %s: %s owner = %q", boundary, taskID, agg.Definition.Owner)
				}
			}
			if pendingJournalCount(t, parent) != 0 {
				t.Fatalf("boundary %s: journal not removed after recovery", boundary)
			}
		})
	}
}

func TestHandoffRecoveryRejectsCorruptJournal(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	dir := filepath.Join(parent, "state", taskHandoffDirName, "bad-transfer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected corrupt journal to fail closed")
	}
	// The corrupt journal is retained for inspection.
	if _, err := os.Stat(filepath.Join(dir, "journal.json")); err != nil {
		t.Fatalf("corrupt journal removed: %v", err)
	}
}

func TestHandoffSourcePreservedUntilDestinationNoSideEffect(t *testing.T) {
	// A crash exactly after the journal intent but before any side effect must
	// leave the source owning the task and the destination empty.
	parent, captain := seedHandoffPair(t)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")

	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		handoffCrashHook = func(got string) {
			if got == "journal" {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1"}); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffSourcePreservedUntilDestinationNoSideEffect$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+captain,
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper crash")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, out)
	}

	// Source still owns the task; destination does not — no side effect before
	// the durable journal intent.
	mustTransferOwner(t, parent, "TASK-1")
	mustTransferNoOwner(t, captain, "TASK-1")
}

func TestHandoffReopenReplaysSameTransfer(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")

	if err := Handoff(parent, captain, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	// Reopen both homes from scratch and re-read the canonical truth.
	parentC, err := canonicalAuthorityForTest(t, parent)
	if err != nil {
		t.Fatal(err)
	}
	captainC, err := canonicalAuthorityForTest(t, captain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parentC.Get(mustTransferTaskID(t, "TASK-1")); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("source still owns after reopen: %v", err)
	}
	agg, err := captainC.Get(mustTransferTaskID(t, "TASK-1"))
	if err != nil {
		t.Fatalf("destination lost ownership across reopen: %v", err)
	}
	if !agg.Current || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("destination aggregate across reopen = %+v", agg)
	}
}

// mustAuthority opens the canonical Authority of a home, failing on error.
func mustAuthority(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	c, err := canonicalAuthorityForTest(t, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
