package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// CheckIssueStateFn is an injectable function that checks whether an issue
// is closed on the provider. Returns true when the issue is closed, false
// when open, and an error when the provider is unavailable.
type CheckIssueStateFn func(link *domain.IssueLink) (bool, error)

// IssueLinkRepairRecord is a single audit record for an issue link repair.
// Stored as a JSON array in the meta field `issue_link_repair_history`.
type IssueLinkRepairRecord struct {
	Timestamp string                              `json:"timestamp"`
	Results   []domain.IssueLinkReconciliationResult `json:"results"`
	Decision  string                              `json:"decision,omitempty"` // "required", "acknowledged", "none"
}

// Meta field keys for issue link repair lifecycle.
const (
	MetaIssueLinkRepairHistory = "issue_link_repair_history"
)

// defaultCheckIssueState checks the provider for the issue's state.
// For GitHub issues, uses gh CLI via the GitHubClient.
// For GitLab, uses glab CLI.
// Returns an error when the provider is unavailable or the state is unknown.
var defaultCheckIssueState = defaultCheckIssueStateImpl

func defaultCheckIssueStateImpl(link *domain.IssueLink) (bool, error) {
	if link.Provider == "" || link.Owner == "" || link.Repo == "" || link.Number <= 0 {
		return false, fmt.Errorf("incomplete issue link: provider=%q owner=%q repo=%q number=%d", link.Provider, link.Owner, link.Repo, link.Number)
	}

	switch link.Provider {
	case "github":
		client, err := DefaultGitHubClient()
		if err != nil {
			return false, fmt.Errorf("GitHub provider not available: %w", err)
		}
		state, err := client.ViewIssueState(link.Owner, link.Repo, link.Number)
		if err != nil {
			return false, fmt.Errorf("checking issue state: %w", err)
		}
		return state == "CLOSED", nil
	case "gitlab":
		return false, fmt.Errorf("GitLab issue state check not implemented")
	default:
		return false, fmt.Errorf("unknown provider %q", link.Provider)
	}
}

// VerifyDeliveryIssueLinks verifies that all auto-close implementation issue
// links have valid closing references. This is a pure verification that does
// not contact the provider.
//
// Returns:
//   - nil error when all auto-close implementation issues have valid closing
//     references and no related/parent links are misconfigured with auto-close.
//   - An error describing the first violation when any link is invalid.
func VerifyDeliveryIssueLinks(links []domain.IssueLink) error {
	for _, link := range links {
		if err := domain.ValidateIssueLink(&link); err != nil {
			return fmt.Errorf("invalid issue link: %w", err)
		}

		switch link.ClosurePolicy {
		case domain.ClosurePolicyAuto:
			// Auto-close implementation issues must have a valid closing reference.
			if link.Relation != domain.IssueLinkImplementation {
				return fmt.Errorf("auto-close policy on non-implementation issue link %s (relation=%q): only implementation issues may be auto-closed",
					link.URL, link.Relation)
			}
			if link.ClosingRef == "" && link.ClosingReference() == "" {
				return fmt.Errorf("auto-close implementation issue link %s has no closing reference: must specify ClosingRef or owner/repo#N",
					link.URL)
			}
		case domain.ClosurePolicyManual:
			// Manual-close policy is valid for any relation.
		case domain.ClosurePolicyNever:
			// Never-close policy is valid for any relation.
		}
	}
	return nil
}

// ReconcileIssueLinks reconciles each issue link against the provider after
// a merge. It uses the provided check function to determine the issue state.
//
// For each link:
//   - auto-close + implementation: checks provider for CLOSED → closed, OPEN → open,
//     provider error → unavailable
//   - auto-close + non-implementation: reports as open (misconfiguration)
//   - manual-close: reports as manual-policy
//   - never-close: reports as manual-policy (never auto-closed)
//
// The checkFn must be safe to call concurrently (it is called sequentially).
func ReconcileIssueLinks(links []domain.IssueLink, checkFn CheckIssueStateFn) []domain.IssueLinkReconciliationResult {
	if checkFn == nil {
		checkFn = defaultCheckIssueState
	}

	results := make([]domain.IssueLinkReconciliationResult, 0, len(links))

	for _, link := range links {
		result := domain.IssueLinkReconciliationResult{
			Link: link,
		}

		switch link.ClosurePolicy {
		case domain.ClosurePolicyAuto:
			if link.Relation != domain.IssueLinkImplementation {
				result.Status = domain.IssueLinkOpen
				result.Detail = fmt.Sprintf("auto-close policy on non-implementation issue link (relation=%q): misconfiguration", link.Relation)
				results = append(results, result)
				continue
			}

			// Check provider for issue state
			closed, err := checkFn(&link)
			if err != nil {
				result.Status = domain.IssueLinkUnavailable
				result.Detail = fmt.Sprintf("provider check failed: %v", err)
			} else if closed {
				result.Status = domain.IssueLinkClosed
				result.Detail = "issue closed by merge"
			} else {
				// Issue is still open — could be pending (CI hasn't triggered close yet)
				// or the closing reference was incorrect.
				// We report as "pending" on first check; a subsequent repair would
				// differentiate between pending and truly open.
				result.Status = domain.IssueLinkPending
				result.Detail = "issue still open after merge: closing reference may be pending or incorrect"
			}

		case domain.ClosurePolicyManual:
			result.Status = domain.IssueLinkManualPolicy
			result.Detail = "closure policy is manual: requires Decision"

		case domain.ClosurePolicyNever:
			result.Status = domain.IssueLinkManualPolicy
			result.Detail = "closure policy is never-close: link will not be closed"

		default:
			result.Status = domain.IssueLinkUnavailable
			result.Detail = fmt.Sprintf("unknown closure policy %q", link.ClosurePolicy)
		}

		results = append(results, result)
	}

	return results
}

