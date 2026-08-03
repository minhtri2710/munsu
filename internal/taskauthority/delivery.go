package taskauthority

import (
	"strings"
)

// This file owns the generation-bound delivery preparation and terminal
// completion records and their named semantic operations (Task 7.5). Every
// operation commits the record inside one Store transaction with the
// Expected Generation fence revalidated inside the transaction, exactly one
// Revision advance, a typed audit event, and the durable idempotency
// receipt. Delivery writes never mutate task .meta directly: .meta is a
// post-commit projection.

// DeliveryPrepareStateReviewReady is the delivery state a preparation
// commits: the PR/MR identity is captured and the delivery awaits review.
const DeliveryPrepareStateReviewReady = "review-ready"

// Delivery terminal states. Delivered and done are distinct terminal
// transitions; resolved (the Supersede/reopen resolution phase) is never a
// delivery completion state.
const (
	DeliveryTerminalDelivered = "delivered"
	DeliveryTerminalDone      = "done"
)

// Merge outcome kinds (Task 7.6). Merged and already-merged are the
// merged-equivalent outcomes; remote-unknown is terminal — once committed,
// the Authority refuses further provider-mutating attempts and only read
// reconciliation is permitted. Verified merged truth is never erased by a
// later ambiguous (remote-unknown), false-negative (open), or stale
// closed-not-merged (failed) read.
const (
	MergeOutcomeMerged        = "merged"
	MergeOutcomeAlreadyMerged = "already-merged"
	MergeOutcomeFailed        = "failed"
	MergeOutcomeOpen          = "open"
	MergeOutcomeRemoteUnknown = "remote-unknown"
)

// DeliveryPrepare is the generation-bound delivery preparation record
// committed by pr-check: the provider identity snapshot, the immutable head
// SHA the delivery is prepared against, and the review-ready delivery state.
// It lives inside the Aggregate, so the generation binding is structural.
type DeliveryPrepare struct {
	State      string                   `json:"state"`
	HeadSHA    string                   `json:"head_sha"`
	Identity   ProviderIdentitySnapshot `json:"identity"`
	PreparedAt int64                    `json:"prepared_at"`
	Preparer   string                   `json:"preparer"`
}

// DeliveryTerminal is the generation-bound terminal evidence record: the
// terminal transition (delivered or done), the exact immutable head SHA the
// evidence binds, and the terminal provider identity snapshot. Ship
// completion never uses resolved as a delivery terminal state: the terminal
// transitions are delivered/done only, and completing delivery from a
// resolved or retired generation fails closed.
type DeliveryTerminal struct {
	Terminal    string                   `json:"terminal"`
	HeadSHA     string                   `json:"head_sha"`
	Identity    ProviderIdentitySnapshot `json:"identity"`
	CompletedAt int64                    `json:"completed_at"`
	Completer   string                   `json:"completer"`
	Reason      string                   `json:"reason,omitempty"`
}

// validateDeliveryRecord validates the generation-bound delivery records of
// one Aggregate.
func validateDeliveryRecord(agg Aggregate) error {
	if agg.DeliveryPrepare != nil {
		if err := validateDeliveryPrepareRecord(*agg.DeliveryPrepare); err != nil {
			return err
		}
	}
	if agg.DeliveryTerminal != nil {
		if err := validateDeliveryTerminalRecord(*agg.DeliveryTerminal); err != nil {
			return err
		}
	}
	if agg.MergeAttempt != nil {
		if err := validateMergeAttemptRecord(*agg.MergeAttempt); err != nil {
			return err
		}
	}
	return nil
}

