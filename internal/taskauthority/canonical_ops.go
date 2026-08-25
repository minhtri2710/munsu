package taskauthority

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// CreateRequest creates one new queued task at Generation 1, Revision 1. The
// request is the typed intent for the operation digest.
type CanonicalCreateRequest struct {
	HomeID                 domain.HomeID
	TaskID                 domain.TaskID
	Owner                  string
	Description            string
	Kind                   string
	Project                domain.ProjectID
	ParentTaskID           domain.TaskID
	ScoutScope             string
	ScoutRuntimeBudgetSecs int64
	Reason                 string
}

func (r CanonicalCreateRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID                 string `json:"home_id"`
		TaskID                 string `json:"task_id"`
		Owner                  string `json:"owner"`
		Description            string `json:"description"`
		Kind                   string `json:"kind"`
		Project                string `json:"project"`
		ParentTaskID           string `json:"parent_task_id"`
		ScoutScope             string `json:"scout_scope,omitempty"`
		ScoutRuntimeBudgetSecs int64  `json:"scout_runtime_budget_secs,omitempty"`
		Reason                 string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Owner, r.Description, r.Kind, r.Project.Value(), r.ParentTaskID.Value(), r.ScoutScope, r.ScoutRuntimeBudgetSecs, r.Reason})
}

// Create is the canonical operation that creates one queued Task Generation.
func (c *Canonical) Create(op domain.Operation, req CanonicalCreateRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := req.TaskID.Validate(); err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(req.Owner) == "" {
		return Outcome{}, validationError("create requires an owner")
	}
	if err := validateScoutContract(TaskDefinition{Kind: req.Kind, ScoutScope: req.ScoutScope, ScoutRuntimeBudgetSecs: req.ScoutRuntimeBudgetSecs}); err != nil {
		return Outcome{}, err
	}

	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	if _, exists, err := c.readTaskDoc(req.TaskID.Value()); err != nil {
		return Outcome{}, err
	} else if exists {
		return Outcome{}, conflictError(ErrConflict, "task %s already exists", req.TaskID.Value())
	}

	agg, err := NewAggregate(req.TaskID.Value(), req.Owner, req.Description, req.Kind, req.Project.Value(), req.ParentTaskID.Value())
	if err == nil {
		agg.Definition.ScoutScope = strings.TrimSpace(req.ScoutScope)
		agg.Definition.ScoutRuntimeBudgetSecs = req.ScoutRuntimeBudgetSecs
		err = validateScoutContract(agg.Definition)
	}
	if err != nil {
		return Outcome{}, err
	}
	doc := taskDoc{HomeRevision: 1, Aggregate: agg}
	rec := receiptFor(op, agg)
	items, err := taskItems(req.TaskID.Value(), doc, rec)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), 0, items); err != nil {
		return Outcome{}, commitError(req.TaskID, domain.Precondition{}, err)
	}
	return outcomeFor(op, agg, false), nil
}

// Get returns the current authoritative Task Aggregate for the task. It is a
// current-Task-truth query: a superseded/non-current generation is not returned
// as current truth (it fails closed with ErrNotFound). Malformed current state
// fails closed.
func (c *Canonical) Get(taskID domain.TaskID) (Aggregate, error) {
	if err := taskID.Validate(); err != nil {
		return Aggregate{}, err
	}
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return Aggregate{}, err
	}
	if !exists || !doc.Aggregate.Current {
		return Aggregate{}, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	return doc.Aggregate.clone(), nil
}

