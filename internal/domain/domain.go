// Package domain defines pure munsu business rules and value types.
package domain

import (
	"fmt"
	"strconv"
	"strings"
)

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

// --- IssueLink types ---

// IssueLinkRelation specifies the semantic relationship between a task
// and the linked issue. Implementation issues are automatically closed on
// merge; related and parent links are never automatically closed.
type IssueLinkRelation string

const (
	IssueLinkImplementation IssueLinkRelation = "implementation"
	IssueLinkRelated        IssueLinkRelation = "related"
	IssueLinkParent         IssueLinkRelation = "parent"
)

// IssueLinkClosurePolicy specifies how the linked issue should be closed
// when the task's PR is merged.
type IssueLinkClosurePolicy string

const (
	ClosurePolicyAuto   IssueLinkClosurePolicy = "auto-close"
	ClosurePolicyManual IssueLinkClosurePolicy = "manual-close"
	ClosurePolicyNever  IssueLinkClosurePolicy = "never-close"
)

// IssueLinkReconciliationStatus is the outcome of reconciling a single
// issue link against the provider after a merge.
type IssueLinkReconciliationStatus string

const (
	IssueLinkClosed       IssueLinkReconciliationStatus = "closed"
	IssueLinkPending      IssueLinkReconciliationStatus = "pending"
	IssueLinkOpen         IssueLinkReconciliationStatus = "open"
	IssueLinkUnavailable  IssueLinkReconciliationStatus = "unavailable"
	IssueLinkManualPolicy IssueLinkReconciliationStatus = "manual-policy"
)

// IssueLink is a structured reference to a GitHub/GitLab issue that a task
// is connected to. It carries provider identity, the issue URL/number, the
// semantic relation, and the closure policy.
type IssueLink struct {
	Provider      string                  `json:"provider"`
	Owner         string                  `json:"owner"`
	Repo          string                  `json:"repo"`
	Number        int                     `json:"number"`
	URL           string                  `json:"url"`
	Relation      IssueLinkRelation       `json:"relation"`
	ClosurePolicy IssueLinkClosurePolicy  `json:"closurePolicy"`
	ClosingRef    string                  `json:"closingRef,omitempty"`
}

// IssueLinkReconciliationResult captures the outcome of reconciling one
// issue link against the provider after a merge.
type IssueLinkReconciliationResult struct {
	Link   IssueLink                     `json:"link"`
	Status IssueLinkReconciliationStatus `json:"status"`
	Detail string                        `json:"detail,omitempty"`
}

// ValidateIssueLink returns an error if the link is missing required fields.
func ValidateIssueLink(link *IssueLink) error {
	switch {
	case link == nil:
		return fmt.Errorf("issue link is nil")
	case link.URL == "":
		return fmt.Errorf("issue link: URL is required")
	case link.Number <= 0:
		return fmt.Errorf("issue link: number must be positive, got %d", link.Number)
	case link.Relation == "":
		return fmt.Errorf("issue link: relation is required")
	case link.ClosurePolicy == "":
		return fmt.Errorf("issue link: closure policy is required")
	}
	switch link.Relation {
	case IssueLinkImplementation, IssueLinkRelated, IssueLinkParent:
	default:
		return fmt.Errorf("issue link: invalid relation %q", link.Relation)
	}
	switch link.ClosurePolicy {
	case ClosurePolicyAuto, ClosurePolicyManual, ClosurePolicyNever:
	default:
		return fmt.Errorf("issue link: invalid closure policy %q", link.ClosurePolicy)
	}
	return nil
}

// DefaultClosurePolicy returns the default closure policy for a given relation.
// Implementation issues default to auto-close; related and parent to never-close.
func DefaultClosurePolicy(relation IssueLinkRelation) IssueLinkClosurePolicy {
	switch relation {
	case IssueLinkImplementation:
		return ClosurePolicyAuto
	case IssueLinkRelated, IssueLinkParent:
		return ClosurePolicyNever
	default:
		return ClosurePolicyManual
	}
}

// MetaKeys returns the task meta keys used to persist a single issue link
// at the given index in the task meta.
func (l *IssueLink) MetaKeys(index int) []string {
	prefix := fmt.Sprintf("issue_link_%d", index)
	return []string{
		prefix + "_url",
		prefix + "_provider",
		prefix + "_owner",
		prefix + "_repo",
		prefix + "_number",
		prefix + "_relation",
		prefix + "_policy",
		prefix + "_closing_ref",
	}
}

// ToMeta serializes the issue link into task meta key-value pairs.
func (l *IssueLink) ToMeta(index int) map[string]string {
	prefix := fmt.Sprintf("issue_link_%d", index)
	m := map[string]string{
		prefix + "_url":      l.URL,
		prefix + "_provider": l.Provider,
		prefix + "_owner":    l.Owner,
		prefix + "_repo":     l.Repo,
		prefix + "_number":   fmt.Sprintf("%d", l.Number),
		prefix + "_relation": string(l.Relation),
		prefix + "_policy":   string(l.ClosurePolicy),
	}
	if l.ClosingRef != "" {
		m[prefix+"_closing_ref"] = l.ClosingRef
	}
	return m
}

// IssueLinkFromMeta reconstructs a single IssueLink from meta at the given
// index. Returns nil when no issue link is stored at that index.
func IssueLinkFromMeta(meta map[string]string, index int) *IssueLink {
	prefix := fmt.Sprintf("issue_link_%d", index)
	url := meta[prefix+"_url"]
	if url == "" {
		return nil
	}
	num, _ := strconv.Atoi(meta[prefix+"_number"])
	return &IssueLink{
		URL:           url,
		Provider:      meta[prefix+"_provider"],
		Owner:         meta[prefix+"_owner"],
		Repo:          meta[prefix+"_repo"],
		Number:        num,
		Relation:      IssueLinkRelation(meta[prefix+"_relation"]),
		ClosurePolicy: IssueLinkClosurePolicy(meta[prefix+"_policy"]),
		ClosingRef:    meta[prefix+"_closing_ref"],
	}
}

// IssueLinksFromMeta reconstructs all issue links from a task meta map.
// It reads indexed issue_link_N keys until no more are found.
func IssueLinksFromMeta(meta map[string]string) []IssueLink {
	var links []IssueLink
	for i := 0; ; i++ {
		link := IssueLinkFromMeta(meta, i)
		if link == nil {
			break
		}
		links = append(links, *link)
	}
	return links
}

// ClosingReference returns the canonical closing reference for this issue link.
// For same-repo issues, this is just "#N". For cross-repo issues, it is
// "owner/repo#N". This is the string that should appear in the merge commit
// message to trigger automatic closing.
func (l *IssueLink) ClosingReference() string {
	if l.ClosingRef != "" {
		return l.ClosingRef
	}
	if l.Owner != "" && l.Repo != "" && l.Number > 0 {
		return fmt.Sprintf("%s/%s#%d", l.Owner, l.Repo, l.Number)
	}
	return ""
}

// MetaKeys returns the task meta keys used to persist this identity.
