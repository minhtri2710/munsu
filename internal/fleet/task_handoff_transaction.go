package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// taskHandoffDirName is the state-root key prefix that holds the durable
// Fleet-owned Task Transfer journals of one home (ADR-0008 §3, §5, §9). The
// journal is written before the first side effect and recovery resumes the
// same Transfer Operation IDs. All journal state (index document and per
// transfer records) lives under this logical key and is written through
// Home's journaled change-set commit; Home owns the durable mechanics.
const taskHandoffDirName = ".task-handoff"

// handoffIndexKey is the bounded handoff index document key under the home
// state root. It lists the ACTIVE Transfer IDs of one home; recovery
// discovers journals only through this index (never by scanning the
// filesystem). Completed transfers are removed from the index while their
// terminal journal record is retained.
const handoffIndexKey = taskHandoffDirName + "/index.json"

// handoffIndexVersion is the schema version of the handoff index document.
const handoffIndexVersion = 1

// Journal phases of one transfer record. Phase is Fleet-owned meaning: a
// record is resumable only while "prepared"; the terminal "completed" record
// is retained for truth and is never resumed.
const (
	handoffPhasePrepared  = "prepared"
	handoffPhaseCompleted = "completed"
)

// handoffJournalKey returns the contained logical key of one transfer
// journal record under the home state root.
func handoffJournalKey(id string) string {
	return taskHandoffDirName + "/" + id + ".json"
}

// handoffTxnID derives the deterministic Home transaction identity of one
// journal transition. Transitions are distinct (create vs complete), so a
// txnID is never reused for changed journal bytes and replay of the same
// transition is deterministic.
func handoffTxnID(transferID, transition string) string {
	return "handoff-" + transferID + "-" + transition
}

// handoffJournalIndex is the bounded Fleet-owned index of ACTIVE transfer
// journals of one home (ADR-0008 §5: one bounded aggregate under a logical
// key in the handoff lock scope). Only IDs in Active are ever discovered
// during recovery, so completed transfers cost nothing to skip; each
// completed transfer leaves a terminal journal record that is never scanned.
type handoffJournalIndex struct {
	Version      int      `json:"version"`
	HomeRevision uint64   `json:"home_revision"`
	Active       []string `json:"active"`
}

// handoffLockScope is the fleet-level fenced lock scope on the source home
// serializing journal creation and recovery. The canonical transfer
// primitives fence every task independently; this lock only coordinates the
// fleet-owned journal, so no cross-home lock is ever held.
const handoffLockScope = "task-handoff"

// Handoff transfers tasks from a parent home to a captain home through the
// ADR-0008 Fleet-owned journaled Task Transfer (ADR-0008 §3): durable intent
// is written before the first side effect, the source generation is fenced
// with ReserveTransfer, the destination re-creates the generation from the
// typed TaskDefinition with an idempotent ReceiveTransfer (NON-CURRENT), the
// received generation is verified in scope, the source generation is
// superseded with CommitTransfer bound to the destination-activation
// evidence, and only then is the received generation activated as current at
// the destination. Recovery continues the SAME Transfer Operation IDs and
// journal. No raw Task document is copied, no ownership is inferred, and no
// source/destination-wins heuristic resolves divergence.
func Handoff(parentHome, captainHome string, itemKeys []string) error {
	return durableTaskHandoff(parentHome, captainHome, itemKeys)
}

// SetHandoffCrashHookForTest installs a crash-boundary hook for subprocess
// tests. Production callers should never set this hook. Boundary names are
// the durable stages of one Transfer: journal, reserved, received, committed,
// activated, completed.
func SetHandoffCrashHookForTest(hook func(string)) func() {
	previous := handoffCrashHook
	if hook == nil {
		handoffCrashHook = func(string) {}
	} else {
		handoffCrashHook = hook
	}
	return func() { handoffCrashHook = previous }
}