// CurrentLocked returns the current authoritative Task Aggregate under the
// task-scope lock. Unlike Get (a lock-free committed read), this read
// serializes against concurrent canonical mutations for the same task
// (Reopen/BindEndpoint/Retire), so a caller can re-read and compare-and-fence
// immediately before a destructive external action (BEO-16/P1a retirement
// TOCTOU guard): a mutation either commits before the locked read (observed)
// or after it, never in between. It returns the same current-Task-truth
// contract as Get: a superseded/non-current generation fails closed with
// ErrNotFound.
// ReconcileRetirementCleanup runs bounded local data-path work and commits the
// terminal cleanup state under one task-scope lock. Handoff scope precedes task
// scope; callbacks must not acquire another lock scope.
func (c *Canonical) ReconcileRetirementCleanup(taskID domain.TaskID, generation Generation, terminal CleanupStatus, work func() error, after ...func() error) error {
	if work == nil {
		return fmt.Errorf("cleanup callback is nil")
	}
	if terminal != CleanupCompleted && terminal != CleanupAborted {
		return fmt.Errorf("invalid cleanup terminal status %q", terminal)
	}
	if err := taskID.Validate(); err != nil {
		return err
	}
	if err := generation.Validate(); err != nil {
		return err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return err
	}
	defer lk.Release()
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return err
	}
	if !exists || !doc.Aggregate.Current || doc.Aggregate.Phase != PhaseRetired || doc.Aggregate.CleanupClaim == nil || doc.Aggregate.CleanupClaim.Generation != generation {
		return conflictError(ErrConflict, "task %s generation %s cleanup claim is not active", taskID, generation)
	}
	if doc.Aggregate.CleanupClaim.Status != CleanupActive {
		return conflictError(ErrConflict, "task %s generation %s cleanup claim is not active", taskID, generation)
	}
	claimID := doc.Aggregate.CleanupClaim.OperationID
	var req domain.Intent
	if terminal == CleanupCompleted {
		req = CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(doc.Aggregate.Generation), uint64(doc.Aggregate.Revision)), ClaimOperationID: claimID, ClaimGeneration: generation, Reason: "retirement cleanup complete"}
	} else {
		req = CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(doc.Aggregate.Generation), uint64(doc.Aggregate.Revision)), ClaimOperationID: claimID, ClaimGeneration: generation, Reason: "retirement cleanup abort"}
	}
	opID, err := domain.NewOperationID(fmt.Sprintf("cleanup-%s-%s-%d", terminal, taskID.Value(), time.Now().UnixNano()))
	if err != nil {
		return err
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		return err
	}
	if err := work(); err != nil {
		return err
	}
	_, err = c.mutateTaskFencedLocked(lk, op, taskID, domain.Of(uint64(doc.Aggregate.Generation), uint64(doc.Aggregate.Revision)), func(cur Aggregate) (Aggregate, error) {
		next := cur.clone()
		next.CleanupClaim.Status = terminal
		next.CleanupClaim.ReconciledAt = c.now().UnixNano()
		next.Revision++
		return next, nil
	}, nil, &cleanupGate{operationID: claimID, generation: generation})
	if err != nil {
		return err
	}
	if len(after) > 0 && after[0] != nil {
		return after[0]()
	}
	return nil
}

// WriteTaskDataArtifact serializes a bounded task-data write with
// ReclaimReleasedTaskArtifacts so a brief writer cannot race reclamation.
// Handoff scope precedes task scope; callbacks may perform only bounded local
// filesystem work and must not acquire another lock scope.
func (c *Canonical) WriteTaskDataArtifact(taskID domain.TaskID, write func() error) error {
	return c.WriteTaskDataArtifactByID(taskID.Value(), write)
}

// WriteTaskDataArtifactByID provides synchronization only for a raw durable
// task-data ID; it does not authorize the write.
func (c *Canonical) WriteTaskDataArtifactByID(id string, write func() error) error {
	if write == nil {
		return fmt.Errorf("task-data callback is nil")
	}
	lk, err := c.h.Lock(taskScope(id))
	if err != nil {
		return err
	}
	defer lk.Release()
	return write()
}

// ReconcileCompletedCleanup runs only bounded local projection work while the
// completed task remains fenced; callbacks must not acquire another lock scope.
func (c *Canonical) ReconcileCompletedCleanup(taskID domain.TaskID, generation Generation, work func() error) error {
	if work == nil {
		return fmt.Errorf("cleanup callback is nil")
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return err
	}
	defer lk.Release()
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return err
	}
	if !exists || !doc.Aggregate.Current || doc.Aggregate.Phase != PhaseRetired || doc.Aggregate.CleanupClaim == nil || doc.Aggregate.CleanupClaim.Status != CleanupCompleted || doc.Aggregate.CleanupClaim.Generation != generation {
		return conflictError(ErrConflict, "task %s generation %s cleanup is not completed", taskID, generation)
	}
	return work()
}

