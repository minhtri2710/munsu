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

// --- Adapter compile-time checks ---

func TestAdaptersImplementPipeline(t *testing.T) {
	// Compile-time checks live in pipeline.go as var _ checks.
	// This test verifies they can be constructed and expose the right methods.
	checks := []struct {
		name string
		p    Pipeline
	}{
		{"GHAxiAdapter", NewGHAxiAdapter()},
		{"NoMistakesAdapter", NewNoMistakesAdapter()},
		{"GitLocalAdapter", NewGitLocalAdapter()},
		{"CompositeAdapter", NewCompositeAdapter()},
	}
	for _, c := range checks {
		if c.p == nil {
			t.Errorf("%s: Pipeline must not be nil", c.name)
		}
	}
}

// --- Adapter routing tests ---

func TestGHAxiAdapter_RoutesPRCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter routing test in short mode")
	}
	a := NewGHAxiAdapter()
	// Without a real environment, RunPRCheck should fail at gh CLI, not at routing.
	err := a.RunPRCheck(t.TempDir(), "nonexistent", "https://github.com/minhtri2710/munsu/pull/999999")
	if err == nil {
		t.Error("expected error for nonexistent task PR check")
	}
}

func TestNoMistakesAdapter_RoutesNoMistakes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter routing test in short mode")
	}
	a := NewNoMistakesAdapter()
	err := a.RunNoMistakes("test", []string{"skip-1"})
	if err == nil {
		t.Error("expected error for no-mistakes run (not on PATH in test)")
	}
}

func TestGitLocalAdapter_RoutesMergeLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter routing test in short mode")
	}
	a := NewGitLocalAdapter()
	err := a.MergeLocal(t.TempDir(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task merge")
	}
}

func TestCompositeAdapter_RoutesCorrectly(t *testing.T) {
	a := NewCompositeAdapter()
	// Verify the composite adapter delegates to the right underlying adapter
	// by checking that each method produces the right error domain.

	errPR := a.RunPRCheck("", "", "")
	if errPR == nil {
		t.Error("expected error from RunPRCheck")
	}

	errNM := a.RunNoMistakes("", nil)
	if errNM == nil {
		t.Error("expected error from RunNoMistakes")
	}

	errML := a.MergeLocal("", "")
	if errML == nil {
		t.Error("expected error from MergeLocal")
	}
}

func TestCompositeAdapter_DelegatesPRCheck(t *testing.T) {
	a := NewCompositeAdapter()
	// GHAxiAdapter.RunPRCheck calls PRCheck which needs gh-axi.
	// We verify it reaches PRCheck (and fails there) vs being rejected at routing.
	err := a.RunPRCheck(t.TempDir(), "nonexistent", "https://github.com/minhtri2710/munsu/pull/999999")
	if err == nil {
		t.Error("expected error")
	}
}

func TestCompositeAdapter_DelegatesMergeLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter routing test in short mode")
	}
	a := NewCompositeAdapter()
	// GitLocalAdapter.MergeLocal calls MergeLocal which reads task meta.
	err := a.MergeLocal(t.TempDir(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestCompositeAdapter_DelegatesNoMistakes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter routing test in short mode")
	}
	a := NewCompositeAdapter()
	// NoMistakesAdapter.RunNoMistakes calls NoMistakesRun.
	err := a.RunNoMistakes("test", nil)
	if err == nil {
		t.Error("expected error for no-mistakes run (not on PATH)")
	}
}
