package taskauthority

import (
	"fmt"
	"sort"
	"strings"
)

// Store is the transactional implementation seam below the Authority. It
// exposes consistent committed views and staged authoritative changes, not
// lifecycle verbs. Business decisions execute in the Authority; the adapter
// owns locking, atomic replacement, serialization, and crash recovery.
type Store interface {
	// View returns one consistent committed snapshot of authoritative records.
	View() (View, error)
	// Update serializes one authoritative mutation. fn stages changes against
	// the committed snapshot; an error rolls back without mutation. Repeating
	// a committed Operation ID with the same digest returns the original
	// receipt without re-running fn; a changed digest conflicts.
	Update(op Operation, fn func(tx *Tx) error) (Receipt, error)
}

// View is one consistent committed snapshot of authoritative records.
type View struct {
	Aggregates      []Aggregate
	Holds           []DispatchHold
	Interpretations []DispatchInterpretation
	Decisions       []DispatchDecision
	Receipts        []Receipt
	Audit           []AuditEvent
}

// Current returns the current Generation of the task, if any.
func (v View) Current(taskID string) (Aggregate, bool) {
	for _, agg := range v.Aggregates {
		if agg.TaskID == taskID && agg.Current {
			return agg, true
		}
	}
	return Aggregate{}, false
}

// Aggregate returns the record for one task generation, if any.
func (v View) Aggregate(taskID string, generation Generation) (Aggregate, bool) {
	for _, agg := range v.Aggregates {
		if agg.TaskID == taskID && agg.Generation == generation {
			return agg, true
		}
	}
	return Aggregate{}, false
}

// Hold returns the named dispatch hold, if any.
func (v View) Hold(id string) (DispatchHold, bool) {
	for _, hold := range v.Holds {
		if hold.ID == id {
			return hold, true
		}
	}
	return DispatchHold{}, false
}

