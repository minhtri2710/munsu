package fleet

import (
	"strings"
)

// ParentReconciliation is the result of comparing parent captain status
// (append-only event log last line) against structured Captain-home summary.
// Structured home is authoritative; parent status is historical/untrusted when
// it conflicts (munsu captain_current provenance model).
type ParentReconciliation struct {
	// Provenance selected source of current state.
	// structured-home | parent-status-only | unavailable
	Provenance string
	// Freshness of the selected current state.
	// fresh | historical-event | unknown
	Freshness string
	// ParentEventRole describes how parent status may be used.
	// historical-only | fallback-only-not-current | none
	ParentEventRole string
	// Contradiction is true when parent last_status claims open work that
	// structured home does not corroborate.
	Contradiction bool
	// ContradictionReason is a short operator-facing explanation when Contradiction.
	ContradictionReason string
}

// ReconcileParentStatus compares parent last status with a HomeSummary.
// Home summary is authoritative when Valid; parent line never becomes current work.
func ReconcileParentStatus(sum HomeSummary, lastParentStatus string) ParentReconciliation {
	last := strings.TrimSpace(lastParentStatus)
	if sum.Valid {
		r := ParentReconciliation{
			Provenance:      "structured-home",
			Freshness:       "fresh",
			ParentEventRole: "historical-only",
		}
		if last == "" {
			return r
		}
		r.Contradiction, r.ContradictionReason = parentContradictsHome(sum, last)
		return r
	}
	if last != "" {
		return ParentReconciliation{
			Provenance:      "parent-status-only",
			Freshness:       "historical-event",
			ParentEventRole: "fallback-only-not-current",
		}
	}
	// Readable but invalid home with a reason still prefers structured-home labeling
	// for operator visibility; callers may override when home path is empty.
	if sum.Home != "" && sum.Reason != "" {
		return ParentReconciliation{
			Provenance:      "structured-home",
			Freshness:       "fresh",
			ParentEventRole: "historical-only",
		}
	}
	return ParentReconciliation{
		Provenance:      "unavailable",
		Freshness:       "unknown",
		ParentEventRole: "none",
	}
}

func parentContradictsHome(sum HomeSummary, lastParentStatus string) (bool, string) {
	verb := statusVerb(lastParentStatus)
	switch verb {
	case "working", "parked":
		if sum.Counts.ActiveChildren > 0 || sum.Counts.InFlight > 0 || sum.State == "active_child_work" {
			return false, ""
		}
		return true, "parent claims " + verb + " but structured home has no active child work"
	case "needs-decision":
		if sum.State == "captain_decision" || sum.Counts.DecisionsOpen > 0 {
			return false, ""
		}
		return true, "parent claims needs-decision but structured home has no open decisions"
	case "blocked":
		if sum.State == "externally_held" || sum.State == "captain_decision" ||
			sum.Counts.Holds > 0 || sum.Counts.Blocked > 0 || sum.Counts.DecisionsOpen > 0 {
			return false, ""
		}
		return true, "parent claims blocked but structured home has no hold/block evidence"
	case "paused":
		if sum.State == "externally_held" || sum.State == "active_child_work" ||
			sum.Counts.Holds > 0 || sum.Counts.Blocked > 0 ||
			sum.Counts.ActiveChildren > 0 || sum.Counts.InFlight > 0 {
			return false, ""
		}
		return true, "parent claims paused but structured home has no matching hold/work"
	default:
		// Terminal or unknown parent verbs (done, failed, resolved, …) are historical
		// events only; they do not force contradiction by themselves.
		return false, ""
	}
}

// statusVerb extracts the leading verb from a status line (optional [key=…] stripped).
func statusVerb(line string) string {
	before, _, _ := strings.Cut(strings.TrimSpace(line), ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	return strings.TrimSpace(before)
}