// validateDeliveryPrepareRecord checks one delivery preparation: the
// review-ready state, a safe immutable head SHA, and a complete provider
// identity snapshot whose head agrees with the record head.
func validateDeliveryPrepareRecord(rec DeliveryPrepare) error {
	if rec.State != DeliveryPrepareStateReviewReady {
		return validationError("invalid delivery prepare state %q", rec.State)
	}
	if err := validateHeadSHA(rec.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(rec.Identity); err != nil {
		return err
	}
	if rec.Identity.HeadSHA != rec.HeadSHA {
		return validationError("delivery prepare head %q does not match identity snapshot head %q", rec.HeadSHA, rec.Identity.HeadSHA)
	}
	if strings.TrimSpace(rec.Preparer) == "" {
		return validationError("delivery prepare missing preparer")
	}
	if rec.PreparedAt <= 0 {
		return validationError("delivery prepare missing prepared timestamp")
	}
	return nil
}

// validateDeliveryTerminalRecord checks one terminal evidence record: the
// terminal transition must be delivered or done (never resolved), the head
// must be a safe immutable identity, and the identity snapshot head must
// agree with the record head.
func validateDeliveryTerminalRecord(rec DeliveryTerminal) error {
	switch rec.Terminal {
	case DeliveryTerminalDelivered, DeliveryTerminalDone:
	default:
		return validationError("delivery terminal %q is not a delivery completion state (delivered/done; resolved is never delivery completion)", rec.Terminal)
	}
	if err := validateHeadSHA(rec.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(rec.Identity); err != nil {
		return err
	}
	if rec.Identity.HeadSHA != rec.HeadSHA {
		return validationError("delivery terminal head %q does not match identity snapshot head %q", rec.HeadSHA, rec.Identity.HeadSHA)
	}
	if strings.TrimSpace(rec.Completer) == "" {
		return validationError("delivery terminal missing completer")
	}
	if rec.CompletedAt <= 0 {
		return validationError("delivery terminal missing completed timestamp")
	}
	return nil
}

// DeliveryResult is the caller-visible outcome of one delivery operation.
// Replayed is true when the Operation ID was already committed with the same
// intent and the original receipt was returned without re-running the
// transaction.
type DeliveryResult struct {
	TaskID     string
	Generation Generation
	Revision   Revision
	Phase      Phase
	Replayed   bool
}

// deliveryResultFromReceipt returns the caller-visible outcome of one
// delivery operation from the committed receipt. When the operation
// committed no staged change (an in-value no-op such as re-preparing the
// same identity or replaying the same terminal state), the outcome is read
// back from the committed aggregate.
func (a *Authority) deliveryResultFromReceipt(taskID string, receipt Receipt) (DeliveryResult, error) {
	if receipt.TaskID != "" || receipt.Generation != 0 || receipt.Revision != 0 {
		return DeliveryResult{
			TaskID:     receipt.TaskID,
			Generation: receipt.Generation,
			Revision:   receipt.Revision,
			Phase:      receipt.Phase,
			Replayed:   receipt.Replayed,
		}, nil
	}
	agg, err := a.Get(taskID)
	if err != nil {
		return DeliveryResult{}, err
	}
	return DeliveryResult{
		TaskID:     agg.TaskID,
		Generation: agg.Generation,
		Revision:   agg.Revision,
		Phase:      agg.Phase,
	}, nil
}

// --- PrepareDelivery ---

// PrepareDeliveryRequest is the immutable request payload of one
// generation-bound delivery preparation. It carries the exact Task
// Generation fence, the stable Task Operation identity, the actor, the
// review-ready delivery state, the provider identity snapshot, the immutable
// head SHA being prepared, and the expected prior prepared head ("" for the
// first preparation). The Operation ID is excluded from the intent digest, so
// a retry that changes the identity or the head under the same ID detects a
// conflict.
type PrepareDeliveryRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	State              string
	HeadSHA            string
	Identity           ProviderIdentitySnapshot
	ExpectedPriorHead  string
	Reason             string
}

// PrepareDelivery is the named semantic operation that commits the
// generation-bound delivery preparation record in one Store transaction:
// the Expected Generation fence is revalidated inside the transaction, the
// prior prepared head binding is enforced (a changed head must be
// acknowledged explicitly — never silent reuse), the Revision advances by
// exactly one when the preparation changes, one typed delivery-prepare audit
// event commits, and the durable idempotency receipt pins the intent.
// Re-preparing the same identity and head is an in-value no-op; re-preparing
// a changed head requires acknowledging the committed prior head. Same-op
// replay is idempotent; a stale or missing task fails closed. If the
// caller's provider verification fails before this operation, nothing
// commits — the prior authoritative phase is unchanged.
func (a *Authority) PrepareDelivery(req PrepareDeliveryRequest) (DeliveryResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return DeliveryResult{}, err
	}
	if err := validatePrepareDeliveryRequest(req); err != nil {
		return DeliveryResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                   `json:"task_id"`
		ExpectedGeneration uint64                   `json:"expected_generation"`
		State              string                   `json:"state"`
		HeadSHA            string                   `json:"head_sha"`
		Identity           ProviderIdentitySnapshot `json:"identity"`
		ExpectedPriorHead  string                   `json:"expected_prior_head,omitempty"`
		Reason             string                   `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.State, req.HeadSHA, req.Identity, req.ExpectedPriorHead, req.Reason})
	if err != nil {
		return DeliveryResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		prior := ""
		if cur.DeliveryPrepare != nil {
			prior = cur.DeliveryPrepare.HeadSHA
		}
		if prior != req.ExpectedPriorHead {
			return conflictError(ErrConflict, "task %s generation %s delivery prepare prior head %q does not match expected %q: a changed head must be re-prepared explicitly", req.TaskID, cur.Generation, prior, req.ExpectedPriorHead)
		}
		if cur.DeliveryPrepare != nil && cur.DeliveryPrepare.HeadSHA == req.HeadSHA && cur.DeliveryPrepare.Identity == req.Identity {
			return nil // in-value no-op: the same preparation is already committed
		}
		updated := cur.clone()
		rec := DeliveryPrepare{
			State:      req.State,
			HeadSHA:    req.HeadSHA,
			Identity:   req.Identity,
			PreparedAt: a.now().UnixNano(),
			Preparer:   req.Actor.ID,
		}
		updated.DeliveryPrepare = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditDeliveryPrepare,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	return a.deliveryResultFromReceipt(req.TaskID, receipt)
}

// validatePrepareDeliveryRequest validates one delivery preparation request:
// the review-ready state, a safe head, and a complete provider identity
// snapshot whose head agrees with the requested head.
func validatePrepareDeliveryRequest(req PrepareDeliveryRequest) error {
	if req.State != DeliveryPrepareStateReviewReady {
		return validationError("invalid delivery prepare state %q (expected %q)", req.State, DeliveryPrepareStateReviewReady)
	}
	if err := validateHeadSHA(req.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(req.Identity); err != nil {
		return err
	}
	if req.Identity.HeadSHA != req.HeadSHA {
		return validationError("delivery prepare head %q does not match identity snapshot head %q", req.HeadSHA, req.Identity.HeadSHA)
	}
	return nil
}

// --- CompleteDelivery ---

// CompleteDeliveryRequest is the immutable request payload of one
// generation-bound delivery completion. It carries the exact Task Generation
// fence, the stable Task Operation identity, the actor, the terminal
// transition (delivered or done), and the terminal provider evidence: the
// provider identity snapshot, the immutable head SHA, and the PR metadata.
// The Operation ID is excluded from the intent digest, so a retry that
// changes the terminal state or the evidence under the same ID detects a
// conflict.
type CompleteDeliveryRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Terminal           string
	HeadSHA            string
	Identity           ProviderIdentitySnapshot
	Reason             string
}

// CompleteDelivery is the named semantic operation that commits the
// generation-bound terminal evidence record and the delivered/done terminal
// transition in one Store transaction: the Expected Generation fence is
// revalidated inside the transaction, the lifecycle phase must not be
// resolved or retired (resolved is the Supersede/reopen resolution phase and
// is never accepted as a delivery terminal state; an attempt to complete
// delivery from a resolved/stale generation fails closed), the delivery must
// have been prepared (run pr-check first), the terminal head must equal the
// prepare-time head (the evidence binds the exact immutable head), and the
// terminal identity must identify the same PR as the preparation. The
// Revision advances by exactly one when the transition commits, one typed
// delivery-terminal audit event commits, and the durable idempotency receipt
// pins the intent. Repeating the same terminal state for the same head is an
// in-value no-op; a second distinct terminal transition conflicts (one
// terminal record per generation). Same-op replay is idempotent and returns
// the original receipt; a stale or missing task fails closed.
func (a *Authority) CompleteDelivery(req CompleteDeliveryRequest) (DeliveryResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return DeliveryResult{}, err
	}
	if err := validateCompleteDeliveryRequest(req); err != nil {
		return DeliveryResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                   `json:"task_id"`
		ExpectedGeneration uint64                   `json:"expected_generation"`
		Terminal           string                   `json:"terminal"`
		HeadSHA            string                   `json:"head_sha"`
		Identity           ProviderIdentitySnapshot `json:"identity"`
		Reason             string                   `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Terminal, req.HeadSHA, req.Identity, req.Reason})
	if err != nil {
		return DeliveryResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.Phase == PhaseResolved || cur.Phase == PhaseRetired {
			return conflictError(ErrConflict, "task %s generation %s is %s; delivery completion is a delivered/done transition, never %s (resolved is the Supersede/reopen resolution phase, not delivery completion)", req.TaskID, cur.Generation, cur.Phase, req.Terminal)
		}
		if cur.DeliveryPrepare == nil {
			return conflictError(ErrPrecondition, "task %s generation %s has no delivery preparation; run pr-check first", req.TaskID, cur.Generation)
		}
		if req.HeadSHA != cur.DeliveryPrepare.HeadSHA {
			return conflictError(ErrConflict, "task %s generation %s terminal head %q does not match prepared head %q (terminal evidence binds the exact prepared head)", req.TaskID, cur.Generation, req.HeadSHA, cur.DeliveryPrepare.HeadSHA)
		}
		if req.Identity.URL != cur.DeliveryPrepare.Identity.URL {
			return conflictError(ErrConflict, "task %s generation %s terminal identity URL %q does not match prepared identity URL %q", req.TaskID, cur.Generation, req.Identity.URL, cur.DeliveryPrepare.Identity.URL)
		}
		if cur.DeliveryTerminal != nil {
			if cur.DeliveryTerminal.Terminal == req.Terminal && cur.DeliveryTerminal.HeadSHA == req.HeadSHA {
				return nil // in-value no-op: already in this terminal state
			}
			return conflictError(ErrConflict, "task %s generation %s already completed delivery (%s at %s); one terminal transition per generation", req.TaskID, cur.Generation, cur.DeliveryTerminal.Terminal, cur.DeliveryTerminal.HeadSHA)
		}
		updated := cur.clone()
		rec := DeliveryTerminal{
			Terminal:    req.Terminal,
			HeadSHA:     req.HeadSHA,
			Identity:    req.Identity,
			CompletedAt: a.now().UnixNano(),
			Completer:   req.Actor.ID,
			Reason:      req.Reason,
		}
		updated.DeliveryTerminal = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditDeliveryTerminal,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	return a.deliveryResultFromReceipt(req.TaskID, receipt)
}