// ReclaimReleasedTaskArtifacts holds the task scope through the bounded local
// removal callback; callbacks must not acquire another lock scope.
func (c *Canonical) ReclaimReleasedTaskArtifacts(taskID domain.TaskID, reclaim func() error) (bool, error) {
	return c.ReclaimReleasedTaskArtifactsByID(taskID.Value(), reclaim)
}

func (c *Canonical) ReclaimReleasedTaskArtifactsByID(id string, reclaim func() error) (bool, error) {
	if reclaim == nil {
		return false, fmt.Errorf("reclaim callback is nil")
	}
	lk, err := c.h.Lock(taskScope(id))
	if err != nil {
		return false, err
	}
	defer lk.Release()
	var taskID domain.TaskID
	var doc taskDoc
	var exists bool
	if parsed, parseErr := domain.NewTaskID(id); parseErr == nil {
		taskID = parsed
		doc, exists, err = c.readTaskDoc(taskID.Value())
		if err != nil {
			return false, err
		}
	}
	if exists && doc.Aggregate.Current {
		agg := doc.Aggregate
		if agg.Phase != PhaseRetired {
			return false, nil
		}
		if claim := agg.CleanupClaim; claim != nil {
			switch claim.Status {
			case CleanupCompleted, CleanupAborted:
			default:
				return false, nil
			}
		} else {
			return false, nil
		}
	}
	if err := reclaim(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Canonical) CurrentLocked(taskID domain.TaskID) (Aggregate, error) {
	if err := taskID.Validate(); err != nil {
		return Aggregate{}, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return Aggregate{}, err
	}
	defer lk.Release()
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return Aggregate{}, err
	}
	if !exists || !doc.Aggregate.Current {
		return Aggregate{}, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	return doc.Aggregate.clone(), nil
}

// GetGeneration returns the stored generation document for a task as a narrow
// historical/audit read. It is NOT a current-Task-truth query: a superseded or
// non-current generation is returned by its exact generation for audit and
// transfer-recovery evidence, and a caller distinguishing current truth must
// use Get. If the task has no document for the requested generation it fails
// closed with ErrNotFound.
func (c *Canonical) GetGeneration(taskID domain.TaskID, gen Generation) (Aggregate, error) {
	if err := taskID.Validate(); err != nil {
		return Aggregate{}, err
	}
	if err := gen.Validate(); err != nil {
		return Aggregate{}, err
	}
	// Prefer the current document when it is the requested generation (it may
	// be a superseded source still stored at current.json for audit); otherwise
	// read the generation document.
	if doc, exists, err := c.readTaskDoc(taskID.Value()); err != nil {
		return Aggregate{}, err
	} else if exists && doc.Aggregate.Generation == gen {
		return doc.Aggregate.clone(), nil
	}
	doc, exists, err := c.readGenDoc(taskID.Value(), uint64(gen))
	if err != nil {
		return Aggregate{}, err
	}
	if !exists {
		return Aggregate{}, conflictError(ErrNotFound, "task %s generation %s not found", taskID.Value(), gen)
	}
	return doc.Aggregate.clone(), nil
}

// List returns the current authoritative Task Aggregates sorted by task ID.
// It is a current-Task-truth query: superseded/non-current generations are
// excluded.
func (c *Canonical) List() ([]Aggregate, error) {
	ids, err := c.listTaskIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Aggregate, 0, len(ids))
	for _, id := range ids {
		doc, exists, err := c.readTaskDoc(id)
		if err != nil {
			return nil, err
		}
		if !exists || !doc.Aggregate.Current {
			continue
		}
		out = append(out, doc.Aggregate.clone())
	}
	return out, nil
}

// StartRequest starts a queued task into working.
type CanonicalStartRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Reason       string
}

