package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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

// pendingJournalCount returns the number of ACTIVE transfer journals at a
// home's state root, read through the production Home causal path (the
// bounded handoff index). Completed transfers are removed from the index, so
// this reflects only resumable (pending) transfers.
func pendingJournalCount(t *testing.T, homeDir string) int {
	t.Helper()
	h, err := mhome.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := readHandoffIndex(h)
	if err != nil {
		t.Fatal(err)
	}
	return len(idx.Active)
}

// completedJournalCount returns the number of retained terminal journal
// records (phase=completed) at a home's state root. Field records are never
// deleted; only their IDs leave the active index.
func completedJournalCount(t *testing.T, homeDir string) int {
	t.Helper()
	root := filepath.Join(homeDir, "state", taskHandoffDirName)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "index.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var j taskHandoffJournal
		if err := json.Unmarshal(data, &j); err != nil {
			t.Fatalf("unmarshal journal %s: %v", e.Name(), err)
		}
		if j.Phase == handoffPhaseCompleted {
			count++
		}
	}
	return count
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
	// The terminal journal record is retained as durable truth (never deleted).
	if completedJournalCount(t, parent) != 1 {
		t.Fatalf("terminal journal record not retained after successful transfer")
	}
}

// TestHandoffCarriesDeliveryContractToDestination proves a transferred task
// keeps its recorded delivery contract — including a recorded fallback — after
// the full journal → request → receive handoff. A drop at any wiring point
// (journal field, receive-loop request field) makes the destination read back
// a nil or bare contract.
func TestHandoffCarriesDeliveryContractToDestination(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	src := mustAuthority(t, parent)
	seedCanonicalQueuedTask(t, src, "TASK-1", "general")
	seedSourceFallbackContract(t, src, "TASK-1")

	if err := Handoff(parent, captain, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	agg := mustTransferOwner(t, captain, "TASK-1")
	if agg.DeliveryContract == nil {
		t.Fatal("destination dropped the delivery contract on handoff")
	}
	if agg.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("destination contract mode = %q, want direct-PR", agg.DeliveryContract.Mode)
	}
	fb := agg.DeliveryContract.Fallback
	if fb == nil {
		t.Fatal("destination dropped the fallback provenance on handoff")
	}
	if fb.From != "no-mistakes" || fb.To != "direct-PR" {
		t.Fatalf("destination fallback = %+v, want no-mistakes -> direct-PR", fb)
	}
}

// seedSourceFallbackContract records a no-mistakes contract on a queued task and
// then the authorized no-mistakes -> direct-PR fallback, leaving the task's
// canonical contract carrying full transition provenance.
func seedSourceFallbackContract(t *testing.T, c *taskauthority.Canonical, taskID string) {
	t.Helper()
	tid := mustTransferTaskID(t, taskID)
	agg, err := c.Get(tid)
	if err != nil {
		t.Fatalf("Get(%s): %v", taskID, err)
	}
	contractReq := taskauthority.CanonicalRecordDeliveryContractRequest{
		HomeID:       c.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Mode:         "no-mistakes",
		Reason:       "test seed contract",
	}
	if _, err := c.RecordDeliveryContract(mustTransferOp(t, "seed-contract-"+taskID, contractReq), contractReq); err != nil {
		t.Fatalf("RecordDeliveryContract(%s): %v", taskID, err)
	}
	agg, err = c.Get(tid)
	if err != nil {
		t.Fatalf("Get after contract(%s): %v", taskID, err)
	}
	fallbackReq := taskauthority.CanonicalRecordDeliveryFallbackRequest{
		HomeID:       c.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		From:         "no-mistakes",
		To:           "direct-PR",
		Reason:       "test seed fallback",
	}
	if _, err := c.RecordDeliveryFallback(mustTransferOp(t, "seed-fallback-"+taskID, fallbackReq), fallbackReq); err != nil {
		t.Fatalf("RecordDeliveryFallback(%s): %v", taskID, err)
	}
}