// RecoverTaskHandoffs resumes every pending Fleet-owned Task Transfer journal
// of a home and of its configured parent home, continuing the SAME Transfer
// Operation IDs recorded in each journal. It is the pre-read public task
// safety gate for brief, spawn, and task reads.
func RecoverTaskHandoffs(homeDir string) error {
	canonical, err := canonicalHandoffHome(homeDir)
	if err != nil {
		return err
	}
	if err := recoverTransferJournals(canonical); err != nil {
		return err
	}
	parent, hasParent, err := configuredParentHome(canonical)
	if err != nil {
		return err
	}
	if hasParent {
		return recoverTransferJournals(parent)
	}
	return nil
}

func configuredParentHome(homeDir string) (string, bool, error) {
	path := filepath.Join(config.ConfigDir(homeDir), "parent-home")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading parent-home configuration: %w", err)
	}
	parent, err := config.Get(homeDir, "parent-home")
	if err != nil {
		return "", false, fmt.Errorf("reading parent-home configuration: %w", err)
	}
	if strings.TrimSpace(parent) == "" {
		return "", false, fmt.Errorf("malformed parent-home configuration")
	}
	canonical, err := canonicalHandoffHome(parent)
	if err != nil {
		return "", false, fmt.Errorf("canonicalizing parent-home: %w", err)
	}
	return canonical, true, nil
}

// taskHandoffJournal is the durable Fleet-owned intent of one Task Transfer
// (ADR-0008 §9: durable intent before side effects). It pin every typed
// request field needed to resume the transfer with the SAME Operation IDs:
// the source generation/revision precondition, the reservation identity and
// fence token, the destination owner, and the typed TaskDefinition the
// destination re-creates the generation from (never a raw source document).
type taskHandoffJournal struct {
	Version         int               `json:"version"`
	ID              string            `json:"id"`
	Phase           string            `json:"phase"`
	SourceHome      string            `json:"source_home"`
	DestinationHome string            `json:"destination_home"`
	Tasks           []taskHandoffTask `json:"tasks"`
}

// taskHandoffTask pins one task's transfer intent. The four Operation IDs are
// deterministic from the transfer ID and task ID, so a restart replays the
// exact same operations (idempotent by Operation ID + digest).
type taskHandoffTask struct {
	TaskID           string                       `json:"task_id"`
	SourceGeneration uint64                       `json:"source_generation"`
	SourceRevision   uint64                       `json:"source_revision"`
	ReservationID    string                       `json:"reservation_id"`
	FenceToken       string                       `json:"fence_token"`
	Owner            string                       `json:"owner"`
	Definition       taskauthority.TaskDefinition `json:"definition"`
}

var handoffCrashHook = func(string) {}