func (r CanonicalStartRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		TaskID       string `json:"task_id"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), struct {
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
	}{r.Precondition.Generation, r.Precondition.Revision}, r.Reason})
}

// Start is the canonical operation that transitions a queued task to working,
// evaluating Dispatch Holds against the committed hold set.
func (c *Canonical) Start(op domain.Operation, req CanonicalStartRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase != PhaseQueued {
			return Aggregate{}, preconditionError("start requires queued task")
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, err
		}
		if holdsBlockStart(holds, cur) {
			return Aggregate{}, conflictError(ErrDispatchHeld, "%s: dispatch is held for %s", ErrDispatchHeld, cur.TaskID)
		}
		return phaseChanged(cur, PhaseWorking, "", req.Reason), nil
	})
}

// BlockRequest blocks a queued or working task.
type CanonicalBlockRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Detail       string
	Reason       string
}

func (r CanonicalBlockRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		Detail     string `json:"detail"`
		Reason     string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Detail, r.Reason})
}

// Block is the canonical operation that transitions a queued or working task
// into blocked.
func (c *Canonical) Block(op domain.Operation, req CanonicalBlockRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase != PhaseQueued && cur.Phase != PhaseWorking {
			return Aggregate{}, preconditionError("block requires queued or working task")
		}
		return phaseChanged(cur, PhaseBlocked, req.Detail, req.Reason), nil
	})
}

// UnblockRequest unblocks a blocked task back to queued.
type CanonicalUnblockRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Reason       string
}

func (r CanonicalUnblockRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		Reason     string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Reason})
}

// Unblock is the canonical operation that returns a blocked task to queued.
func (c *Canonical) Unblock(op domain.Operation, req CanonicalUnblockRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase != PhaseBlocked {
			return Aggregate{}, preconditionError("unblock requires blocked task")
		}
		return phaseChanged(cur, PhaseQueued, "", req.Reason), nil
	})
}

// CompleteRequest completes a non-terminal task into a terminal phase.
type CanonicalCompleteRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	To           Phase
	Reason       string
}

func (r CanonicalCompleteRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		To         Phase  `json:"to"`
		Reason     string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.To, r.Reason})
}

// Complete is the canonical operation that transitions a non-terminal task
// into a terminal phase (done or resolved).
func (c *Canonical) Complete(op domain.Operation, req CanonicalCompleteRequest) (Outcome, error) {
	if req.To != PhaseDone && req.To != PhaseResolved {
		return Outcome{}, validationError("complete target %q is not a terminal phase", req.To)
	}
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase.terminal() {
			return Aggregate{}, preconditionError("complete requires a non-terminal task")
		}
		return phaseChanged(cur, req.To, "", req.Reason), nil
	})
}

// ReopenRequest reopens a terminal task into a new Generation.
type CanonicalReopenRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Reason       string
}

func (r CanonicalReopenRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		Reason     string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Reason})
}

// Reopen is the canonical operation that creates the next Task Generation at
// Revision 1 and preserves the prior Generation as a historical record.
func (c *Canonical) Reopen(op domain.Operation, req CanonicalReopenRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := req.Precondition.Validate(); err != nil {
		return Outcome{}, err
	}
	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	doc, exists, err := c.readTaskDoc(req.TaskID.Value())
	if err != nil {
		return Outcome{}, err
	}
	if !exists {
		return Outcome{}, conflictError(ErrNotFound, "task %s not found", req.TaskID.Value())
	}
	cur := doc.Aggregate
	if err := verifyPrecondition(req.TaskID, cur, req.Precondition); err != nil {
		return Outcome{}, err
	}
	if err := c.checkMutableCurrent(cur); err != nil {
		return Outcome{}, err
	}
	if err := c.checkReservationFence(cur, nil); err != nil {
		return Outcome{}, err
	}
	// An ACTIVE cleanup claim pins the task until cleanup completes or aborts:
	// a retired-but-unreconciled generation can never be reopened (BEO-16/P1a
	// durable disposal claim).
	if err := c.checkCleanupFence(cur, nil); err != nil {
		return Outcome{}, err
	}
	if !cur.Phase.terminal() {
		return Outcome{}, preconditionError("reopen requires terminal task")
	}
	next, err := cur.Generation.Next()
	if err != nil {
		return Outcome{}, err
	}
	historical := cur.clone()
	historical.Current = false
	historical.Revision++
	newGen := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        cur.TaskID,
		Generation:    next,
		Revision:      FirstRevision,
		Current:       true,
		Definition:    cur.Definition,
		Phase:         PhaseQueued,
	}
	if err := validateAggregate(newGen); err != nil {
		return Outcome{}, err
	}
	newDoc := taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: newGen}
	rec := receiptFor(op, newGen)
	rec.Reopened = true
	// The superseded generation is preserved in the same taskDoc envelope as
	// every other generation document (the transfer path and readGenDoc), so
	// GetGeneration can reread its historical evidence (retirement/transfer)
	// after reopen.
	histDoc := taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: historical}
	histData, err := json.Marshal(histDoc)
	if err != nil {
		return Outcome{}, err
	}
	docData, err := json.Marshal(newDoc)
	if err != nil {
		return Outcome{}, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return Outcome{}, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskGenKey(cur.TaskID, uint64(historical.Generation)), Data: histData},
		{Root: canonicalRoot, Key: taskCurrentKey(cur.TaskID), Data: docData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.TaskID, req.Precondition, err)
	}
	return outcomeFor(op, newGen, true), nil
}

// Readiness evaluates the current authoritative task state against the start
// action's durable Dispatch Holds.
func (c *Canonical) Readiness(taskID domain.TaskID) (Readiness, error) {
	if err := taskID.Validate(); err != nil {
		return Readiness{}, err
	}
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return Readiness{}, err
	}
	if !exists {
		return Readiness{TaskID: taskID.Value(), BlockingReasons: []ReadinessReason{ReadinessNotFound}}, nil
	}
	holds, err := c.listHolds()
	if err != nil {
		return Readiness{}, err
	}
	return evaluateReadiness(holds, doc.Aggregate), nil
}

// AddHoldRequest creates one durable dispatch hold.
type CanonicalAddHoldRequest struct {
	HomeID  domain.HomeID
	HoldID  string
	Scope   DispatchHoldScope
	Actions []DispatchAction
	Reason  string
}

func (r CanonicalAddHoldRequest) DigestBytes() ([]byte, error) {
	scope := normalizeScope(r.Scope)
	return json.Marshal(struct {
		HomeID  string            `json:"home_id"`
		HoldID  string            `json:"hold_id"`
		Scope   DispatchHoldScope `json:"scope"`
		Actions []DispatchAction  `json:"actions"`
		Reason  string            `json:"reason"`
	}{r.HomeID.Value(), r.HoldID, scope, uniqueActions(r.Actions), r.Reason})
}

// AddHold is the canonical operation that creates one durable dispatch hold.
// Re-creating an identical active hold is a successful no-op; a different
// definition under the same ID conflicts.
func (c *Canonical) AddHold(op domain.Operation, req CanonicalAddHoldRequest) (HoldResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return HoldResult{}, err
	}
	if req.HoldID == "" || strings.ContainsAny(req.HoldID, `/\\.`) {
		return HoldResult{}, validationError("dispatch hold ID must be a safe non-empty value")
	}
	if len(req.Actions) == 0 || strings.TrimSpace(req.Reason) == "" {
		return HoldResult{}, validationError("dispatch hold requires actions and reason")
	}
	lk, err := c.h.Lock(holdScope(req.HoldID))
	if err != nil {
		return HoldResult{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return HoldResult{}, err
	} else if ok {
		return HoldResult{HoldID: rec.HoldID, Replayed: true}, nil
	}

	doc, exists, err := c.readHoldDoc(req.HoldID)
	if err != nil {
		return HoldResult{}, err
	}
	var newDoc holdDoc
	var expected uint64
	if exists {
		if doc.Hold.ReleasedAt != 0 {
			return HoldResult{}, conflictError(ErrConflict, "hold %s is already released", req.HoldID)
		}
		if !holdEquivalentRequest(doc.Hold, req) {
			return HoldResult{}, conflictError(ErrConflict, "hold %s already exists with a different definition", req.HoldID)
		}
		newDoc = holdDoc{HomeRevision: doc.HomeRevision + 1, Hold: doc.Hold}
		expected = doc.HomeRevision
	} else {
		hold := DispatchHold{
			SchemaVersion: TaskAuthoritySchema,
			ID:            req.HoldID,
			Scope:         normalizeScope(req.Scope),
			Actions:       uniqueActions(req.Actions),
			Reason:        req.Reason,
			CreatedAt:     c.now().UnixNano(),
		}
		if err := validateHold(hold); err != nil {
			return HoldResult{}, err
		}
		newDoc = holdDoc{HomeRevision: 1, Hold: hold}
		expected = 0
	}
	rec := receipt{OperationID: op.ID.Value(), Digest: op.Digest, HoldID: req.HoldID}
	items, err := holdItems(req.HoldID, newDoc, rec)
	if err != nil {
		return HoldResult{}, err
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), expected, items); err != nil {
		return HoldResult{}, err
	}
	return HoldResult{HoldID: req.HoldID}, nil
}

// ReleaseHoldRequest releases one durable dispatch hold.
type CanonicalReleaseHoldRequest struct {
	HomeID domain.HomeID
	HoldID string
	Reason string
}

func (r CanonicalReleaseHoldRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID string `json:"home_id"`
		HoldID string `json:"hold_id"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.HoldID, r.Reason})
}