// validateCompleteDeliveryRequest validates one delivery completion request:
// the terminal transition must be delivered or done (resolved is never
// accepted as delivery completion), the head must be safe, and the provider
// identity snapshot must be complete and agree with the requested head.
func validateCompleteDeliveryRequest(req CompleteDeliveryRequest) error {
	switch req.Terminal {
	case DeliveryTerminalDelivered, DeliveryTerminalDone:
	default:
		return validationError("delivery terminal %q is not a delivery completion state (delivered/done; resolved is never delivery completion)", req.Terminal)
	}
	if err := validateHeadSHA(req.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(req.Identity); err != nil {
		return err
	}
	if req.Identity.HeadSHA != req.HeadSHA {
		return validationError("delivery terminal head %q does not match identity snapshot head %q", req.HeadSHA, req.Identity.HeadSHA)
	}
	return nil
}

// --- Merge attempt and outcome (Task 7.6) ---

// MergeAttempt is the generation-bound merge attempt and outcome record
// committed by the RecordMergeAttempt operation: the stable attempt identity
// binds the provider identity snapshot, the PR identity, and the exact head
// SHA with the provider-verified remote outcome. A remote-unknown outcome is
// terminal — once committed, the Authority refuses further provider-mutating
// attempts and only read reconciliation is permitted. Verified merged truth
// is never erased by a later ambiguous or false-negative read.
type MergeAttempt struct {
	Outcome       string                   `json:"outcome"` // merged | already-merged | failed | open | remote-unknown
	HeadSHA       string                   `json:"head_sha"`
	MergedSHA     string                   `json:"merged_sha,omitempty"`
	Identity      ProviderIdentitySnapshot `json:"identity"`
	ProviderState string                   `json:"provider_state,omitempty"`
	Detail        string                   `json:"detail,omitempty"`
	AttemptedAt   int64                    `json:"attempted_at"`
	Actor         string                   `json:"actor"`
}

// validateMergeAttemptRecord checks one generation-bound merge attempt
// record: a known outcome, a safe immutable head SHA, a complete provider
// identity snapshot whose head agrees with the record head, and a safe
// merged SHA when present.
func validateMergeAttemptRecord(rec MergeAttempt) error {
	if err := validateMergeOutcome(rec.Outcome); err != nil {
		return err
	}
	if err := validateHeadSHA(rec.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(rec.Identity); err != nil {
		return err
	}
	if rec.Identity.HeadSHA != rec.HeadSHA {
		return validationError("merge attempt head %q does not match identity snapshot head %q", rec.HeadSHA, rec.Identity.HeadSHA)
	}
	if rec.MergedSHA != "" && (rec.MergedSHA != strings.TrimSpace(rec.MergedSHA) || strings.ContainsAny(rec.MergedSHA, `/\\`)) {
		return validationError("merge attempt merged SHA %q is unsafe", rec.MergedSHA)
	}
	if strings.TrimSpace(rec.Actor) == "" {
		return validationError("merge attempt missing actor")
	}
	if rec.AttemptedAt <= 0 {
		return validationError("merge attempt missing attempted timestamp")
	}
	return nil
}

// validateMergeOutcome accepts the five merge outcome kinds.
func validateMergeOutcome(outcome string) error {
	switch outcome {
	case MergeOutcomeMerged, MergeOutcomeAlreadyMerged, MergeOutcomeFailed, MergeOutcomeOpen, MergeOutcomeRemoteUnknown:
		return nil
	}
	return validationError("invalid merge outcome %q", outcome)
}

// mergeOutcomeIsMergedEquiv reports whether a committed outcome is the
// provider-verified merged truth (merged or already-merged).
func mergeOutcomeIsMergedEquiv(outcome string) bool {
	return outcome == MergeOutcomeMerged || outcome == MergeOutcomeAlreadyMerged
}

// RecordMergeAttemptRequest is the immutable request payload of one
// generation-bound merge attempt. It carries the exact Task Generation fence,
// the stable Task Operation identity, the actor, the provider-verified remote
// outcome, the provider identity snapshot, the exact head SHA the attempt
// binds, and the provider-reported merged SHA when merged. The Operation ID
// is excluded from the intent digest, so a retry that changes the outcome or
// the evidence under the same ID detects a conflict.
type RecordMergeAttemptRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Outcome            string
	HeadSHA            string
	MergedSHA          string
	Identity           ProviderIdentitySnapshot
	ProviderState      string
	Detail             string
	Reason             string
}

// RecordMergeAttempt is the named semantic operation that commits the
// generation-bound merge attempt and its provider-verified remote outcome in
// one Store transaction: the Expected Generation fence is revalidated inside
// the transaction, the attempt binds the exact head SHA and provider identity
// snapshot, the Revision advances by exactly one when the attempt commits,
// one typed merge-outcome audit event commits, and the durable idempotency
// receipt pins the intent.
//
// Once a remote-unknown outcome is committed, the Authority refuses every
// further provider-mutating attempt with the typed fail-closed
// ErrMergeMutationRefused; only read reconciliation (Get) is permitted.
// Verified merged truth is never erased: a re-attempt of a merged-equivalent
// outcome is an in-value no-op (already-merged idempotency), and a later
// provider false-negative (open), stale closed-not-merged (failed), or
// ambiguous (remote-unknown) read never regresses the committed merged
// outcome. A non-terminal committed outcome (open or failed) is replaced by
// a fresh attempt. Same-op replay is idempotent; a stale or missing task
// fails closed.
func (a *Authority) RecordMergeAttempt(req RecordMergeAttemptRequest) (DeliveryResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return DeliveryResult{}, err
	}
	if err := validateRecordMergeAttemptRequest(req); err != nil {
		return DeliveryResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                   `json:"task_id"`
		ExpectedGeneration uint64                   `json:"expected_generation"`
		Outcome            string                   `json:"outcome"`
		HeadSHA            string                   `json:"head_sha"`
		MergedSHA          string                   `json:"merged_sha,omitempty"`
		Identity           ProviderIdentitySnapshot `json:"identity"`
		ProviderState      string                   `json:"provider_state,omitempty"`
		Detail             string                   `json:"detail,omitempty"`
		Reason             string                   `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Outcome, req.HeadSHA, req.MergedSHA, req.Identity, req.ProviderState, req.Detail, req.Reason})
	if err != nil {
		return DeliveryResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.MergeAttempt != nil {
			committed := cur.MergeAttempt.Outcome
			if committed == MergeOutcomeRemoteUnknown {
				// A remote-unknown outcome forbids repeated provider mutation:
				// the same mutation attempt must never be repeated, and only
				// read reconciliation is permitted (ADR-0007 §7).
				return conflictError(ErrMergeMutationRefused, "task %s generation %s has committed outcome remote-unknown; further provider-mutating merge attempts are refused (read reconciliation only)", req.TaskID, cur.Generation)
			}
			if mergeOutcomeIsMergedEquiv(committed) {
				// Verified remote truth is committed. Already-merged
				// re-attempts are idempotent, and a provider false-negative
				// (open), a stale closed-not-merged read (failed), or an
				// ambiguous read (remote-unknown) never erases it.
				return nil // in-value no-op: the committed merged truth stands
			}
		}
		updated := cur.clone()
		rec := MergeAttempt{
			Outcome:       req.Outcome,
			HeadSHA:       req.HeadSHA,
			MergedSHA:     req.MergedSHA,
			Identity:      req.Identity,
			ProviderState: req.ProviderState,
			Detail:        req.Detail,
			AttemptedAt:   a.now().UnixNano(),
			Actor:         req.Actor.ID,
		}
		updated.MergeAttempt = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditMergeOutcome,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	return a.deliveryResultFromReceipt(req.TaskID, receipt)
}

// validateRecordMergeAttemptRequest validates one merge attempt request: a
// known outcome, a safe head, a safe merged SHA when present, and a complete
// provider identity snapshot whose head agrees with the requested head.
func validateRecordMergeAttemptRequest(req RecordMergeAttemptRequest) error {
	if err := validateMergeOutcome(req.Outcome); err != nil {
		return err
	}
	if err := validateHeadSHA(req.HeadSHA); err != nil {
		return err
	}
	if req.MergedSHA != "" && (req.MergedSHA != strings.TrimSpace(req.MergedSHA) || strings.ContainsAny(req.MergedSHA, `/\\`)) {
		return validationError("merge attempt merged SHA %q is unsafe", req.MergedSHA)
	}
	if err := validateProviderIdentitySnapshot(req.Identity); err != nil {
		return err
	}
	if req.Identity.HeadSHA != req.HeadSHA {
		return validationError("merge attempt head %q does not match identity snapshot head %q", req.HeadSHA, req.Identity.HeadSHA)
	}
	return nil
}
