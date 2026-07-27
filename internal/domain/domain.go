// Package domain defines pure munsu business rules and value types.
package domain

import "fmt"

type PRStatus string

const (
	PROpen   PRStatus = "open"
	PRClosed PRStatus = "closed"
	PRMerged PRStatus = "merged"
)

type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
	CheckPending CheckStatus = "pending"
	CheckSkipped CheckStatus = "skipped"
)

type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes-requested"
	ReviewPending          ReviewState = "pending"
	ReviewDismissed        ReviewState = "dismissed"
)

type CheckRun struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
}

type Review struct {
	State ReviewState `json:"state"`
	Body  string      `json:"body"`
}

type PR struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Status     PRStatus   `json:"status"`
	BaseBranch string     `json:"baseBranch"`
	HeadBranch string     `json:"headBranch"`
	Checks     []CheckRun `json:"checks,omitempty"`
	Reviews    []Review   `json:"reviews,omitempty"`
}

func (pr PR) CanMerge() bool {
	if pr.Status != PROpen {
		return false
	}
	for _, check := range pr.Checks {
		if check.Status == CheckFailed {
			return false
		}
	}
	hasApproval := false
	for _, review := range pr.Reviews {
		switch review.State {
		case ReviewChangesRequested:
			return false
		case ReviewApproved:
			hasApproval = true
		}
	}
	return hasApproval
}

func (r Review) IsApproving() bool {
	return r.State == ReviewApproved
}

type DeliveryIdentity struct {
	Provider   string `json:"provider"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	BaseRef    string `json:"baseRef"`
	HeadRef    string `json:"headRef"`
	HeadSHA    string `json:"headSHA"`
	CapturedAt string `json:"capturedAt"`
}

func ValidateIdentity(id *DeliveryIdentity) error {
	switch {
	case id == nil:
		return fmt.Errorf("delivery identity is nil")
	case id.Provider == "":
		return fmt.Errorf("delivery identity: provider is required")
	case id.Owner == "":
		return fmt.Errorf("delivery identity: owner is required")
	case id.Repo == "":
		return fmt.Errorf("delivery identity: repo is required")
	case id.Number <= 0:
		return fmt.Errorf("delivery identity: PR number must be positive, got %d", id.Number)
	case id.URL == "":
		return fmt.Errorf("delivery identity: URL is required")
	case id.BaseRef == "":
		return fmt.Errorf("delivery identity: baseRef is required")
	case id.HeadRef == "":
		return fmt.Errorf("delivery identity: headRef is required")
	case id.HeadSHA == "":
		return fmt.Errorf("delivery identity: headSHA is required")
	case id.CapturedAt == "":
		return fmt.Errorf("delivery identity: capturedAt is required")
	}
	return nil
}
