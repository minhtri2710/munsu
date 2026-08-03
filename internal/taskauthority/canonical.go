package taskauthority

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// Canonical is the canonical home-backed Task Authority. It owns Task
// documents, lifecycle transitions, readiness, and Dispatch Holds on top of
// the canonical home's durable mechanics (ADR-0008 §2): every mutation is an
// atomic journaled change-set under the smallest scoped fenced lock, and
// every read observes the same committed Task documents. There is no Store
// interface, in-memory fake, projection, or backlog behind this surface.
//
// All mutations are idempotent by Operation identity: repeating a committed
// Operation ID with the same digest replays the original outcome, and reusing
// an Operation ID with a different digest conflicts (ErrOperationConflict).
// Stale intents fail closed with a typed domain.Conflict built through
// domain.ConflictFrom from a verified generation/revision mismatch.
type Canonical struct {
	h      *home.Home
	homeID domain.HomeID
	now    func() time.Time
}

// NewCanonical constructs the canonical Task Authority over an opened
// canonical home. It derives the typed Home identity from the home's durable
// identity; every operation is bound to exactly this home.
func NewCanonical(h *home.Home) (*Canonical, error) {
	if h == nil {
		return nil, errors.New("taskauthority: nil home")
	}
	homeID, err := domain.NewHomeID(h.Identity().ID)
	if err != nil {
		return nil, fmt.Errorf("taskauthority: invalid home identity: %w", err)
	}
	return &Canonical{h: h, homeID: homeID, now: time.Now}, nil
}

// HomeID returns the canonical home identity this Authority is bound to.
func (c *Canonical) HomeID() domain.HomeID { return c.homeID }

// Outcome is the committed outcome of one canonical task mutation.
type Outcome struct {
	TaskID     domain.TaskID
	Generation Generation
	Revision   Revision
	Phase      Phase
	Reopened   bool
	Replayed   bool
}

// Canonical storage layout under the home state root. Keys are logical
// home keys; every write goes through home.Commit and every read through
// home.Read, so containment and no-follow safety are enforced by home.
const (
	canonicalRoot = home.RootState
	tasksDir      = "task-authority/tasks"
	holdsDir      = "task-authority/holds"
	receiptsDir   = "task-authority/receipts"
)

func taskCurrentKey(taskID string) string { return tasksDir + "/" + taskID + "/current.json" }
func taskGenKey(taskID string, gen uint64) string {
	return tasksDir + "/" + taskID + "/gen-" + strconv.FormatUint(gen, 10) + ".json"
}
func holdKey(holdID string) string  { return holdsDir + "/" + holdID + ".json" }
func receiptKey(opID string) string { return receiptsDir + "/" + opID + ".json" }

// taskScope and holdScope derive a safe home lock scope from a typed identity
// value. Hex encoding is deterministic, collision-free, and always a valid
// home scope (no dots or separators).
func taskScope(taskID string) string { return "task-" + hex.EncodeToString([]byte(taskID)) }
func holdScope(holdID string) string { return "hold-" + hex.EncodeToString([]byte(holdID)) }

// taskDoc is the durable envelope of one task's current generation. It stores
// the home scope revision alongside the v1 Task Aggregate so the next
// mutation can pass the correct optimistic expectedRevision to home.Commit.
type taskDoc struct {
	HomeRevision uint64    `json:"home_revision"`
	Aggregate    Aggregate `json:"aggregate"`
}

// holdDoc is the durable envelope of one dispatch hold.
type holdDoc struct {
	HomeRevision uint64       `json:"home_revision"`
	Hold         DispatchHold `json:"hold"`
}

