package taskauthority

import (
	"errors"
	"sync"
	"time"
)

// memState is the committed record set of the in-memory adapter.
type memState struct {
	aggregates      map[string]Aggregate
	holds           map[string]DispatchHold
	interpretations map[string]DispatchInterpretation
	decisions       map[string]DispatchDecision
	receipts        map[string]Receipt
	audit           []AuditEvent
}

func newMemState() *memState {
	return &memState{
		aggregates:      map[string]Aggregate{},
		holds:           map[string]DispatchHold{},
		interpretations: map[string]DispatchInterpretation{},
		decisions:       map[string]DispatchDecision{},
		receipts:        map[string]Receipt{},
	}
}

// clone deep-copies the committed state so staged application can never leak
// partial visibility into a committed view.
func (s *memState) clone() *memState {
	out := newMemState()
	for k, agg := range s.aggregates {
		out.aggregates[k] = agg.clone()
	}
	for k, hold := range s.holds {
		out.holds[k] = hold.clone()
	}
	for k, rec := range s.interpretations {
		cp := rec
		cp.RequestedOrder = append([]string(nil), rec.RequestedOrder...)
		cp.SelectedTasks = append([]string(nil), rec.SelectedTasks...)
		cp.ComputedReadiness = append([]DispatchReadiness(nil), rec.ComputedReadiness...)
		cp.Evidence = append([]DispatchEvidence(nil), rec.Evidence...)
		out.interpretations[k] = cp
	}
	for k, dec := range s.decisions {
		out.decisions[k] = dec
	}
	for k, rec := range s.receipts {
		out.receipts[k] = rec
	}
	out.audit = append([]AuditEvent(nil), s.audit...)
	return out
}

func (s *memState) view() View {
	out := View{
		Holds:           make([]DispatchHold, 0, len(s.holds)),
		Interpretations: make([]DispatchInterpretation, 0, len(s.interpretations)),
		Decisions:       make([]DispatchDecision, 0, len(s.decisions)),
		Receipts:        make([]Receipt, 0, len(s.receipts)),
		Audit:           append([]AuditEvent(nil), s.audit...),
	}
	for _, agg := range s.aggregates {
		out.Aggregates = append(out.Aggregates, agg.clone())
	}
	for _, hold := range s.holds {
		out.Holds = append(out.Holds, hold.clone())
	}
	for _, rec := range s.interpretations {
		cp := rec
		cp.RequestedOrder = append([]string(nil), rec.RequestedOrder...)
		cp.SelectedTasks = append([]string(nil), rec.SelectedTasks...)
		cp.ComputedReadiness = append([]DispatchReadiness(nil), rec.ComputedReadiness...)
		cp.Evidence = append([]DispatchEvidence(nil), rec.Evidence...)
		out.Interpretations = append(out.Interpretations, cp)
	}
	for _, dec := range s.decisions {
		out.Decisions = append(out.Decisions, dec)
	}
	for _, rec := range s.receipts {
		out.Receipts = append(out.Receipts, rec)
	}
	return out
}

func aggregateKey(taskID string, generation Generation) string {
	return taskID + "\x00" + generation.String()
}

// memStore is a test-only in-memory Store adapter. It serializes all updates
// under one transaction mutex and contains no lifecycle rules.
type memStore struct {
	mu    sync.Mutex
	state *memState
	now   func() time.Time

	// beforeCommit, when set, runs inside Update after the callback returns
	// and before staged changes are applied. It is a deterministic barrier for
	// concurrency tests; it must not block forever.
	beforeCommit func() error
}

// newMemStore constructs an empty in-memory Store adapter.
func newMemStore() *memStore {
	return &memStore{state: newMemState(), now: time.Now}
}

func (s *memStore) View() (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.view(), nil
}

func (s *memStore) Update(op Operation, fn func(tx *Tx) error) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := op.Validate(); err != nil {
		return Receipt{}, err
	}
	if receipt, ok := s.state.receipts[op.ID]; ok {
		if receipt.Digest != op.Digest {
			return Receipt{}, conflictError(ErrOperationConflict, "operation %s reused with different intent", op.ID)
		}
		receipt.Replayed = true
		return receipt, nil
	}
	tx := NewTx(s.state.view())
	if err := fn(tx); err != nil {
		return Receipt{}, err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return Receipt{}, err
		}
	}
	candidate := s.state.clone()
	if err := tx.Apply(&memApplier{state: candidate}); err != nil {
		return Receipt{}, err
	}
	s.state = candidate
	receipt := Receipt{OperationID: op.ID, Digest: op.Digest, CommittedAt: s.now().UnixNano()}
	s.state.receipts[op.ID] = receipt
	return receipt, nil
}

// memApplier applies staged changes onto a candidate copy of the state.
type memApplier struct {
	state *memState
}

func (a *memApplier) ApplyAggregate(agg Aggregate) error {
	if agg.TaskID == "" {
		return errors.New("aggregate missing task id")
	}
	a.state.aggregates[aggregateKey(agg.TaskID, agg.Generation)] = agg
	return nil
}

func (a *memApplier) ApplyHold(hold DispatchHold) error {
	a.state.holds[hold.ID] = hold
	return nil
}

func (a *memApplier) ApplyInterpretation(rec DispatchInterpretation) error {
	if rec.ID == "" {
		return errors.New("interpretation missing id")
	}
	a.state.interpretations[rec.ID] = rec
	return nil
}

func (a *memApplier) ApplyDecision(dec DispatchDecision) error {
	a.state.decisions[dec.Key] = dec
	return nil
}

func (a *memApplier) ApplyAudit(ev AuditEvent) error {
	a.state.audit = append(a.state.audit, ev)
	return nil
}

// failApplier returns a static error from every apply method; contract tests
// use it to prove partial application is rejected by the adapter.
type failApplier struct{ err error }

func (f *failApplier) ApplyAggregate(Aggregate) error { return f.err }
func (f *failApplier) ApplyHold(DispatchHold) error   { return f.err }
func (f *failApplier) ApplyInterpretation(DispatchInterpretation) error {
	return f.err
}
func (f *failApplier) ApplyDecision(DispatchDecision) error { return f.err }
func (f *failApplier) ApplyAudit(AuditEvent) error          { return f.err }
