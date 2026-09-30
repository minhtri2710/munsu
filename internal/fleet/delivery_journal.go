package fleet

import (
	"errors"
	"fmt"
	"slices"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file owns the durable Fleet delivery journal (ADR-0008 §3, §9): the
// journaled delivery execution path writes its intent before the first
// external mutation and recovery resumes the same operation identities. It
// follows the accepted Task Transfer journal mechanics
// (task_handoff_transaction.go): a bounded active index plus per-operation
// journal records written through home.Commit; recovery discovers journals
// only through the index (never a filesystem scan); a completed record is
// retained as immutable terminal truth and is never resumed.

// deliveryJournalDirName is the state-root key prefix that holds the durable
// Fleet-owned delivery journals of one home.
const deliveryJournalDirName = ".delivery-journal"

// deliveryJournals is the bounded active index and record store of the
// delivery journals of one home; recovery discovers journals only through
// its index.
var deliveryJournals = journalStore{dir: deliveryJournalDirName, noun: "delivery"}

// Journal phases of one delivery record. Phase is Fleet-owned meaning: a
// record is resumable only while "prepared"; the terminal "completed" record
// is retained for truth and is never resumed.
const (
	deliveryPhasePrepared  = "prepared"
	deliveryPhaseCompleted = "completed"
)

// Journal resume stages of one delivery record, persisted durably before
// each step so a crash is distinguishable and an irreversible provider
// mutation is never blindly repeated:
//
//	authorize  — intent written; authorization not yet issued
//	authorized — authorization issued; currency not yet verified and no
//	             irreversible mutation attempted
//	mutating   — currency verified; the irreversible provider mutation was
//	             attempted (it may or may not have executed); recovery
//	             observes and classifies and never re-mutates
//	outcome    — the truthful outcome is derived and pinned; the canonical
//	             outcome commit is pending or committed; completion pending
//	aborted    — terminal: the intent was abandoned before any canonical
//	             mutation was committed (the authorization was never issued)
const (
	deliveryStageAuthorize  = "authorize"
	deliveryStageAuthorized = "authorized"
	deliveryStageMutating   = "mutating"
	deliveryStageOutcome    = "outcome"
	deliveryStageAborted    = "aborted"
)

// deliveryLockScope is the fleet-level fenced lock scope on the home that
// serializes delivery journal creation, transitions, and recovery. The
// canonical delivery primitives fence every task independently; this lock
// only coordinates the fleet-owned journal, so no cross-task lock is ever
// held.
const deliveryLockScope = "delivery"

// deliveryJournal is the durable Fleet-owned intent of one delivery
// execution. It pins every typed request field needed to resume the delivery
// with the SAME Operation IDs: the authorization's generation/revision
// precondition (later operations fence the task's current revision), the
// operation kind, the exact typed delivery identity/head, the provider merge
// method, the asserted closed-set preconditions, the deterministic
// authorization/gate/revoke/outcome Operation identities, the authorization
// request digest, the provider action, and the durable resume stage. The outcome
// fields are pinned at the outcome stage so recovery replays the exact
// committed intent instead of re-deriving a conflicting one.
type deliveryJournal struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Phase   string `json:"phase"`
	Home    string `json:"home"`
	Stage   string `json:"stage"`

	TaskID        string                                  `json:"task_id"`
	Generation    uint64                                  `json:"generation"`
	Revision      uint64                                  `json:"revision"`
	Kind          taskauthority.DeliveryAuthorizationKind `json:"kind"`
	Identity      domain.DeliveryIdentity                 `json:"identity"`
	Method        string                                  `json:"method,omitempty"`
	Preconditions []taskauthority.DeliveryPrecondition    `json:"preconditions"`
	Words         domain.Words                            `json:"words"`

	AuthorizeOpID string `json:"authorize_op_id"`
	GateOpID      string `json:"gate_op_id"`
	RevokeOpID    string `json:"revoke_op_id"`
	OutcomeOpID   string `json:"outcome_op_id"`

	AuthorizeDigest string `json:"authorize_digest,omitempty"`

	OutcomeStatus    taskauthority.DeliveryOutcomeStatus `json:"outcome_status,omitempty"`
	OutcomeDetail    string                              `json:"outcome_detail,omitempty"`
	OutcomeHeadSHA   string                              `json:"outcome_head_sha,omitempty"`
	OutcomeMergedSHA string                              `json:"outcome_merged_sha,omitempty"`
}

// deliveryAuthorizeOpID / deliveryGateOpID / deliveryRevokeOpID /
// deliveryOutcomeOpID derive the
// deterministic canonical Operation identities of one delivery journal. The
// same identities are reused across retries, so the canonical primitives
// replay idempotently and recovery continues the same delivery.
func deliveryAuthorizeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-authorize"
}
func deliveryGateOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-gate"
}
func deliveryRevokeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-revoke"
}
func deliveryOutcomeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-outcome"
}

func (j *deliveryJournal) journalHead() (int, string) { return j.Version, j.ID }

