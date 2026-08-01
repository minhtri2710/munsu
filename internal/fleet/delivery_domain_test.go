//go:build integration

package fleet

import (
	"testing"
)

func TestPR_CanMerge_OpenWithPassedChecksAndApproval(t *testing.T) {
	pr := PR{
		Number: 42,
		Title:  "Add feature",
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
			{Name: "Lint", Status: CheckPassed},
		},
		Reviews: []Review{
			{State: ReviewApproved, Body: "LGTM"},
		},
	}
	if !pr.CanMerge() {
		t.Error("expected CanMerge() = true for open PR with passing checks and approval")
	}
}

func TestPR_CanMerge_Closed(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PRClosed,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
		},
		Reviews: []Review{
			{State: ReviewApproved},
		},
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false for closed PR")
	}
}

func TestPR_CanMerge_Merged(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PRMerged,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
		},
		Reviews: []Review{
			{State: ReviewApproved},
		},
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false for merged PR")
	}
}

func TestPR_CanMerge_FailedChecks(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckFailed},
		},
		Reviews: []Review{
			{State: ReviewApproved},
		},
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false when checks failed")
	}
}

func TestPR_CanMerge_PendingChecks(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPending},
		},
		Reviews: []Review{
			{State: ReviewApproved},
		},
	}
	if !pr.CanMerge() {
		t.Error("expected CanMerge() = true with pending checks (not failed)")
	}
}

func TestPR_CanMerge_ChangesRequested(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
		},
		Reviews: []Review{
			{State: ReviewChangesRequested, Body: "Fix this"},
		},
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false when changes are requested")
	}
}

func TestPR_CanMerge_NoReviews(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
		},
		Reviews: nil,
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false with no reviews")
	}
}

func TestPR_CanMerge_ChangesRequestedThenApproved(t *testing.T) {
	// A later approving review does not override an earlier changes-requested.
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "CI", Status: CheckPassed},
		},
		Reviews: []Review{
			{State: ReviewChangesRequested, Body: "Fix this"},
			{State: ReviewApproved, Body: "LGTM"},
		},
	}
	if pr.CanMerge() {
		t.Error("expected CanMerge() = false when changes-requested exists alongside approval")
	}
}

func TestPR_CanMerge_SkippedChecks(t *testing.T) {
	pr := PR{
		Number: 42,
		Status: PROpen,
		Checks: []CheckRun{
			{Name: "Lint", Status: CheckSkipped},
		},
		Reviews: []Review{
			{State: ReviewApproved},
		},
	}
	if !pr.CanMerge() {
		t.Error("expected CanMerge() = true with skipped checks")
	}
}

func TestReview_IsApproving_Approved(t *testing.T) {
	r := Review{State: ReviewApproved}
	if !r.IsApproving() {
		t.Error("expected IsApproving() = true for approved review")
	}
}

func TestReview_IsApproving_ChangesRequested(t *testing.T) {
	r := Review{State: ReviewChangesRequested}
	if r.IsApproving() {
		t.Error("expected IsApproving() = false for changes-requested")
	}
}

func TestReview_IsApproving_Pending(t *testing.T) {
	r := Review{State: ReviewPending}
	if r.IsApproving() {
		t.Error("expected IsApproving() = false for pending")
	}
}

func TestReview_IsApproving_Dismissed(t *testing.T) {
	r := Review{State: ReviewDismissed}
	if r.IsApproving() {
		t.Error("expected IsApproving() = false for dismissed")
	}
}
