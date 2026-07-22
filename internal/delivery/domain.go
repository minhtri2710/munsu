package delivery

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/ghurl"
	"github.com/minhtri2710/munsu/internal/task"
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

// DeliveryIdentity captures the durable identity of a pull request at the
// time it was discovered, before branch topology can change.
// The record survives local checkout switch and remote head deletion.
type DeliveryIdentity struct {
	Provider   string `json:"provider"`   // e.g. "github"
	Owner      string `json:"owner"`      // repository owner
	Repo       string `json:"repo"`       // repository name
	Number     int    `json:"number"`     // PR number
	URL        string `json:"url"`        // full PR URL
	BaseRef    string `json:"baseRef"`    // target branch ref (e.g. "main")
	HeadRef    string `json:"headRef"`    // source branch ref (e.g. "feature/foo")
	HeadSHA    string `json:"headSHA"`    // exact head commit SHA at capture time
	CapturedAt string `json:"capturedAt"` // ISO 8601 capture timestamp
}

// ValidateIdentity checks that a DeliveryIdentity has all required fields
// populated and that the repository components are consistent.
// Returns a descriptive error if the identity is insufficient for
// destructive delivery actions.
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

// MetaKeys returns the task meta keys used to persist this identity.
func (id *DeliveryIdentity) MetaKeys() []string {
	return []string{
		"pr_provider", "pr_owner", "pr_repo",
		"pr_number", "pr_url",
		"pr_base", "pr_base_ref", "pr_head_ref", "pr_head", "pr_head_sha",
		"pr_timestamp",
	}
}

// ToMeta serializes the identity into a task meta map.
// Writes both canonical (pr_head_sha, pr_base_ref) and legacy
// (pr_head, pr_base) names for backward compatibility.
func (id *DeliveryIdentity) ToMeta() map[string]string {
	return map[string]string{
		"pr_provider":  id.Provider,
		"pr_owner":     id.Owner,
		"pr_repo":      id.Repo,
		"pr_number":    fmt.Sprintf("%d", id.Number),
		"pr_url":       id.URL,
		"pr_base":      id.BaseRef,
		"pr_base_ref":  id.BaseRef,
		"pr_head_ref":  id.HeadRef,
		"pr_head":      id.HeadSHA,
		"pr_head_sha":  id.HeadSHA,
		"pr_timestamp": id.CapturedAt,
	}
}

// IdentityFromMeta reconstructs a DeliveryIdentity from task meta.
// Returns nil (no error) when no identity metadata exists, so callers
// can distinguish "no identity" from "corrupt identity".
func IdentityFromMeta(meta map[string]string) (*DeliveryIdentity, error) {
	prURL := meta["pr_url"]
	if prURL == "" {
		prURL = meta["pr"]
	}
	if prURL == "" {
		for _, key := range (&DeliveryIdentity{}).MetaKeys() {
			if meta[key] != "" {
				return nil, fmt.Errorf("delivery identity has %s but no pr_url", key)
			}
		}
		return nil, nil
	}

	numStr := meta["pr_number"]
	num := 0
	if numStr != "" {
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid pr_number %q", numStr)
		}
		num = n
	}

	parsed, err := ghurl.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid pr_url %q: %w", prURL, err)
	}
	owner := meta["pr_owner"]
	repo := meta["pr_repo"]
	if owner != "" && owner != parsed.Owner {
		return nil, fmt.Errorf("pr_owner %q does not match pr_url owner %q", owner, parsed.Owner)
	}
	if repo != "" && repo != parsed.Repo {
		return nil, fmt.Errorf("pr_repo %q does not match pr_url repo %q", repo, parsed.Repo)
	}
	if num > 0 && num != parsed.Num {
		return nil, fmt.Errorf("pr_number %d does not match pr_url number %d", num, parsed.Num)
	}
	if owner == "" {
		owner = parsed.Owner
	}
	if repo == "" {
		repo = parsed.Repo
	}
	if num <= 0 {
		num = parsed.Num
	}

	// Resolve headSHA with fallback: pr_head_sha -> pr_head
	headSHA := meta["pr_head_sha"]
	if headSHA == "" {
		headSHA = meta["pr_head"]
	} else if other := meta["pr_head"]; other != "" && other != headSHA {
		return nil, fmt.Errorf("pr_head_sha %q conflicts with pr_head %q", headSHA, other)
	}

	// Resolve baseRef with fallback: pr_base_ref -> pr_base
	baseRef := meta["pr_base_ref"]
	if baseRef == "" {
		baseRef = meta["pr_base"]
	} else if other := meta["pr_base"]; other != "" && other != baseRef {
		return nil, fmt.Errorf("pr_base_ref %q conflicts with pr_base %q", baseRef, other)
	}

	id := &DeliveryIdentity{
		Provider:   meta["pr_provider"],
		Owner:      owner,
		Repo:       repo,
		Number:     num,
		URL:        prURL,
		BaseRef:    baseRef,
		HeadRef:    meta["pr_head_ref"],
		HeadSHA:    headSHA,
		CapturedAt: meta["pr_timestamp"],
	}

	return id, nil
}

// RequireIdentity reads and validates a delivery identity from task meta.
// It is the authoritative check before destructive delivery actions.
// Returns an error if the identity is missing, incomplete, or inconsistent.
// This refuses to guess or reconstruct identity from current branch state.
func RequireIdentity(homeDir, id string) (*DeliveryIdentity, error) {
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("reading task meta for identity: %w", err)
	}

	ident, err := IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("parsing delivery identity: %w", err)
	}
	if ident == nil {
		return nil, fmt.Errorf("no delivery identity found for task %s: PR URL not set in meta; use pr-check to capture identity before destructive actions", id)
	}

	if err := ValidateIdentity(ident); err != nil {
		return nil, fmt.Errorf("incomplete delivery identity for task %s: %w; re-run pr-check to recapture", id, err)
	}

	return ident, nil
}

// CaptureIdentity extracts a DeliveryIdentity from a PR URL and GitHub
// API data. It fetches the PR head SHA and branch info via gh-axi CLI when
// the capability is Ready, falling back to gh CLI for JSON fields.
func CaptureIdentity(prURL string) (*DeliveryIdentity, error) {
	client, err := DefaultGitHubClient()
	if err == nil {
		return client.CaptureIdentity(prURL)
	}

	// Degraded path: try gh CLI directly
	ghURL, err := ghurl.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PR URL: %w", err)
	}

	// Fetch PR metadata via gh CLI (head SHA, head ref, base ref)
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

	// Parse JSON output
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

	return &DeliveryIdentity{
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
