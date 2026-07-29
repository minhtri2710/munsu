package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

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

func RequireIdentity(homeDir, id string) (*domain.DeliveryIdentity, error) {
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("reading task meta for identity: %w", err)
	}

	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("parsing delivery identity: %w", err)
	}
	if ident == nil {
		return nil, fmt.Errorf("no delivery identity found for task %s: PR URL not set in meta; use pr-check to capture identity before destructive actions", id)
	}

	if err := domain.ValidateIdentity(ident); err != nil {
		return nil, fmt.Errorf("incomplete delivery identity for task %s: %w; re-run pr-check to recapture", id, err)
	}

	return ident, nil
}

// CaptureIdentity extracts a domain.DeliveryIdentity from a PR/MR URL.
// For GitHub URLs it uses gh-axi (via GitHubClient) with degraded gh CLI fallback.
// For GitLab URLs it uses glab (via GitLabClient) with no degraded fallback.
func CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	provider, _, _, _, _, err := ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PR/MR URL: %w", err)
	}

	switch provider {
	case "github":
		return captureGitHubIdentity(prURL)
	case "gitlab":
		return captureGitLabIdentity(prURL)
	default:
		return nil, fmt.Errorf("unknown provider %q for URL %s", provider, prURL)
	}
}

// captureGitHubIdentity captures a domain.DeliveryIdentity from a GitHub PR URL.
// Uses gh-axi via the typed GitHubClient when Ready; falls back to raw gh CLI.
func captureGitHubIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	client, err := DefaultGitHubClient()
	if err == nil {
		return client.CaptureIdentity(prURL)
	}

	// Degraded path: try gh CLI directly (read-only, permitted when gh-axi is Absent)
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PR URL: %w", err)
	}

	cmd := exec.Command("gh", "pr", "view",
		fmt.Sprintf("%d", ghURL.Num),
		"--repo", fmt.Sprintf("%s/%s", ghURL.Owner, ghURL.Repo),
		"--json", "headRefOid,headRefName,baseRefName",
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view: %w", err)
	}

	var result struct {
		HeadRefOid  string `json:"headRefOid"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	if result.HeadRefOid == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefOid")
	}

	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      ghURL.Owner,
		Repo:       ghURL.Repo,
		Number:     ghURL.Num,
		URL:        prURL,
		BaseRef:    result.BaseRefName,
		HeadRef:    result.HeadRefName,
		HeadSHA:    result.HeadRefOid,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// captureGitLabIdentity captures a domain.DeliveryIdentity from a GitLab MR URL.
// Uses glab via the typed GitLabClient. No degraded fallback — if glab is
// Absent or Failed, callers must fail closed so silent raw glab is never used.
func captureGitLabIdentity(mrURL string) (*domain.DeliveryIdentity, error) {
	client, err := DefaultGitLabClient()
	if err != nil {
		return nil, fmt.Errorf("GitLab provider not available: %w", err)
	}
	return client.CaptureIdentity(mrURL)
}