func durableTaskHandoff(parentHome, captainHome string, itemKeys []string) error {
	source, err := canonicalHandoffHome(parentHome)
	if err != nil {
		return err
	}
	destination, err := canonicalHandoffHome(captainHome)
	if err != nil {
		return err
	}
	if source == destination {
		return fmt.Errorf("refusing handoff: destination is parent home itself")
	}
	captainID, err := ValidateProvenance(destination)
	if err != nil {
		return fmt.Errorf("refusing handoff to unmarked home %s: %w", destination, err)
	}
	if len(itemKeys) == 0 {
		return fmt.Errorf("handoff: no tasks requested")
	}
	// Supervision gate: handoff fails closed when the watcher lease of either
	// home is degraded (ADR-0007 §8). Durable Dispatch Holds are evaluated by
	// the fleet holds gate below.
	if err := CheckSupervisionForDispatch(source, mhome.DispatchActionHandoff); err != nil {
		return err
	}
	if err := CheckSupervisionForDispatch(destination, mhome.DispatchActionHandoff); err != nil {
		return err
	}

	sourceHome, err := mhome.Open(source)
	if err != nil {
		return err
	}
	sourceAuth, err := taskauthority.NewCanonical(sourceHome)
	if err != nil {
		return err
	}
	destinationHome, err := mhome.Open(destination)
	if err != nil {
		return err
	}
	destinationAuth, err := taskauthority.NewCanonical(destinationHome)
	if err != nil {
		return err
	}

	// Fleet-owned holds gate: every applicable durable Dispatch Hold on either
	// home blocks the transfer before any intent (the matching rule lives in
	// taskauthority.DispatchHold.Matches; only the orchestration stays here).
	if err := checkHandoffHolds(sourceAuth, "", "", "", ""); err != nil {
		return err
	}
	if err := checkHandoffHolds(destinationAuth, "", "", "", ""); err != nil {
		return err
	}

	keys, err := resolveHandoffKeys(source, sourceAuth, itemKeys)
	if err != nil {
		return err
	}
	for _, taskID := range keys {
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return err
		}
		agg, err := sourceAuth.Get(tid)
		if err != nil {
			return fmt.Errorf("handoff: reading source task %s: %w", taskID, err)
		}
		project := agg.Definition.Project
		parentID := agg.Definition.ParentTaskID
		if err := checkHandoffHolds(sourceAuth, taskID, project, agg.Generation.String(), parentID); err != nil {
			return err
		}
		if err := checkHandoffHolds(destinationAuth, taskID, project, agg.Generation.String(), parentID); err != nil {
			return err
		}
		if parentID != "" {
			if err := checkHandoffHolds(sourceAuth, parentID, project, "", parentID); err != nil {
				return err
			}
		}
		// A destination that already owns the task quarantines the transfer
		// with a typed conflict before any intent; the receive operation
		// re-fences the same rule inside its transaction.
		if _, err := destinationAuth.Get(tid); err == nil {
			return handoffDestinationConflict(taskID, agg.Generation)
		} else if !errors.Is(err, taskauthority.ErrNotFound) {
			return fmt.Errorf("handoff: reading destination ownership for %s: %w", taskID, err)
		}
	}

	// Lock the source home and converge any pending journals before creating a
	// new Transfer, so an interrupted transfer completes first and the new
	// transfer observes truthful ownership.
	lk, err := sourceHome.Lock(handoffLockScope)
	if err != nil {
		return err
	}
	defer lk.Release()
	if err := recoverPendingJournals(sourceHome, lk); err != nil {
		return err
	}

	journal, err := buildTransferJournal(source, destination, captainID, sourceAuth, destinationAuth, keys)
	if err != nil {
		return err
	}
	// Durable intent BEFORE the first side effect (Reserve).
	if err := writeHandoffJournal(sourceHome, lk, journal); err != nil {
		return err
	}
	handoffCrashHook("journal")

	if err := resumeTransfer(sourceHome, lk, journal); err != nil {
		return err
	}
	for _, task := range journal.Tasks {
		fmt.Printf("handed-off %s\n", task.TaskID)
	}
	return nil
}

// handoffCanonical constructs the canonical Task Authority over one home. The
// home must be an initialized canonical v1 home; construction fails closed on
// a non-canonical home.
func handoffCanonical(homeDir string) (*taskauthority.Canonical, error) {
	h, err := mhome.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return taskauthority.NewCanonical(h)
}

// transferOpID derives one deterministic Operation ID for a transfer step.
// The same IDs are reused across retries, so the canonical primitives replay
// idempotently and recovery continues the same Transfer.
func transferOpID(transferID, taskID, step string) string {
	return "transfer-" + transferID + "-" + taskID + "-" + step
}

// mustHandoffOperation builds a validated Operation from a typed Operation ID
// and intent, deriving the digest from the typed intent.
func mustHandoffOperation(id string, intent domain.Intent) domain.Operation {
	opID, err := domain.NewOperationID(id)
	if err != nil {
		panic(fmt.Sprintf("handoff: invalid operation id %q: %v", id, err))
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		panic(fmt.Sprintf("handoff: invalid operation for %q: %v", id, err))
	}
	return op
}