// ReleaseHold is the canonical operation that releases one dispatch hold.
// Releasing an already-released hold is a successful no-op.
func (c *Canonical) ReleaseHold(op domain.Operation, req CanonicalReleaseHoldRequest) (HoldResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return HoldResult{}, err
	}
	if req.HoldID == "" || strings.ContainsAny(req.HoldID, `/\\.`) {
		return HoldResult{}, validationError("dispatch hold ID must be a safe non-empty value")
	}
	lk, err := c.h.Lock(holdScope(req.HoldID))
	if err != nil {
		return HoldResult{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return HoldResult{}, err
	} else if ok {
		return HoldResult{HoldID: rec.HoldID, Replayed: true}, nil
	}

	doc, exists, err := c.readHoldDoc(req.HoldID)
	if err != nil {
		return HoldResult{}, err
	}
	if !exists {
		return HoldResult{}, conflictError(ErrHoldNotFound, "dispatch hold %s not found", req.HoldID)
	}
	var newDoc holdDoc
	if doc.Hold.ReleasedAt != 0 {
		newDoc = holdDoc{HomeRevision: doc.HomeRevision + 1, Hold: doc.Hold}
	} else {
		updated := doc.Hold.clone()
		updated.ReleasedAt = c.now().UnixNano()
		newDoc = holdDoc{HomeRevision: doc.HomeRevision + 1, Hold: updated}
	}
	rec := receipt{OperationID: op.ID.Value(), Digest: op.Digest, HoldID: req.HoldID}
	items, err := holdItems(req.HoldID, newDoc, rec)
	if err != nil {
		return HoldResult{}, err
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return HoldResult{}, err
	}
	return HoldResult{HoldID: req.HoldID}, nil
}

