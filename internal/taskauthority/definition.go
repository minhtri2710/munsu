package taskauthority

import (
	"github.com/minhtri2710/munsu/internal/domain"
)

// Issue link definition records live inside the Aggregate, bound to the Task
// Generation: the issue links themselves (the definition record) and the
// provider reconciliation evidence committed with them. Only implementation
// issues may carry automatic closure policy; parent and related links are
// never promoted to automatic closure (Task 7.2).

// validateIssueLinkDefinition validates the generation-bound issue link
// definition records of one Aggregate: every link must be a well-formed issue
// link with a valid closure policy for its relation, and the provider
// reconciliation evidence must correspond one-to-one with the links.
func validateIssueLinkDefinition(agg Aggregate) error {
	if len(agg.IssueLinks) == 0 {
		return nil
	}
	if len(agg.IssueLinkReconciliation) != len(agg.IssueLinks) {
		return validationError("issue link reconciliation has %d results for %d links", len(agg.IssueLinkReconciliation), len(agg.IssueLinks))
	}
	for i := range agg.IssueLinks {
		if err := validateIssueLink(agg.IssueLinks[i]); err != nil {
			return err
		}
		if err := validateReconciliationResult(agg.IssueLinkReconciliation[i], agg.IssueLinks[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateIssueLink checks one generation-bound issue link: shape validity
// plus the automatic-closure promotion rule. Only implementation links close
// automatically on merge; parent and related links never do.
func validateIssueLink(link domain.IssueLink) error {
	if err := domain.ValidateIssueLink(&link); err != nil {
		return validationError("invalid issue link: %v", err)
	}
	if link.ClosurePolicy == domain.ClosurePolicyAuto && link.Relation != domain.IssueLinkImplementation {
		return validationError("auto-close policy on %s issue link %s: only implementation issues may auto-close", link.Relation, link.URL)
	}
	return nil
}

// validateReconciliationResult checks one provider evidence entry against the
// link it describes: the status must be a known reconciliation outcome and the
// result must reference the exact link definition at the same index.
func validateReconciliationResult(result domain.IssueLinkReconciliationResult, link domain.IssueLink) error {
	switch result.Status {
	case domain.IssueLinkClosed, domain.IssueLinkPending, domain.IssueLinkOpen, domain.IssueLinkUnavailable, domain.IssueLinkManualPolicy:
	default:
		return validationError("issue link reconciliation result has unknown status %q", result.Status)
	}
	if result.Link != link {
		return validationError("issue link reconciliation result does not match link %s", link.URL)
	}
	return nil
}