// TestHandoffDeliversUnderCarriedContractNotLiveInput closes the loop the
// transfer is meant to protect: the destination spawn resolves its delivery
// mode from the carried contract, not from live project inputs. The same
// captain home, given the transferred contract, delivers direct-PR; given no
// contract it would instead re-resolve to the live default — proving the
// carried contract, and not device/PATH/config drift, decides the next spawn.
func TestHandoffDeliversUnderCarriedContractNotLiveInput(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	src := mustAuthority(t, parent)
	seedCanonicalQueuedTask(t, src, "TASK-1", "general")
	seedSourceFallbackContract(t, src, "TASK-1")

	if err := Handoff(parent, captain, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	destAuth := mustAuthority(t, captain)
	destAgg, err := destAuth.Get(mustTransferTaskID(t, "TASK-1"))
	if err != nil {
		t.Fatalf("destination Get: %v", err)
	}
	if destAgg.DeliveryContract == nil {
		t.Fatal("destination dropped the delivery contract on handoff")
	}
	contract := destAgg.DeliveryContract
	if contract.Mode != "direct-PR" {
		t.Fatalf("carried contract mode = %q, want direct-PR", contract.Mode)
	}

	// Divergent live input: the captain's project overlay defaults to a mode
	// the transferred task was never contracted for.
	seedLiveDefaultDeliveryConfig(t, captain, "munsu", "local-only")

	args := Args{ID: "TASK-1", ProjectName: "munsu"}

	// With the carried contract, the next spawn delivers under the contract.
	carried, err := ResolveSpawnProjectConfig(captain, args, DispatchPolicyGeneralDirect, contract)
	if err != nil {
		t.Fatalf("ResolveSpawnProjectConfig with contract: %v", err)
	}
	if carried.Soldier.Mode != "direct-PR" {
		t.Fatalf("carried-contract spawn mode = %q, want carried direct-PR", carried.Soldier.Mode)
	}

	// Without it, the same home re-resolves to the live default — the behavior
	// the transfer must prevent.
	live, err := ResolveSpawnProjectConfig(captain, args, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatalf("ResolveSpawnProjectConfig without contract: %v", err)
	}
	if live.Soldier.Mode != "local-only" {
		t.Fatalf("live-default spawn mode = %q, want local-only", live.Soldier.Mode)
	}
}

// seedLiveDefaultDeliveryConfig registers a typed project overlay on a home
// whose default delivery mode differs from the task's carried contract, so the
// spawn tests exercise real re-resolution rather than a contrived identity.
func seedLiveDefaultDeliveryConfig(t *testing.T, homeDir, projectName, defaultMode string) {
	t.Helper()
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			Backend:        "tmux",
			SoldierHarness: "pi",
			Model:          "gpt-5",
			DefaultMode:    defaultMode,
		},
	}, []testProjectRecord{{Name: projectName, Path: t.TempDir()}}, nil)
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
				t.Fatalf("boundary %s: journal still active after recovery", boundary)
			}
			// The terminal journal record is retained (truth, never deleted) and
			// is never resumed again.
			if completedJournalCount(t, parent) != 1 {
				t.Fatalf("boundary %s: terminal journal record not retained after recovery", boundary)
			}
		})
	}
}

func TestHandoffRecoveryRejectsCorruptJournal(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	h, err := mhome.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	// Fabricate the corrupt state through the production Home causal path: a
	// valid index referencing one journal whose record bytes are garbage.
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	idx := handoffJournalIndex{Version: handoffIndexVersion, HomeRevision: 1, Active: []string{"bad-transfer"}}
	idxData, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	items := []mhome.ChangeItem{
		{Root: mhome.RootState, Key: handoffIndexKey, Data: append(idxData, '\n')},
		{Root: mhome.RootState, Key: handoffJournalKey("bad-transfer"), Data: []byte("not json")},
	}
	if _, err := h.Commit(lk, "bad-transfer-create", 0, items); err != nil {
		t.Fatal(err)
	}
	lk.Release()

	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected corrupt journal to fail closed")
	}
	// The corrupt journal is retained for inspection.
	if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName, "bad-transfer.json")); err != nil {
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