// ListHolds returns the committed dispatch holds sorted by ID.
func (c *Canonical) ListHolds() ([]DispatchHold, error) {
	ids, err := c.listHoldIDs()
	if err != nil {
		return nil, err
	}
	out := make([]DispatchHold, 0, len(ids))
	for _, id := range ids {
		doc, exists, err := c.readHoldDoc(id)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		out = append(out, doc.Hold.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// listHolds returns the committed dispatch holds (unsorted) for readiness
// evaluation.
func (c *Canonical) listHolds() ([]DispatchHold, error) {
	ids, err := c.listHoldIDs()
	if err != nil {
		return nil, err
	}
	out := make([]DispatchHold, 0, len(ids))
	for _, id := range ids {
		doc, exists, err := c.readHoldDoc(id)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		out = append(out, doc.Hold.clone())
	}
	return out, nil
}

// holdEquivalentRequest reports whether an existing active hold matches the
// add-hold request definition.
func holdEquivalentRequest(hold DispatchHold, req CanonicalAddHoldRequest) bool {
	if hold.Reason != req.Reason {
		return false
	}
	actions := uniqueActions(req.Actions)
	if len(hold.Actions) != len(actions) {
		return false
	}
	for i := range hold.Actions {
		if hold.Actions[i] != actions[i] {
			return false
		}
	}
	return scopesEqual(hold.Scope, normalizeScope(req.Scope))
}
