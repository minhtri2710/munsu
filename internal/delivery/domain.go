package delivery

// PRStatus represents the current state of a pull request.
type PRStatus string

const (
	PROpen   PRStatus = "open"
	PRClosed PRStatus = "closed"
	PRMerged PRStatus = "merged"
)

// CheckStatus represents the result of a check run on a PR.
type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
	CheckPending CheckStatus = "pending"
	CheckSkipped CheckStatus = "skipped"
)

// ReviewState represents the state of a PR review.
type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes-requested"
	ReviewPending          ReviewState = "pending"
	ReviewDismissed        ReviewState = "dismissed"
)

// CheckRun represents a single check run on a pull request.
type CheckRun struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
}

// Review represents a pull request review.
type Review struct {
	State ReviewState `json:"state"`
	Body  string      `json:"body"`
}

// PR represents a pull request with its associated checks and reviews.
type PR struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Status     PRStatus   `json:"status"`
	BaseBranch string     `json:"baseBranch"`
	HeadBranch string     `json:"headBranch"`
	Checks     []CheckRun `json:"checks,omitempty"`
	Reviews    []Review   `json:"reviews,omitempty"`
}

// CanMerge returns true when the PR is open, has no failed checks,
// and has at least one approving review without any changes-requested.
func (pr PR) CanMerge() bool {
	if pr.Status != PROpen {
		return false
	}
	for _, c := range pr.Checks {
		if c.Status == CheckFailed {
			return false
		}
	}
	hasApproval := false
	for _, r := range pr.Reviews {
		switch r.State {
		case ReviewChangesRequested:
			return false
		case ReviewApproved:
			hasApproval = true
		}
	}
	return hasApproval
}

// IsApproving returns true when the review state is approved.
func (r Review) IsApproving() bool {
	return r.State == ReviewApproved
}