// readDeliveryJournal reads one delivery journal record through Home.Read
// and refuses a phase it does not know.
func readDeliveryJournal(h *home.Home, id string) (*deliveryJournal, error) {
	journal, err := readJournalRecord[deliveryJournal](h, deliveryJournals, id)
	if err != nil {
		return nil, err
	}
	if journal.Phase != deliveryPhasePrepared && journal.Phase != deliveryPhaseCompleted {
		return nil, fmt.Errorf("delivery journal %s has unknown phase %q", id, journal.Phase)
	}
	return journal, nil
}

// transitionDeliveryJournal durably rewrites one active journal record with
// the next stage (and any pinned outcome fields) in ONE atomic Home.Commit,
// always advancing the index home revision so the optimistic base stays
// accurate. The journal must still be listed as active; a terminal record is
// never transitioned.
func transitionDeliveryJournal(h *home.Home, lk *home.Lock, journal *deliveryJournal, transition string, mutate func(*deliveryJournal)) error {
	idx, err := deliveryJournals.readIndex(h)
	if err != nil {
		return err
	}
	if !slices.Contains(idx.Active, journal.ID) {
		return fmt.Errorf("delivery journal %s is not active", journal.ID)
	}
	if journal.Phase != deliveryPhasePrepared {
		return fmt.Errorf("delivery journal %s is terminal (%q); cannot transition", journal.ID, journal.Phase)
	}
	mutate(journal)
	if err := deliveryJournals.commit(h, lk, idx, idx.Active, journal, transition); err != nil {
		return fmt.Errorf("transitioning delivery journal %s (%s): %w", journal.ID, transition, err)
	}
	return nil
}

// completeDeliveryJournal commits the terminal truth of one delivery
// journal: the index drops the journal ID (bounding the active set) and the
// journal record is rewritten phase=completed with the terminal stage in ONE
// atomic Home.Commit. The terminal record is retained as durable truth; no
// file is deleted and a completed record is never resumed.
func completeDeliveryJournal(h *home.Home, lk *home.Lock, journal *deliveryJournal, terminalStage string) error {
	return deliveryJournals.complete(h, lk, journal, func() {
		journal.Phase = deliveryPhaseCompleted
		journal.Stage = terminalStage
	})
}

// abortDeliveryJournal abandons a delivery intent whose authorization was
// never committed: the journal leaves the active index and is retained as a
// terminal aborted record. No canonical mutation is ever unwound because
// none was committed. The reason is preserved on the record for audit.
func abortDeliveryJournal(h *home.Home, lk *home.Lock, journal *deliveryJournal, reason string) error {
	if journal.Phase == deliveryPhaseCompleted {
		return nil
	}
	journal.OutcomeDetail = "aborted before authorization: " + reason
	return completeDeliveryJournal(h, lk, journal, deliveryStageAborted)
}

// recoverPendingDeliveryJournals resumes every ACTIVE delivery journal of
// one home through the bounded index (Home.Read — never a filesystem scan).
// The index and every listed journal are validated before any resume: a
// malformed index, a missing or malformed referenced journal, or a terminal
// record still listed as active is contradictory state and recovery stops
// without resuming anything. A failed resume fails closed and keeps that
// record active without preventing the other journals from being attempted;
// every resume failure is returned joined. A completed record's terminal
// truth is never resumed.
func recoverPendingDeliveryJournals(h *home.Home, lk *home.Lock) error {
	idx, err := deliveryJournals.readIndex(h)
	if err != nil {
		return err
	}
	if len(idx.Active) == 0 {
		return nil
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return fmt.Errorf("delivery recovery: composing task authority: %w", err)
	}
	journals := make([]*deliveryJournal, 0, len(idx.Active))
	for _, id := range idx.Active {
		journal, err := readActiveDeliveryJournal(h, id)
		if err != nil {
			return err
		}
		journals = append(journals, journal)
	}
	var failures []error
	for _, journal := range journals {
		if _, err := resumeDeliveryJournal(h, lk, c, journal); err != nil {
			failures = append(failures, fmt.Errorf("delivery journal %s: %w", journal.ID, err))
		}
	}
	return errors.Join(failures...)
}

// readActiveDeliveryJournal reads one journal listed as active and fails
// closed on contradictory state.
func readActiveDeliveryJournal(h *home.Home, id string) (*deliveryJournal, error) {
	journal, err := readDeliveryJournal(h, id)
	if err != nil {
		return nil, err
	}
	if journal.ID != id || journal.Home != h.Root() {
		return nil, fmt.Errorf("invalid delivery journal entry %s", id)
	}
	if journal.Phase != deliveryPhasePrepared {
		return nil, fmt.Errorf("delivery journal %s is terminal (%q) but still active", id, journal.Phase)
	}
	return journal, nil
}

// RecoverDeliveryJournals resumes every pending Fleet-owned delivery journal
// of a home, continuing the SAME operation identities recorded in each
// journal. It is the delivery recovery gate; Deliver invokes it before
// creating any new journal so an interrupted delivery completes first and a
// new delivery observes truthful state.
func RecoverDeliveryJournals(homeDir string) error {
	h, err := home.Open(homeDir)
	if err != nil {
		return err
	}
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		return err
	}
	defer lk.Release()
	return recoverPendingDeliveryJournals(h, lk)
}

var deliveryCrashHook = func(string) {}