// checkHandoffHolds is the fleet-owned dispatch-hold gate for the handoff
// action: it lists the home's durable holds through the canonical Authority
// and matches them against the task/project/generation/parent. The matching
// rule lives in taskauthority.DispatchHold.Matches; only the orchestration
// stays in fleet.
func checkHandoffHolds(auth *taskauthority.Canonical, taskID, project, generation, parentID string) error {
	holds, err := auth.ListHolds()
	if err != nil {
		return err
	}
	for _, hold := range holds {
		if hold.Matches(taskauthority.DispatchActionHandoff, taskID, project, generation, parentID) {
			return domain.NewError(domain.ErrorConflict,
				fmt.Sprintf("dispatch is held: %s (%s)", hold.ID, hold.Reason),
				domain.RetryNever, taskauthority.ErrDispatchHeld)
		}
	}
	return nil
}

// handoffCandidateOwners returns every canonical home that currently owns the
// task: the source home plus every captain under its captains/ tree.
// Ownership is read from the canonical Get, so a home owns the task when its
// current aggregate exists. A home that cannot serve a canonical view fails
// closed: its ownership is unknowable.
func handoffCandidateOwners(source, taskID string) ([]string, error) {
	var owners []string
	collect := func(home string) error {
		auth, err := handoffCanonical(home)
		if err != nil {
			return err
		}
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return err
		}
		if _, err := auth.Get(tid); err == nil {
			owners = append(owners, home)
		} else if !errors.Is(err, taskauthority.ErrNotFound) {
			return fmt.Errorf("resolving handoff task %s: home %s cannot serve canonical authority state: %w", taskID, home, err)
		}
		return nil
	}
	if err := collect(source); err != nil {
		return nil, err
	}
	captainsRoot := filepath.Join(source, "captains")
	entries, err := os.ReadDir(captainsRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := collect(filepath.Join(captainsRoot, entry.Name())); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

// resolveHandoffTaskID resolves one requested key against canonical ownership
// across the source home and its captains. Multiple canonical owners make the
// key ambiguous and the CLI surfaces correction commands preserving the
// requested destination.
func resolveHandoffTaskID(source, key string) (string, error) {
	if err := validateHandoffTaskID(key); err != nil {
		return "", err
	}
	owners, err := handoffCandidateOwners(source, key)
	if err != nil {
		return "", err
	}
	if len(owners) > 1 {
		return "", &mhome.AmbiguousTaskIDError{Requested: key, Matches: owners}
	}
	return key, nil
}

func validateHandoffTaskID(taskID string) error {
	if taskID == "" || taskID == "." || taskID == ".." || filepath.Base(taskID) != taskID || strings.ContainsAny(taskID, `/\\`) {
		return fmt.Errorf("invalid handoff task id %q", taskID)
	}
	return nil
}

func canonicalHandoffHome(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving handoff home %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("canonicalizing handoff home %s: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

// resolveHandoffKeys resolves the requested keys against canonical source
// ownership, refusing any key that is not an owned queued task. Each key must
// resolve to a single canonical owner (no ambiguity).
func resolveHandoffKeys(source string, sourceAuth *taskauthority.Canonical, requested []string) ([]string, error) {
	seen := make(map[string]bool, len(requested))
	keys := make([]string, 0, len(requested))
	for _, key := range requested {
		resolved, err := resolveHandoffTaskID(source, key)
		if err != nil {
			return nil, fmt.Errorf("handoff: resolving task %s: %w", key, err)
		}
		if seen[resolved] {
			return nil, fmt.Errorf("handoff: duplicate task %s", resolved)
		}
		tid, err := domain.NewTaskID(resolved)
		if err != nil {
			return nil, err
		}
		agg, err := sourceAuth.Get(tid)
		if err != nil {
			return nil, fmt.Errorf("handoff: key %s not owned by the source home: %w", resolved, err)
		}
		if agg.Phase != taskauthority.PhaseQueued {
			return nil, fmt.Errorf("handoff: task %s has state %q — only queued tasks may be transferred", resolved, agg.Phase)
		}
		seen[resolved] = true
		keys = append(keys, resolved)
	}
	return keys, nil
}

// handoffDestinationConflict is the typed conflict returned when the
// destination already owns the task: the transfer quarantines and never
// overwrites destination truth.
func handoffDestinationConflict(taskID string, generation taskauthority.Generation) error {
	return domain.NewError(domain.ErrorConflict,
		fmt.Sprintf("handoff: destination already has current authority for %s generation %s", taskID, generation),
		domain.RetryNever, nil)
}

// buildTransferJournal pins the durable intent of one transfer: one
// validated reservation per task, the source generation/revision precondition,
// the destination owner, and the typed TaskDefinition. The destination's
// ownership is proven absent before intent.
func buildTransferJournal(source, destination, captainID string, sourceAuth, destinationAuth *taskauthority.Canonical, keys []string) (*taskHandoffJournal, error) {
	id, err := newTaskHandoffID()
	if err != nil {
		return nil, err
	}
	journal := &taskHandoffJournal{
		Version:         1,
		ID:              id,
		Phase:           "prepared",
		SourceHome:      source,
		DestinationHome: destination,
	}
	for _, taskID := range keys {
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return nil, err
		}
		agg, err := sourceAuth.Get(tid)
		if err != nil {
			return nil, fmt.Errorf("handoff: reading source task %s: %w", taskID, err)
		}
		if _, err := destinationAuth.Get(tid); err == nil {
			return nil, handoffDestinationConflict(taskID, agg.Generation)
		} else if !errors.Is(err, taskauthority.ErrNotFound) {
			return nil, fmt.Errorf("handoff: reading destination ownership for %s: %w", taskID, err)
		}
		reservationID := "transfer-" + id + "-" + taskID
		journal.Tasks = append(journal.Tasks, taskHandoffTask{
			TaskID:           taskID,
			SourceGeneration: uint64(agg.Generation),
			SourceRevision:   uint64(agg.Revision),
			ReservationID:    reservationID,
			FenceToken:       "fence-" + reservationID,
			Owner:            "captain:" + captainID,
			Definition: taskauthority.TaskDefinition{
				Owner:        "captain:" + captainID,
				Description:  agg.Definition.Description,
				Kind:         agg.Definition.Kind,
				Project:      agg.Definition.Project,
				ParentTaskID: agg.Definition.ParentTaskID,
			},
		})
	}
	return journal, nil
}

// recoverTransferJournals resumes every pending transfer journal of one
// canonical home. A home that cannot serve canonical state has no canonical
// transfers to recover.
func recoverTransferJournals(homeDir string) error {
	h, err := mhome.Open(homeDir)
	if err != nil {
		return nil
	}
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		return err
	}
	defer lk.Release()
	return recoverPendingJournals(h, lk)
}

// recoverPendingJournals discovers every ACTIVE transfer of one home through
// the bounded handoff index (Home.Read — never a filesystem scan) and resumes
// each with the same Operation IDs. A completed transfer's journal record is
// retained as terminal truth; a failed resume fails closed and keeps the
// record active. The index and each journal are validated fail-closed: a
// malformed index, a missing or malformed referenced journal, or a terminal
// record still listed as active is contradictory state and recovery stops.
func recoverPendingJournals(h *mhome.Home, lk *mhome.Lock) error {
	idx, err := readHandoffIndex(h)
	if err != nil {
		return err
	}
	for _, id := range idx.Active {
		if err := recoverPendingJournal(h, lk, id); err != nil {
			return err
		}
	}
	return nil
}

func recoverPendingJournal(h *mhome.Home, lk *mhome.Lock, id string) error {
	journal, err := readHandoffJournal(h, id)
	if err != nil {
		return err
	}
	if journal.ID != id || journal.SourceHome != h.Root() {
		return fmt.Errorf("invalid handoff journal entry %s", id)
	}
	if journal.Phase != handoffPhasePrepared {
		return fmt.Errorf("handoff journal %s is terminal (%q) but still active", id, journal.Phase)
	}
	return resumeTransfer(h, lk, journal)
}

// resumeTransfer runs one Transfer's idempotent pipeline through the
// canonical primitives, then commits the journal's terminal truth. Every
// operation reuses the recorded deterministic Operation IDs, so an
// interrupted Transfer continues from any durable stage. The ordered
// invariant is: source Reserve → destination Receive (NON-CURRENT) → scoped
// verification → source Commit/supersede → destination Activate; no
// successful state exposes both homes as current owners.
func resumeTransfer(h *mhome.Home, lk *mhome.Lock, journal *taskHandoffJournal) error {
	sourceAuth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return err
	}
	destinationHome, err := mhome.Open(journal.DestinationHome)
	if err != nil {
		return err
	}
	destinationAuth, err := taskauthority.NewCanonical(destinationHome)
	if err != nil {
		return err
	}
	sourceHomeID := sourceAuth.HomeID()
	destHomeID := destinationAuth.HomeID()

	// 1. Fence every source generation (idempotent).
	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		tid, err := domain.NewTaskID(task.TaskID)
		if err != nil {
			return err
		}
		req := taskauthority.CanonicalReserveTransferRequest{
			HomeID:        sourceHomeID,
			TaskID:        tid,
			Precondition:  domain.Of(task.SourceGeneration, task.SourceRevision),
			ReservationID: task.ReservationID,
			Destination:   destHomeID,
			FenceToken:    task.FenceToken,
			Reason:        "task transfer to " + destHomeID.Value(),
		}
		if _, err := sourceAuth.ReserveTransfer(mustHandoffOperation(transferOpID(journal.ID, task.TaskID, "reserve"), req), req); err != nil {
			return fmt.Errorf("transfer %s: reserving source task %s: %w", journal.ID, task.TaskID, err)
		}
	}
	handoffCrashHook("reserved")

	// 2. Re-create every generation at the destination as NON-CURRENT from the
	// typed TaskDefinition (never from a raw source document), idempotently.
	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		tid, err := domain.NewTaskID(task.TaskID)
		if err != nil {
			return err
		}
		req := taskauthority.CanonicalReceiveTransferRequest{
			HomeID:           destHomeID,
			TaskID:           tid,
			ReservationID:    task.ReservationID,
			SourceHome:       sourceHomeID,
			SourceGeneration: taskauthority.Generation(task.SourceGeneration),
			Definition:       task.Definition,
			Reason:           "task transfer receive",
		}
		if _, err := destinationAuth.ReceiveTransfer(mustHandoffOperation(transferOpID(journal.ID, task.TaskID, "receive"), req), req); err != nil {
			return fmt.Errorf("transfer %s: receiving task %s at destination: %w", journal.ID, task.TaskID, err)
		}
	}
	handoffCrashHook("received")

	// 3. Scoped verification: the received generation must be the exact
	// non-current generation under this reservation before the source may be
	// superseded. The verified generation pins the destination-activation
	// evidence for the commit.
	evidence := make([]taskauthority.TransferActivationInfo, len(journal.Tasks))
	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		tid, err := domain.NewTaskID(task.TaskID)
		if err != nil {
			return err
		}
		activateReq := taskauthority.CanonicalActivateTransferRequest{
			HomeID:        destHomeID,
			TaskID:        tid,
			Precondition:  domain.Of(1, 1),
			ReservationID: task.ReservationID,
			Reason:        "task transfer activate",
		}
		activateOp := mustHandoffOperation(transferOpID(journal.ID, task.TaskID, "activate"), activateReq)
		evidence[i], err = verifyTransferReceived(destinationAuth, sourceHomeID, activateOp, journal, task)
		if err != nil {
			return err
		}
	}

	// 4. Supersede every source generation, each bound to the destination-
	// activation evidence of the exact reservation (idempotent).
	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		tid, err := domain.NewTaskID(task.TaskID)
		if err != nil {
			return err
		}
		req := taskauthority.CanonicalCommitTransferRequest{
			HomeID:        sourceHomeID,
			TaskID:        tid,
			Precondition:  domain.Of(task.SourceGeneration, task.SourceRevision+1),
			ReservationID: task.ReservationID,
			FenceToken:    task.FenceToken,
			Evidence:      evidence[i],
			Reason:        "task transfer commit",
		}
		if _, err := sourceAuth.CommitTransfer(mustHandoffOperation(transferOpID(journal.ID, task.TaskID, "commit"), req), req); err != nil {
			return fmt.Errorf("transfer %s: superseding source task %s: %w", journal.ID, task.TaskID, err)
		}
	}
	handoffCrashHook("committed")

	// 5. Activate every received generation as the destination's current
	// owned generation (idempotent; activation is unique).
	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		tid, err := domain.NewTaskID(task.TaskID)
		if err != nil {
			return err
		}
		activateReq := taskauthority.CanonicalActivateTransferRequest{
			HomeID:        destHomeID,
			TaskID:        tid,
			Precondition:  domain.Of(1, 1),
			ReservationID: task.ReservationID,
			Reason:        "task transfer activate",
		}
		if _, err := destinationAuth.ActivateTransfer(mustHandoffOperation(transferOpID(journal.ID, task.TaskID, "activate"), activateReq), activateReq); err != nil {
			return fmt.Errorf("transfer %s: activating task %s at destination: %w", journal.ID, task.TaskID, err)
		}
	}
	handoffCrashHook("activated")

	if err := completeHandoffJournal(h, lk, journal); err != nil {
		return err
	}
	handoffCrashHook("completed")
	return nil
}

