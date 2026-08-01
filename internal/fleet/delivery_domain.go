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

// Delivery acceptance aliases preserve the fleet API while internal/domain
// remains the single owner of merge business rules.
type (
	PRStatus    = domain.PRStatus
	CheckStatus = domain.CheckStatus
	ReviewState = domain.ReviewState
	CheckRun    = domain.CheckRun
	Review      = domain.Review
	PR          = domain.PR
)

const (
	PROpen                 = domain.PROpen
	PRClosed               = domain.PRClosed
	PRMerged               = domain.PRMerged
	CheckPassed            = domain.CheckPassed
	CheckFailed            = domain.CheckFailed
	CheckPending           = domain.CheckPending
	CheckSkipped           = domain.CheckSkipped
	ReviewApproved         = domain.ReviewApproved
	ReviewChangesRequested = domain.ReviewChangesRequested
	ReviewPending          = domain.ReviewPending
	ReviewDismissed        = domain.ReviewDismissed
)

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
