// Package domain defines pure munsu business rules and value types.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrCheckValidationRefused indicates that a check artifact failed validation
// during retirement.
var ErrCheckValidationRefused = errors.New("check validation refused")

// ErrCheckInvalidAfterPublication indicates that post-publication
// revalidation refused the check artifact. It also matches
// ErrCheckValidationRefused because this is a validation refusal.
var ErrCheckInvalidAfterPublication = fmt.Errorf("%w: check invalid after publication", ErrCheckValidationRefused)

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

// ParseProviderURL parses a GitHub PR URL or GitLab MR URL.
func ParseProviderURL(rawURL string) (provider, owner, repo string, num int, host string, err error) {
	if strings.Contains(rawURL, "github.com") || strings.Contains(rawURL, "/pull/") {
		ghURL, err := ParseGHURL(rawURL)
		if err != nil {
			return "", "", "", 0, "", err
		}
		return "github", ghURL.Owner, ghURL.Repo, ghURL.Num, "github.com", nil
	}
	if strings.Contains(rawURL, "gitlab.com") || strings.Contains(rawURL, "/merge_requests/") {
		glURL, err := ParseMRURL(rawURL)
		if err != nil {
			return "", "", "", 0, "", err
		}
		return "gitlab", glURL.Owner, glURL.Project, glURL.IID, glURL.Host, nil
	}
	return "", "", "", 0, "", fmt.Errorf("unsupported provider URL %q", rawURL)
}

// IdentityFromMeta reconstructs a DeliveryIdentity from task meta.
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

	urlProvider, parsedOwner, parsedRepo, parsedNum, _, err := ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid pr_url %q: %w", prURL, err)
	}
	owner := meta["pr_owner"]
	repo := meta["pr_repo"]

	if metaProvider := meta["pr_provider"]; metaProvider != "" && metaProvider != urlProvider {
		return nil, fmt.Errorf("provider mismatch: pr_provider=%q but URL provider=%q", metaProvider, urlProvider)
	}
	if owner != "" && owner != parsedOwner {
		return nil, fmt.Errorf("pr_owner %q does not match pr_url owner %q", owner, parsedOwner)
	}
	if repo != "" && repo != parsedRepo {
		return nil, fmt.Errorf("pr_repo %q does not match pr_url repo %q", repo, parsedRepo)
	}
	if num > 0 && num != parsedNum {
		return nil, fmt.Errorf("pr_number %d does not match pr_url number %d", num, parsedNum)
	}
	if owner == "" {
		owner = parsedOwner
	}
	if repo == "" {
		repo = parsedRepo
	}
	if num <= 0 {
		num = parsedNum
	}

	headSHA := meta["pr_head_sha"]
	if headSHA == "" {
		headSHA = meta["pr_head"]
	} else if other := meta["pr_head"]; other != "" && other != headSHA {
		return nil, fmt.Errorf("pr_head_sha %q conflicts with pr_head %q", headSHA, other)
	}

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

// PRMergeStatus holds the provider-confirmed merge state of a pull request.
type PRMergeStatus struct {
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	MergedAt  string `json:"mergedAt,omitempty"`
	MergedSHA string `json:"mergedSha,omitempty"`
	Closed    bool   `json:"closed"`
	ClosedAt  string `json:"closedAt,omitempty"`
	HeadSHA   string `json:"headRefOid,omitempty"`
}

// DeliveryState represents the task delivery lifecycle state.
type DeliveryState string

const (
	DeliveryStateReviewReady DeliveryState = "review-ready"
	DeliveryStatePRCheck     DeliveryState = "pr-check"
	DeliveryStateMerged      DeliveryState = "merged"
)

const MetaDeliveryState = "delivery_state"

// MetaKeys returns the task meta keys used to persist this identity.