// verifyTransferReceived performs the scoped verification of a received
// destination generation and builds the destination-activation evidence bound
// to the reservation. It fails closed unless the received generation is
// exactly the non-current generation under this reservation with the expected
// source provenance.
func verifyTransferReceived(destinationAuth *taskauthority.Canonical, sourceHomeID domain.HomeID, activateOp domain.Operation, journal *taskHandoffJournal, task *taskHandoffTask) (taskauthority.TransferActivationInfo, error) {
	tid, err := domain.NewTaskID(task.TaskID)
	if err != nil {
		return taskauthority.TransferActivationInfo{}, err
	}
	received, err := destinationAuth.GetGeneration(tid, taskauthority.Generation(1))
	if err != nil {
		return taskauthority.TransferActivationInfo{}, fmt.Errorf("transfer %s: verifying received task %s: %w", journal.ID, task.TaskID, err)
	}
	if received.Transfer == nil || received.Transfer.ReservationID != task.ReservationID {
		return taskauthority.TransferActivationInfo{}, fmt.Errorf("transfer %s: received task %s is not under reservation %s", journal.ID, task.TaskID, task.ReservationID)
	}
	if received.Transfer.SourceHome != sourceHomeID.Value() {
		return taskauthority.TransferActivationInfo{}, fmt.Errorf("transfer %s: received task %s source provenance mismatch: %q", journal.ID, task.TaskID, received.Transfer.SourceHome)
	}
	if uint64(received.Transfer.SourceGeneration) != task.SourceGeneration {
		return taskauthority.TransferActivationInfo{}, fmt.Errorf("transfer %s: received task %s source generation mismatch: %d", journal.ID, task.TaskID, received.Transfer.SourceGeneration)
	}
	return taskauthority.TransferActivationInfo{
		ReservationID:         task.ReservationID,
		TaskID:                task.TaskID,
		SourceHome:            sourceHomeID.Value(),
		SourceGeneration:      taskauthority.Generation(task.SourceGeneration),
		DestinationHome:       destinationAuth.HomeID().Value(),
		DestinationGeneration: received.Generation,
		ActivationOperationID: activateOp.ID.Value(),
		ActivationDigest:      activateOp.Digest,
	}, nil
}

