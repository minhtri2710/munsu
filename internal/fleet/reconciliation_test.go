package fleet

import "testing"

func TestReconcileParentStatus_StructuredHomeNoParent(t *testing.T) {
	sum := HomeSummary{Valid: true, State: "no_active_work"}
	r := ReconcileParentStatus(sum, "")
	if r.Provenance != "structured-home" || r.Freshness != "fresh" || r.ParentEventRole != "historical-only" {
		t.Fatalf("got %+v", r)
	}
	if r.Contradiction {
		t.Fatal("expected no contradiction")
	}
}

func TestReconcileParentStatus_StaleWorkingContradictsIdleHome(t *testing.T) {
	// Domain Alpha shape: home idle / held, parent still says working.
	sum := HomeSummary{
		Valid:  true,
		State:  "externally_held",
		Counts: HomeCounts{Blocked: 1, Queued: 1},
	}
	r := ReconcileParentStatus(sum, "working [key=phase7]: Sample rollout Phase 7")
	if r.Provenance != "structured-home" {
		t.Fatalf("provenance=%q want structured-home", r.Provenance)
	}
	if !r.Contradiction {
		t.Fatalf("expected contradiction, got %+v", r)
	}
	if r.ContradictionReason == "" {
		t.Fatal("expected contradiction reason")
	}
}

func TestReconcileParentStatus_WorkingCorroborated(t *testing.T) {
	sum := HomeSummary{
		Valid:          true,
		State:          "active_child_work",
		Counts:         HomeCounts{ActiveChildren: 1, InFlight: 1},
		ActiveChildren: []ChildBrief{{ID: "t1", Status: "working: x"}},
	}
	r := ReconcileParentStatus(sum, "working [key=t1]: implementing")
	if r.Contradiction {
		t.Fatalf("should corroborate: %+v", r)
	}
	if r.Provenance != "structured-home" {
		t.Fatalf("provenance=%q", r.Provenance)
	}
}

func TestReconcileParentStatus_NeedsDecisionContradicts(t *testing.T) {
	sum := HomeSummary{Valid: true, State: "no_active_work"}
	r := ReconcileParentStatus(sum, "needs-decision [key=api]: pick shape")
	if !r.Contradiction {
		t.Fatalf("expected contradiction: %+v", r)
	}
}

func TestReconcileParentStatus_NeedsDecisionCorroborated(t *testing.T) {
	sum := HomeSummary{Valid: true, State: "captain_decision"}
	r := ReconcileParentStatus(sum, "needs-decision [key=api]: pick shape")
	if r.Contradiction {
		t.Fatalf("should corroborate: %+v", r)
	}
}

func TestReconcileParentStatus_DoneIsHistoricalOnly(t *testing.T) {
	// Terminal parent events alone do not force contradiction.
	sum := HomeSummary{Valid: true, State: "no_active_work"}
	r := ReconcileParentStatus(sum, "done [key=phase7]: PR https://example/1")
	if r.Contradiction {
		t.Fatalf("done should not contradict idle home by itself: %+v", r)
	}
	if r.Provenance != "structured-home" || r.ParentEventRole != "historical-only" {
		t.Fatalf("got %+v", r)
	}
}

func TestReconcileParentStatus_InvalidHomeFallsBackToParent(t *testing.T) {
	sum := HomeSummary{Valid: false, Reason: "no recorded captain home", State: "unknown"}
	r := ReconcileParentStatus(sum, "working: something")
	if r.Provenance != "parent-status-only" || r.Freshness != "historical-event" {
		t.Fatalf("got %+v", r)
	}
	if r.ParentEventRole != "fallback-only-not-current" {
		t.Fatalf("role=%q", r.ParentEventRole)
	}
	if r.Contradiction {
		t.Fatal("fallback must not invent contradiction")
	}
}

func TestReconcileParentStatus_Unavailable(t *testing.T) {
	sum := HomeSummary{Valid: false, Reason: "no recorded captain home", State: "unknown"}
	r := ReconcileParentStatus(sum, "")
	if r.Provenance != "unavailable" || r.Freshness != "unknown" {
		t.Fatalf("got %+v", r)
	}
}

func TestStatusVerb(t *testing.T) {
	if got := statusVerb("working [key=x]: note"); got != "working" {
		t.Fatalf("got %q", got)
	}
	if got := statusVerb("needs-decision: choose"); got != "needs-decision" {
		t.Fatalf("got %q", got)
	}
}
