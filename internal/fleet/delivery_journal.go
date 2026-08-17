package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

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

// deliveryIndexKey is the bounded delivery journal index document key under
// the home state root. It lists the ACTIVE journal IDs of one home;
// recovery discovers journals only through this index.
const deliveryIndexKey = deliveryJournalDirName + "/index.json"

// deliveryIndexVersion is the schema version of the delivery journal index.
const deliveryIndexVersion = 1

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

// deliveryJournalIndex is the bounded Fleet-owned index of ACTIVE delivery
// journals of one home. Only IDs in Active are ever discovered during
// recovery, so completed deliveries cost nothing to skip; each completed
// delivery leaves a terminal journal record that is never scanned.
type deliveryJournalIndex struct {
	Version      int      `json:"version"`
	HomeRevision uint64   `json:"home_revision"`
	Active       []string `json:"active"`
}

// deliveryJournal is the durable Fleet-owned intent of one delivery
// execution. It pins every typed request field needed to resume the delivery
// with the SAME Operation IDs: the task generation/revision precondition, the
// operation kind, the exact typed delivery identity/head, the provider merge
// method, the asserted closed-set preconditions, the deterministic
// authorization/revoke/outcome Operation identities, the exact request
// digests, the provider action, and the durable resume stage. The outcome
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

	AuthorizeOpID string `json:"authorize_op_id"`
	RevokeOpID    string `json:"revoke_op_id"`
	OutcomeOpID   string `json:"outcome_op_id"`

	AuthorizeDigest        string `json:"authorize_digest,omitempty"`
	RevokeDigest           string `json:"revoke_digest,omitempty"`
	RevokeFailClosedDigest string `json:"revoke_fail_closed_digest,omitempty"`
	OutcomeDigest          string `json:"outcome_digest,omitempty"`

	OutcomeStatus    taskauthority.DeliveryOutcomeStatus `json:"outcome_status,omitempty"`
	OutcomeDetail    string                              `json:"outcome_detail,omitempty"`
	OutcomeHeadSHA   string                              `json:"outcome_head_sha,omitempty"`
	OutcomeMergedSHA string                              `json:"outcome_merged_sha,omitempty"`
}

// deliveryJournalKey returns the contained logical key of one delivery
// journal record under the home state root.
func deliveryJournalKey(id string) string {
	return deliveryJournalDirName + "/" + id + ".json"
}

// deliveryTxnID derives the deterministic Home transaction identity of one
// journal transition. Transitions are distinct (create, authorized,
// mutating, outcome, complete, abort), so a txnID is never reused for
// changed journal bytes and replay of the same transition is deterministic.
func deliveryTxnID(journalID, transition string) string {
	return "delivery-" + journalID + "-" + transition
}

// deliveryAuthorizeOpID / deliveryRevokeOpID / deliveryOutcomeOpID derive the
// deterministic canonical Operation identities of one delivery journal. The
// same identities are reused across retries, so the canonical primitives
// replay idempotently and recovery continues the same delivery.
func deliveryAuthorizeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-authorize"
}
func deliveryRevokeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-revoke"
}
func deliveryOutcomeOpID(journalID, taskID string) string {
	return "delivery-" + journalID + "-" + taskID + "-outcome"
}

// mustDeliveryOperation builds a validated Operation from a typed Operation
// ID and intent, deriving the digest from the typed intent.
func mustDeliveryOperation(id string, intent domain.Intent) domain.Operation {
	opID, err := domain.NewOperationID(id)
	if err != nil {
		panic(fmt.Sprintf("delivery: invalid operation id %q: %v", id, err))
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		panic(fmt.Sprintf("delivery: invalid operation for %q: %v", id, err))
	}
	return op
}

// deliveryJournalItems encodes the index document and one journal record as
// the change-set of one Home.Commit transition. The index membership, the
// home revision, and the journal intent always persist atomically.
func deliveryJournalItems(idx deliveryJournalIndex, journal *deliveryJournal) ([]home.ChangeItem, error) {
	idxData, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	journalData, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: home.RootState, Key: deliveryIndexKey, Data: append(idxData, '\n')},
		{Root: home.RootState, Key: deliveryJournalKey(journal.ID), Data: append(journalData, '\n')},
	}, nil
}

// readDeliveryIndex reads and validates the bounded delivery journal index
// through Home.Read. An absent index means no pending deliveries; a malformed
// index (bad JSON, wrong version, duplicate or empty active IDs) fails
// closed.
func readDeliveryIndex(h *home.Home) (deliveryJournalIndex, error) {
	data, err := h.Read(home.RootState, deliveryIndexKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return deliveryJournalIndex{}, nil
		}
		return deliveryJournalIndex{}, fmt.Errorf("reading delivery journal index: %w", err)
	}
	var idx deliveryJournalIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return deliveryJournalIndex{}, fmt.Errorf("corrupt delivery journal index: %w", err)
	}
	if idx.Version != deliveryIndexVersion {
		return deliveryJournalIndex{}, fmt.Errorf("unsupported delivery journal index version %d", idx.Version)
	}
	seen := make(map[string]bool, len(idx.Active))
	for _, id := range idx.Active {
		if id == "" || seen[id] {
			return deliveryJournalIndex{}, fmt.Errorf("invalid delivery journal index: duplicate or empty active id %q", id)
		}
		seen[id] = true
	}
	return idx, nil
}