// --- Journal persistence ---

func newTaskHandoffID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating handoff transaction ID: %w", err)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), buffer), nil
}

// readHandoffIndex reads and validates the bounded handoff index through
// Home.Read. An absent index means no pending transfers; a malformed index
// (bad JSON, wrong version, duplicate or empty active IDs) fails closed.
func readHandoffIndex(h *mhome.Home) (handoffJournalIndex, error) {
	data, err := h.Read(mhome.RootState, handoffIndexKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return handoffJournalIndex{}, nil
		}
		return handoffJournalIndex{}, fmt.Errorf("reading handoff journal index: %w", err)
	}
	var idx handoffJournalIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return handoffJournalIndex{}, fmt.Errorf("corrupt handoff journal index: %w", err)
	}
	if idx.Version != handoffIndexVersion {
		return handoffJournalIndex{}, fmt.Errorf("unsupported handoff journal index version %d", idx.Version)
	}
	seen := make(map[string]bool, len(idx.Active))
	for _, id := range idx.Active {
		if id == "" || seen[id] {
			return handoffJournalIndex{}, fmt.Errorf("invalid handoff journal index: duplicate or empty active id %q", id)
		}
		seen[id] = true
	}
	return idx, nil
}

// handoffJournalItems encodes the index document and one journal record as
// the change-set of one Home.Commit transition. The index membership and the
// journal intent always persist atomically.
func handoffJournalItems(idx handoffJournalIndex, journal *taskHandoffJournal) ([]mhome.ChangeItem, error) {
	idxData, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	journalData, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, err
	}
	return []mhome.ChangeItem{
		{Root: mhome.RootState, Key: handoffIndexKey, Data: append(idxData, '\n')},
		{Root: mhome.RootState, Key: handoffJournalKey(journal.ID), Data: append(journalData, '\n')},
	}, nil
}