// ReconcileAndStoreIssueLinks reconciles all issue links against the provider
// and persists the results in the task meta. Returns the reconciliation results.
// This is the primary integration point for post-merge reconciliation.
func ReconcileAndStoreIssueLinks(homeDir, taskID string, links []domain.IssueLink, checkFn CheckIssueStateFn) ([]domain.IssueLinkReconciliationResult, error) {
	if checkFn == nil {
		checkFn = defaultCheckIssueState
	}

	results := ReconcileIssueLinks(links, checkFn)

	// Store results in meta
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// If meta doesn't exist, create it
		meta = make(map[string]string)
	}

	// Store reconciliation results as JSON in meta
	resultsJSON, _ := json.Marshal(results)
	meta["issue_link_reconciliation"] = string(resultsJSON)

	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return nil, fmt.Errorf("storing issue link reconciliation: %w", err)
	}

	return results, nil
}

// RepairIssueLinks repairs issue links by re-running reconciliation and
// auditing the results. It is idempotent and can be safely re-run.
//
// The repair process:
//  1. Read issue links from the task meta
//  2. Reconcile each link against the provider
//  3. Store the reconciliation results in meta
//  4. Requires Decision when any manual-policy link is encountered
//  5. Records the repair in the audit trail
//
// Returns the reconciliation results. When a Decision is required, the
// results include a manual-policy entry. The caller must check for
// manual-policy results and prompt for a Decision before considering
// the repair complete.
func RepairIssueLinks(homeDir, taskID string, checkFn CheckIssueStateFn) ([]domain.IssueLinkReconciliationResult, error) {
	if checkFn == nil {
		checkFn = defaultCheckIssueState
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("repair issue links: reading meta: %w", err)
	}

	links := domain.IssueLinksFromMeta(meta)
	if len(links) == 0 {
		return nil, nil // no issue links to repair
	}

	// Reconcile all links
	results := ReconcileIssueLinks(links, checkFn)

	// Build audit record
	record := IssueLinkRepairRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Results:   results,
		Decision:  "none",
	}

	// Check if any manual-policy result requires Decision
	needsDecision := false
	for _, r := range results {
		if r.Status == domain.IssueLinkManualPolicy {
			needsDecision = true
			break
		}
	}

	if needsDecision {
		record.Decision = "required"
	}

	// Store reconciliation results in meta
	resultsJSON, _ := json.Marshal(results)
	meta["issue_link_reconciliation"] = string(resultsJSON)

	// Append to repair history
	meta[MetaIssueLinkRepairHistory] = appendRepairHistory(meta[MetaIssueLinkRepairHistory], &record)

	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return nil, fmt.Errorf("repair issue links: writing meta: %w", err)
	}

	return results, nil
}

// appendRepairHistory appends a repair record to the existing history JSON.
func appendRepairHistory(existing string, record *IssueLinkRepairRecord) string {
	var history []*IssueLinkRepairRecord
	if existing != "" {
		json.Unmarshal([]byte(existing), &history)
	}
	history = append(history, record)
	data, _ := json.Marshal(history)
	return string(data)
}

// PrepareDeliveryIssueLinks verifies issue links during PrepareDelivery.
// It checks that all auto-close implementation issues have valid closing
// references, and that related/parent links are not misconfigured with
// auto-close policy.
//
// This is called after the identity and head checks pass, before the
// CAS transition to delivered.
func PrepareDeliveryIssueLinks(links []domain.IssueLink) error {
	if len(links) == 0 {
		return nil // no issue links to verify
	}

	return VerifyDeliveryIssueLinks(links)
}

// RenderIssueLinkReconciliationResults renders the reconciliation results
// as a human-readable string with an AXI machine-readable block.
func RenderIssueLinkReconciliationResults(results []domain.IssueLinkReconciliationResult) string {
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Issue Link Reconciliation:\n")
	b.WriteString("------------------------\n")

	for _, r := range results {
		ref := r.Link.ClosingReference()
		if ref == "" {
			ref = r.Link.URL
		}
		fmt.Fprintf(&b, "  %s (%s): %s", ref, r.Link.Relation, r.Status)
		if r.Detail != "" {
			fmt.Fprintf(&b, " — %s", r.Detail)
		}
		b.WriteString("\n")
	}

	// AXI block
	b.WriteString("\nissue-link-reconciliation:\n")
	for _, r := range results {
		ref := r.Link.ClosingReference()
		if ref == "" {
			ref = r.Link.URL
		}
		fmt.Fprintf(&b, "  - ref: %s\n", ref)
		fmt.Fprintf(&b, "    relation: %s\n", r.Link.Relation)
		fmt.Fprintf(&b, "    policy: %s\n", r.Link.ClosurePolicy)
		fmt.Fprintf(&b, "    status: %s\n", r.Status)
		if r.Detail != "" {
			fmt.Fprintf(&b, "    detail: %s\n", r.Detail)
		}
	}

	return b.String()
}