// sortedTaskIDs returns the distinct task IDs in the view.
func (v View) sortedTaskIDs() []string {
	seen := map[string]bool{}
	for _, agg := range v.Aggregates {
		if agg.Current {
			seen[agg.TaskID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ChangeApplier receives the staged changes of one committed transaction. The
// filesystem adapter implements it with journaled writes; the in-memory
// adapter applies to a private copy.
type ChangeApplier interface {
	ApplyAggregate(agg Aggregate) error
	ApplyHold(hold DispatchHold) error
	ApplyInterpretation(rec DispatchInterpretation) error
	ApplyDecision(dec DispatchDecision) error
	ApplyAudit(ev AuditEvent) error
}

type stagedChange struct {
	kind  string
	agg   Aggregate
	hold  DispatchHold
	rec   DispatchInterpretation
	dec   DispatchDecision
	audit AuditEvent
}

// Tx stages one authoritative transaction against a committed snapshot.
// Reads see only committed state; staged changes apply atomically at commit.
type Tx struct {
	view          View
	changes       []stagedChange
	keys          map[string]bool
	auditAppended bool
}

// NewTx opens a transaction over the given committed view.
func NewTx(view View) *Tx {
	return &Tx{view: view, keys: map[string]bool{}}
}

// Current returns the committed current generation of the task, if any.
func (tx *Tx) Current(taskID string) (Aggregate, bool) {
	return tx.view.Current(taskID)
}

// Hold returns the committed named hold, if any.
func (tx *Tx) Hold(id string) (DispatchHold, bool) {
	return tx.view.Hold(id)
}

// Holds returns the committed dispatch holds.
func (tx *Tx) Holds() []DispatchHold {
	return append([]DispatchHold(nil), tx.view.Holds...)
}

// PutAggregate stages an authoritative aggregate replacement. Record-level
// validation runs immediately; lifecycle rules are not evaluated here.
func (tx *Tx) PutAggregate(agg Aggregate) error {
	if err := validateAggregate(agg); err != nil {
		return err
	}
	key := agg.TaskID + "\x00" + agg.Generation.String()
	if tx.keys[key] {
		return conflictError(ErrConflict, "duplicate aggregate staged for %s generation %s", agg.TaskID, agg.Generation)
	}
	tx.keys[key] = true
	tx.changes = append(tx.changes, stagedChange{kind: "aggregate", agg: agg.clone()})
	return nil
}

// PutHold stages a dispatch hold replacement.
func (tx *Tx) PutHold(hold DispatchHold) error {
	if err := validateHold(hold); err != nil {
		return err
	}
	tx.changes = append(tx.changes, stagedChange{kind: "hold", hold: hold.clone()})
	return nil
}

// PutInterpretation stages a dispatch interpretation record.
func (tx *Tx) PutInterpretation(rec DispatchInterpretation) error {
	if rec.ID == "" {
		return validationError("dispatch interpretation missing id")
	}
	cp := rec
	cp.RequestedOrder = append([]string(nil), rec.RequestedOrder...)
	cp.SelectedTasks = append([]string(nil), rec.SelectedTasks...)
	cp.Evidence = append([]DispatchEvidence(nil), rec.Evidence...)
	cp.ComputedReadiness = append([]DispatchReadiness(nil), rec.ComputedReadiness...)
	tx.changes = append(tx.changes, stagedChange{kind: "interpretation", rec: cp})
	return nil
}

// PutDecision stages a dispatch decision replacement.
func (tx *Tx) PutDecision(dec DispatchDecision) error {
	if err := validateDecision(dec); err != nil {
		return err
	}
	tx.changes = append(tx.changes, stagedChange{kind: "decision", dec: dec})
	return nil
}

// AppendAudit stages a typed audit event for the transaction. At most one
// typed audit event may commit per operation: the audit identity is the
// operation id, so a second event would collide on the same record.
func (tx *Tx) AppendAudit(ev AuditEvent) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	if tx.auditAppended {
		return validationError("transaction stages multiple audit events; one typed audit event per operation is supported")
	}
	tx.auditAppended = true
	tx.changes = append(tx.changes, stagedChange{kind: "audit", audit: ev})
	return nil
}

// Apply validates the full staged set, then replays every change through the
// applier. A staged set must keep at most one current generation per task.
func (tx *Tx) Apply(applier ChangeApplier) error {
	if err := tx.validateStaged(); err != nil {
		return err
	}
	for _, change := range tx.changes {
		switch change.kind {
		case "aggregate":
			if err := applier.ApplyAggregate(change.agg); err != nil {
				return fmt.Errorf("applying aggregate: %w", err)
			}
		case "hold":
			if err := applier.ApplyHold(change.hold); err != nil {
				return fmt.Errorf("applying dispatch hold: %w", err)
			}
		case "interpretation":
			if err := applier.ApplyInterpretation(change.rec); err != nil {
				return fmt.Errorf("applying dispatch interpretation: %w", err)
			}
		case "decision":
			if err := applier.ApplyDecision(change.dec); err != nil {
				return fmt.Errorf("applying dispatch decision: %w", err)
			}
		case "audit":
			if err := applier.ApplyAudit(change.audit); err != nil {
				return fmt.Errorf("applying audit event: %w", err)
			}
		}
	}
	return nil
}

// validateStaged enforces cross-record invariants of the staged set combined
// with the committed view: at most one task's aggregates staged per
// transaction (multi-task staging is rejected rather than inventing lock
// semantics), at most one current generation staged per task, and any staged
// current must replace the committed current in the same transaction rather
// than leaving two currents.
func (tx *Tx) validateStaged() error {
	stagedTaskIDs := map[string]bool{}
	for _, change := range tx.changes {
		if change.kind == "aggregate" {
			stagedTaskIDs[change.agg.TaskID] = true
		}
	}
	if len(stagedTaskIDs) > 1 {
		ids := make([]string, 0, len(stagedTaskIDs))
		for id := range stagedTaskIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return validationError("transaction stages aggregates for multiple tasks (%s); multi-task transactions are not supported", strings.Join(ids, ", "))
	}
	stagedCurrent := map[string]bool{}
	for _, change := range tx.changes {
		if change.kind != "aggregate" {
			continue
		}
		committed, exists := tx.view.Aggregate(change.agg.TaskID, change.agg.Generation)
		if exists {
			if change.agg.Revision != committed.Revision+1 {
				return conflictError(ErrConflict, "task %s generation %s revision must advance from %d to %d", change.agg.TaskID, change.agg.Generation, committed.Revision, committed.Revision+1)
			}
		} else if change.agg.Revision != FirstRevision {
			return conflictError(ErrConflict, "new task %s generation %s must start at revision %d", change.agg.TaskID, change.agg.Generation, FirstRevision)
		}
		if !change.agg.Current {
			continue
		}
		if stagedCurrent[change.agg.TaskID] {
			return conflictError(ErrConflict, "task %s would have two current generations", change.agg.TaskID)
		}
		stagedCurrent[change.agg.TaskID] = true
	}
	for taskID := range stagedCurrent {
		committed, ok := tx.view.Current(taskID)
		if !ok {
			continue
		}
		if !tx.stagesGeneration(taskID, committed.Generation) {
			return conflictError(ErrConflict, "task %s would keep generation %s current while staging a new current", taskID, committed.Generation)
		}
	}
	return nil
}

// Outcome returns the lifecycle result staged by the transaction, if any.
// The Store persists it in the operation receipt so replay returns the
// original committed outcome rather than reconstructing it from newer state.
func (tx *Tx) Outcome() (Result, bool) {
	var result Result
	found := false
	for _, change := range tx.changes {
		if change.kind != "aggregate" || !change.agg.Current {
			continue
		}
		result = Result{
			TaskID:     change.agg.TaskID,
			Generation: change.agg.Generation,
			Revision:   change.agg.Revision,
			Phase:      change.agg.Phase,
		}
		found = true
	}
	return result, found
}

// stagesGeneration reports whether the transaction stages any change for the
// given task generation.
func (tx *Tx) stagesGeneration(taskID string, generation Generation) bool {
	for _, change := range tx.changes {
		if change.kind == "aggregate" && change.agg.TaskID == taskID && change.agg.Generation == generation {
			return true
		}
	}
	return false
}