// writeHandoffJournal durably records the intent of one new Transfer before
// its first side effect: the index gains the Transfer ID and the journal
// record is written (phase prepared) in ONE atomic Home.Commit under the held
// fenced handoff lock. expectedRevision is the index's current HomeRevision,
// which tracks the handoff scope revision read under the same lock.
func writeHandoffJournal(h *mhome.Home, lk *mhome.Lock, journal *taskHandoffJournal) error {
	idx, err := readHandoffIndex(h)
	if err != nil {
		return err
	}
	next := idx
	next.Version = handoffIndexVersion
	next.HomeRevision++
	next.Active = append(next.Active, journal.ID)
	items, err := handoffJournalItems(next, journal)
	if err != nil {
		return err
	}
	if _, err := h.Commit(lk, handoffTxnID(journal.ID, "create"), idx.HomeRevision, items); err != nil {
		return fmt.Errorf("writing handoff journal %s: %w", journal.ID, err)
	}
	return nil
}

// readHandoffJournal reads one transfer journal record through Home.Read.
func readHandoffJournal(h *mhome.Home, id string) (*taskHandoffJournal, error) {
	data, err := h.Read(mhome.RootState, handoffJournalKey(id))
	if err != nil {
		return nil, err
	}
	var journal taskHandoffJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("corrupt handoff journal %s: %w", id, err)
	}
	if journal.Version != 1 || journal.ID != id {
		return nil, fmt.Errorf("invalid handoff journal %s", id)
	}
	return &journal, nil
}

// completeHandoffJournal commits the terminal truth of one completed
// Transfer: the index drops the Transfer ID (bounding the active set) and the
// journal record is rewritten phase=completed in ONE atomic Home.Commit. The
// terminal record is retained as durable truth; no file is deleted and a
// completed record is never resumed.
func completeHandoffJournal(h *mhome.Home, lk *mhome.Lock, journal *taskHandoffJournal) error {
	idx, err := readHandoffIndex(h)
	if err != nil {
		return err
	}
	found := false
	active := make([]string, 0, len(idx.Active))
	for _, id := range idx.Active {
		if id == journal.ID {
			found = true
			continue
		}
		active = append(active, id)
	}
	if !found {
		return fmt.Errorf("completing handoff journal %s: not active", journal.ID)
	}
	next := idx
	next.Version = handoffIndexVersion
	next.HomeRevision++
	next.Active = active
	journal.Phase = handoffPhaseCompleted
	items, err := handoffJournalItems(next, journal)
	if err != nil {
		return err
	}
	if _, err := h.Commit(lk, handoffTxnID(journal.ID, "complete"), idx.HomeRevision, items); err != nil {
		return fmt.Errorf("completing handoff journal %s: %w", journal.ID, err)
	}
	return nil
}
