package taskauthority

import (
	"github.com/minhtri2710/munsu/internal/domain"
)

// ReconcileIssueLinksRequest is the immutable request payload of one
// generation-bound issue link reconciliation commit. It carries the exact
// Task Generation fence, the stable Task Operation identity, the issue link
// definition records, and the provider reconciliation evidence (the
// reconciliation outcome). The Operation ID is excluded from the intent
// digest by the operation helper, so a retry that changes the links or the
// provider evidence under the same ID detects a conflict.
type ReconcileIssueLinksRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Links              []domain.IssueLink
	Results            []domain.IssueLinkReconciliationResult
	Reason             string
}

// ReconcileIssueLinksResult is the caller-visible outcome of one issue link
// reconciliation commit. Replayed is true when the Operation ID was already
// committed with the same intent and the original receipt was returned
// without re-running the transaction; Results are the committed provider
// evidence (identical on replay by the digest guarantee).
type ReconcileIssueLinksResult struct {
	TaskID     string
	Generation Generation
	Revision   Revision
	Phase      Phase
	Replayed   bool
	Results    []domain.IssueLinkReconciliationResult
}

// ReconcileIssueLinks is the named semantic operation that commits the
// generation-bound issue link definition record and the provider
// reconciliation evidence in one Store transaction: the Expected Generation
// fence is revalidated inside the transaction, the Revision advances by
// exactly one, one typed issue-link audit event commits under the operation,
// and the durable idempotency receipt pins the intent. Repeating the same
// Operation ID with the same intent replays the original receipt and
// preserves the original provider evidence; reusing the Operation ID with
// changed links or evidence is a typed non-retryable conflict. Parent and
// related links can never be promoted to automatic closure policy, and the
// evidence must correspond one-to-one with the definition records.
func (a *Authority) ReconcileIssueLinks(req ReconcileIssueLinksRequest) (ReconcileIssueLinksResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return ReconcileIssueLinksResult{}, err
	}
	if err := validateIssueLinkRequest(req); err != nil {
		return ReconcileIssueLinksResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                                 `json:"task_id"`
		ExpectedGeneration uint64                                 `json:"expected_generation"`
		Links              []domain.IssueLink                     `json:"links"`
		Results            []domain.IssueLinkReconciliationResult `json:"results"`
		Reason             string                                 `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Links, req.Results, req.Reason})
	if err != nil {
		return ReconcileIssueLinksResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		updated := cur.clone()
		updated.IssueLinks = req.Links
		updated.IssueLinkReconciliation = req.Results
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditIssueLinks,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return ReconcileIssueLinksResult{}, err
	}
	return ReconcileIssueLinksResult{
		TaskID:     receipt.TaskID,
		Generation: receipt.Generation,
		Revision:   receipt.Revision,
		Phase:      receipt.Phase,
		Replayed:   receipt.Replayed,
		Results:    req.Results,
	}, nil
}

// validateIssueLinkRequest validates the issue link definition records and
// the one-to-one provider evidence of one reconciliation request.
func validateIssueLinkRequest(req ReconcileIssueLinksRequest) error {
	if len(req.Links) == 0 {
		return nil
	}
	if len(req.Results) != len(req.Links) {
		return validationError("issue link reconciliation has %d results for %d links", len(req.Results), len(req.Links))
	}
	for i := range req.Links {
		if err := validateIssueLink(req.Links[i]); err != nil {
			return err
		}
		if err := validateReconciliationResult(req.Results[i], req.Links[i]); err != nil {
			return err
		}
	}
	return nil
}