// receipt is the durable record of one committed Operation identity. It pins
// the operation's intent digest and the committed outcome so replay returns
// the original result and a changed digest fails closed as a conflict.
type receipt struct {
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	TaskID      string `json:"task_id,omitempty"`
	Generation  uint64 `json:"generation,omitempty"`
	Revision    uint64 `json:"revision,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Reopened    bool   `json:"reopened,omitempty"`
	HoldID      string `json:"hold_id,omitempty"`
}

func (r receipt) outcome() Outcome {
	out := Outcome{
		Generation: Generation(r.Generation),
		Revision:   Revision(r.Revision),
		Phase:      Phase(r.Phase),
		Reopened:   r.Reopened,
		Replayed:   true,
	}
	if r.TaskID != "" {
		if id, err := domain.NewTaskID(r.TaskID); err == nil {
			out.TaskID = id
		}
	}
	return out
}

func receiptFor(op domain.Operation, agg Aggregate) receipt {
	return receipt{
		OperationID: op.ID.Value(),
		Digest:      op.Digest,
		TaskID:      agg.TaskID,
		Generation:  uint64(agg.Generation),
		Revision:    uint64(agg.Revision),
		Phase:       string(agg.Phase),
	}
}

func outcomeFor(op domain.Operation, agg Aggregate, reopened bool) Outcome {
	id, _ := domain.NewTaskID(agg.TaskID)
	return Outcome{
		TaskID:     id,
		Generation: agg.Generation,
		Revision:   agg.Revision,
		Phase:      agg.Phase,
		Reopened:   reopened,
	}
}

// readDoc reads one canonical document under the home state root, reporting
// absence via the boolean. All document reads go through home.Read.
func (c *Canonical) readDoc(key string) ([]byte, bool, error) {
	data, err := c.h.Read(canonicalRoot, key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// readTaskDoc reads and validates the current generation document of a task.
// Malformed current state fails closed instead of being served.
func (c *Canonical) readTaskDoc(taskID string) (taskDoc, bool, error) {
	data, ok, err := c.readDoc(taskCurrentKey(taskID))
	if err != nil || !ok {
		return taskDoc{}, ok, err
	}
	var doc taskDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return taskDoc{}, true, internalError("decode task %s document: %v", taskID, err)
	}
	if err := validateAggregate(doc.Aggregate); err != nil {
		return taskDoc{}, true, internalError("task %s has malformed current state: %v", taskID, err)
	}
	return doc, true, nil
}

func (c *Canonical) readHoldDoc(holdID string) (holdDoc, bool, error) {
	data, ok, err := c.readDoc(holdKey(holdID))
	if err != nil || !ok {
		return holdDoc{}, ok, err
	}
	var doc holdDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return holdDoc{}, true, internalError("decode dispatch hold %s document: %v", holdID, err)
	}
	if err := validateHold(doc.Hold); err != nil {
		return holdDoc{}, true, internalError("dispatch hold %s has malformed state: %v", holdID, err)
	}
	return doc, true, nil
}

// listTaskIDs enumerates the committed task IDs by reading the tasks
// directory under the verified home state root. Individual documents are
// read through home.Read; this is a read-only query over canonical state.
func (c *Canonical) listTaskIDs() ([]string, error) {
	path, err := c.h.Path(canonicalRoot, tasksDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (c *Canonical) listHoldIDs() ([]string, error) {
	path, err := c.h.Path(canonicalRoot, holdsDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// checkedReceipt returns the committed receipt for an operation. A matching
// digest means the operation already committed (replay); a different digest
// is a non-retryable operation identity conflict.
func (c *Canonical) checkedReceipt(op domain.Operation) (receipt, bool, error) {
	data, ok, err := c.readDoc(receiptKey(op.ID.Value()))
	if err != nil || !ok {
		return receipt{}, false, err
	}
	var rec receipt
	if err := json.Unmarshal(data, &rec); err != nil {
		return receipt{}, true, internalError("decode operation receipt %s: %v", op.ID.Value(), err)
	}
	if rec.Digest != op.Digest {
		return receipt{}, false, conflictError(ErrOperationConflict, "operation %s reused with different intent", op.ID.Value())
	}
	return rec, true, nil
}

// prepare validates the operation identity, verifies the digest derives from
// the typed intent, and binds the operation to this canonical home.
func (c *Canonical) prepare(op domain.Operation, intent domain.Intent, homeID domain.HomeID) error {
	if err := op.Validate(); err != nil {
		return err
	}
	want, err := domain.Digest(intent)
	if err != nil {
		return err
	}
	if op.Digest != want {
		return validationError("operation %s digest does not match its typed intent", op.ID.Value())
	}
	return c.verifyHome(homeID)
}

func (c *Canonical) verifyHome(homeID domain.HomeID) error {
	if homeID != c.homeID {
		return conflictError(ErrConflict, "operation targets home %s but authority is bound to %s", homeID.Canonical(), c.homeID.Canonical())
	}
	return nil
}

// verifyPrecondition fails closed with a typed domain.Conflict when the
// committed aggregate's generation/revision does not match the precondition.
// Conflicts originate only here and from a home.ErrConflict commit error.
func verifyPrecondition(taskID domain.TaskID, agg Aggregate, prec domain.Precondition) error {
	actualGen, actualRev := uint64(agg.Generation), uint64(agg.Revision)
	if actualGen == prec.Generation && actualRev == prec.Revision {
		return nil
	}
	conflict, ok := domain.ConflictFrom(taskID, prec, home.ErrConflict, func(e error) bool { return errors.Is(e, home.ErrConflict) })
	if !ok {
		return conflictError(ErrConflict, "task %s stale precondition", taskID.Value())
	}
	return conflict.WithActual(actualGen, actualRev)
}

// commitError maps a home.Commit conflict (optimistic revision mismatch) to a
// typed domain.Conflict; every other storage error keeps its category.
func commitError(taskID domain.TaskID, prec domain.Precondition, err error) error {
	if errors.Is(err, home.ErrConflict) {
		conflict, ok := domain.ConflictFrom(taskID, prec, err, func(e error) bool { return errors.Is(e, home.ErrConflict) })
		if ok {
			return conflict
		}
	}
	return err
}

// mutateTask runs one task-scoped lifecycle mutation under the task's fenced
// scope lock: receipt idempotency first, then precondition verification, then
// one atomic home.Commit that writes the new current document and the
// operation receipt together.
func (c *Canonical) mutateTask(op domain.Operation, taskID domain.TaskID, prec domain.Precondition, apply func(Aggregate) (Aggregate, error)) (Outcome, error) {
	if err := op.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := prec.Validate(); err != nil {
		return Outcome{}, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return Outcome{}, err
	}
	if !exists {
		return Outcome{}, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	if err := verifyPrecondition(taskID, doc.Aggregate, prec); err != nil {
		return Outcome{}, err
	}
	next, err := apply(doc.Aggregate)
	if err != nil {
		return Outcome{}, err
	}

	newDoc := taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: next}
	rec := receiptFor(op, next)
	items, err := taskItems(taskID.Value(), newDoc, rec)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(taskID, prec, err)
	}
	return outcomeFor(op, next, false), nil
}

func taskItems(taskID string, doc taskDoc, rec receipt) ([]home.ChangeItem, error) {
	docData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(taskID), Data: docData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}, nil
}

func holdItems(holdID string, doc holdDoc, rec receipt) ([]home.ChangeItem, error) {
	docData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: canonicalRoot, Key: holdKey(holdID), Data: docData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}, nil
}

// phaseChanged advances one aggregate's phase and revision.
func phaseChanged(cur Aggregate, after Phase, detail, reason string) Aggregate {
	next := cur.clone()
	next.Phase = after
	next.PhaseDetail = detail
	next.Revision++
	return next
}