// readDeliveryJournal reads one delivery journal record through Home.Read.
func readDeliveryJournal(h *home.Home, id string) (*deliveryJournal, error) {
	data, err := h.Read(home.RootState, deliveryJournalKey(id))
	if err != nil {
		return nil, err
	}
	var journal deliveryJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("corrupt delivery journal %s: %w", id, err)
	}
	if journal.Version != 1 || journal.ID != id {
		return nil, fmt.Errorf("invalid delivery journal %s", id)
	}
	if journal.Phase != deliveryPhasePrepared && journal.Phase != deliveryPhaseCompleted {
		return nil, fmt.Errorf("delivery journal %s has unknown phase %q", id, journal.Phase)
	}
	return &journal, nil
}

// writeDeliveryJournal durably records the intent of one new delivery before
// its first side effect: the index gains the journal ID and the journal
// record is written (phase prepared, stage authorize) in ONE atomic
// Home.Commit under the held fenced delivery lock.
func writeDeliveryJournal(h *home.Home, lk *home.Lock, journal *deliveryJournal) error {
	idx, err := readDeliveryIndex(h)
	if err != nil {
		return err
	}
	next := idx
	next.Version = deliveryIndexVersion
	next.HomeRevision++
	next.Active = append(next.Active, journal.ID)
	items, err := deliveryJournalItems(next, journal)
	if err != nil {
		return err
	}
	if _, err := h.Commit(lk, deliveryTxnID(journal.ID, "create"), idx.HomeRevision, items); err != nil {
		return fmt.Errorf("writing delivery journal %s: %w", journal.ID, err)
	}
	return nil
}

// transitionDeliveryJournal durably rewrites one active journal record with
// the next stage (and any pinned outcome fields) in ONE atomic Home.Commit,
// always advancing the index home revision so the optimistic base stays
// accurate. The journal must still be listed as active; a terminal record is
// never transitioned.
func transitionDeliveryJournal(h *home.Home, lk *home.Lock, journal *deliveryJournal, transition string, mutate func(*deliveryJournal)) error {
	idx, err := readDeliveryIndex(h)
	if err != nil {
		return err
	}
	if !containsString(idx.Active, journal.ID) {
		return fmt.Errorf("delivery journal %s is not active", journal.ID)
	}
	if journal.Phase != deliveryPhasePrepared {
		return fmt.Errorf("delivery journal %s is terminal (%q); cannot transition", journal.ID, journal.Phase)
	}
	next := idx
	next.Version = deliveryIndexVersion
	next.HomeRevision++
	mutate(journal)
	items, err := deliveryJournalItems(next, journal)
	if err != nil {
		return err
	}
	if _, err := h.Commit(lk, deliveryTxnID(journal.ID, transition), idx.HomeRevision, items); err != nil {
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
	idx, err := readDeliveryIndex(h)
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
		return fmt.Errorf("completing delivery journal %s: not active", journal.ID)
	}
	next := idx
	next.Version = deliveryIndexVersion
	next.HomeRevision++
	next.Active = active
	journal.Phase = deliveryPhaseCompleted
	journal.Stage = terminalStage
	items, err := deliveryJournalItems(next, journal)
	if err != nil {
		return err
	}
	if _, err := h.Commit(lk, deliveryTxnID(journal.ID, "complete"), idx.HomeRevision, items); err != nil {
		return fmt.Errorf("completing delivery journal %s: %w", journal.ID, err)
	}
	return nil
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

// newDeliveryJournalID mints a collision-safe random journal identity.
func newDeliveryJournalID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating delivery journal ID: %w", err)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), buffer), nil
}

// recoverPendingDeliveryJournals resumes every ACTIVE delivery journal of
// one home through the bounded index (Home.Read — never a filesystem scan).
// A failed resume fails closed and keeps the record active; a completed
// record's terminal truth is never resumed. The index and each journal are
// validated fail-closed: a malformed index, a missing or malformed
// referenced journal, or a terminal record still listed as active is
// contradictory state and recovery stops.
func recoverPendingDeliveryJournals(h *home.Home, lk *home.Lock) error {
	idx, err := readDeliveryIndex(h)
	if err != nil {
		return err
	}
	for _, id := range idx.Active {
		if err := recoverPendingDeliveryJournal(h, lk, id); err != nil {
			return err
		}
	}
	return nil
}

// recoverPendingDeliveryJournal resumes one active delivery journal record.
func recoverPendingDeliveryJournal(h *home.Home, lk *home.Lock, id string) error {
	journal, err := readDeliveryJournal(h, id)
	if err != nil {
		return err
	}
	if journal.ID != id || journal.Home != h.Root() {
		return fmt.Errorf("invalid delivery journal entry %s", id)
	}
	if journal.Phase != deliveryPhasePrepared {
		return fmt.Errorf("delivery journal %s is terminal (%q) but still active", id, journal.Phase)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return fmt.Errorf("delivery journal %s: composing task authority: %w", id, err)
	}
	if _, err := resumeDeliveryJournal(h, lk, c, journal); err != nil {
		return err
	}
	return nil
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

// containsString reports whether the value appears in the slice.
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
