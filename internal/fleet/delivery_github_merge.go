package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

// This file is the GitHub delivery provider (ADR-0026): the typed observation
// and the one irreversible mutation Deliver consumes for an open pull request.
// It follows the GitLab provider's shape (delivery_gitlab.go, ADR-0018): the
// merge is pinned to the exact authorized head by the provider itself, the
// base ref is a named residual fenced by Deliver's observation, and CI proof
// is read per check by id and conclusion, never from a watch command's exit
// code.

// GitHubCheck is one reported CI check of a head commit: a check run (by its
// run id) or a legacy commit status (by its status id). Conclusion is the
// check run's conclusion, or the commit status's state.
type GitHubCheck struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
}

// GitHubDeliveryClient is the typed GitHub capability behind Deliver: the PR
// read, the CI evidence reads and the pinned merge mutation.
type GitHubDeliveryClient interface {
	ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error)
	// RequiredCheckNames returns the contexts the base branch requires, from
	// branch protection and rulesets together.
	RequiredCheckNames(owner, repo, baseRef string) ([]string, error)
	// HeadChecks returns every check run and commit status reported for the
	// head commit.
	HeadChecks(owner, repo, headSHA string) ([]GitHubCheck, error)
	// MergePR merges the PR only if its head is still request.HeadSHA.
	MergePR(owner, repo string, number int, request DeliveryMergeRequest) error
}

var _ GitHubDeliveryClient = (*ghAxiClient)(nil)

// ProbeGitHubDeliveryCapability reports whether GitHub delivery can run: the
// GitHub read capability is Ready and gh, which performs the merge, is on PATH.
func ProbeGitHubDeliveryCapability() backend.State {
	if s := ProbeGitHubCapability(); s != backend.Ready {
		return s
	}
	if _, err := ghCLILookPath(); err != nil {
		return backend.Absent
	}
	return backend.Ready
}

// ghCLI runs one gh invocation and returns stdout; a failure carries stderr.
func ghCLI(args ...string) ([]byte, error) {
	ghPath, err := ghCLILookPath()
	if err != nil {
		return nil, fmt.Errorf("gh not found on PATH: %w", err)
	}
	out, err := exec.Command(ghPath, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// githubNoRequiredChecks lists the provider answers that mean a source holds
// no required check; any other failure fails closed.
var githubNoRequiredChecks = []string{"Branch not protected", "Required status checks not enabled"}

func (c *ghAxiClient) RequiredCheckNames(owner, repo, baseRef string) ([]string, error) {
	if baseRef == "" || strings.ContainsAny(baseRef, "?# \t\n") {
		return nil, fmt.Errorf("GitHub base ref %q is not addressable", baseRef)
	}
	names := map[string]bool{}

	protection, err := ghCLI("api", fmt.Sprintf("repos/%s/%s/branches/%s/protection/required_status_checks", owner, repo, baseRef))
	switch {
	case err == nil:
		var raw struct {
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(protection, &raw); err != nil {
			return nil, fmt.Errorf("parsing GitHub branch protection: %w", err)
		}
		for _, ctx := range raw.Contexts {
			names[ctx] = true
		}
		for _, check := range raw.Checks {
			names[check.Context] = true
		}
	case !containsAny(err.Error(), githubNoRequiredChecks):
		return nil, err
	}

	rules, err := ghCLI("api", fmt.Sprintf("repos/%s/%s/rules/branches/%s", owner, repo, baseRef))
	if err != nil {
		return nil, err
	}
	var rawRules []struct {
		Type       string `json:"type"`
		Parameters struct {
			Required []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(rules, &rawRules); err != nil {
		return nil, fmt.Errorf("parsing GitHub branch rules: %w", err)
	}
	for _, rule := range rawRules {
		if rule.Type != "required_status_checks" {
			continue
		}
		for _, r := range rule.Parameters.Required {
			names[r.Context] = true
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func (c *ghAxiClient) HeadChecks(owner, repo, headSHA string) ([]GitHubCheck, error) {
	runs, err := ghCLI("api", "--paginate", "--slurp", fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", owner, repo, headSHA))
	if err != nil {
		return nil, err
	}
	var runPages []struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(runs, &runPages); err != nil {
		return nil, fmt.Errorf("parsing GitHub check runs: %w", err)
	}
	var checks []GitHubCheck
	for _, page := range runPages {
		for _, r := range page.CheckRuns {
			checks = append(checks, GitHubCheck{ID: r.ID, Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
		}
	}

	statuses, err := ghCLI("api", "--paginate", "--slurp", fmt.Sprintf("repos/%s/%s/commits/%s/statuses?per_page=100", owner, repo, headSHA))
	if err != nil {
		return nil, err
	}
	var statusPages [][]struct {
		ID      int64  `json:"id"`
		Context string `json:"context"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(statuses, &statusPages); err != nil {
		return nil, fmt.Errorf("parsing GitHub commit statuses: %w", err)
	}
	for _, page := range statusPages {
		for _, s := range page {
			status := "in_progress"
			if s.State == "success" || s.State == "failure" || s.State == "error" {
				status = "completed"
			}
			checks = append(checks, GitHubCheck{ID: s.ID, Name: s.Context, Status: status, Conclusion: s.State})
		}
	}
	return checks, nil
}

// MergePR merges with `--match-head-commit`, so GitHub itself refuses a moved
// head as part of the merge. It never passes --auto, --admin or
// --delete-branch: the merge lands now, under the PR's own requirements.
func (c *ghAxiClient) MergePR(owner, repo string, number int, request DeliveryMergeRequest) error {
	flag, err := githubMergeFlag(request.Method)
	if err != nil {
		return err
	}
	if request.HeadSHA == "" {
		return fmt.Errorf("GitHub merge requires a pinned head SHA")
	}
	_, err = ghCLI("pr", "merge", fmt.Sprintf("%d", number), "--repo", owner+"/"+repo, flag, "--match-head-commit", request.HeadSHA)
	return err
}

func githubMergeFlag(method string) (string, error) {
	switch method {
	case "squash":
		return "--squash", nil
	case "merge":
		return "--merge", nil
	case "rebase":
		return "--rebase", nil
	default:
		return "", fmt.Errorf("GitHub merge method %q is unsupported (squash, merge, rebase)", method)
	}
}

// githubDeliveryProvider adapts the typed GitHub capability to the narrow
// delivery capability consumed by Deliver.
type githubDeliveryProvider struct {
	client GitHubDeliveryClient
}

var _ DeliveryProvider = (*githubDeliveryProvider)(nil)

// ValidateMergeRequest verifies the pinned identity constraints before the
// irreversible call. The base ref is checked here as the compensating
// observation for `gh pr merge` having no expected-base parameter.
func (p *githubDeliveryProvider) ValidateMergeRequest(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	if request.HeadSHA == "" || request.HeadSHA != ident.HeadSHA || request.BaseRef == "" || request.BaseRef != ident.BaseRef {
		return fmt.Errorf("GitHub merge constraints do not match the delivery identity")
	}
	_, err := githubMergeFlag(request.Method)
	return err
}

func (p *githubDeliveryProvider) Merge(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	if err := p.ValidateMergeRequest(ident, request); err != nil {
		return err
	}
	if p.client == nil {
		return fmt.Errorf("GitHub delivery capability is not composed")
	}
	ghURL, err := domain.ParseGHURL(ident.URL)
	if err != nil {
		return fmt.Errorf("invalid PR URL in identity: %w", err)
	}
	return p.client.MergePR(ghURL.Owner, ghURL.Repo, ghURL.Num, request)
}

// githubPRView is the PR JSON both the delivery observation and the provider
// snapshot read.
type githubPRView struct {
	State          string `json:"state"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	Mergeable      string `json:"mergeable"`
	ReviewDecision string `json:"reviewDecision"`
	MergeCommit    *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

const githubPRViewFields = "state,headRefOid,headRefName,baseRefName,mergeable,reviewDecision,mergeCommit"

func readGitHubPRView(client GitHubDeliveryClient, ghURL domain.GHURL) (githubPRView, error) {
	data, err := client.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, githubPRViewFields)
	if err != nil {
		return githubPRView{}, err
	}
	var raw githubPRView
	if err := json.Unmarshal(data, &raw); err != nil {
		return githubPRView{}, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	raw.State = strings.ToUpper(raw.State)
	return raw, nil
}

// Observe reads the current provider state under the exact identity.
func (p *githubDeliveryProvider) Observe(ident domain.DeliveryIdentity) (DeliveryProviderObservation, error) {
	if p.client == nil {
		return DeliveryProviderObservation{}, fmt.Errorf("GitHub delivery capability is not composed")
	}
	ghURL, err := domain.ParseGHURL(ident.URL)
	if err != nil {
		return DeliveryProviderObservation{}, fmt.Errorf("invalid PR URL in identity: %w", err)
	}
	view, err := readGitHubPRView(p.client, ghURL)
	if err != nil {
		return DeliveryProviderObservation{}, err
	}
	obs := DeliveryProviderObservation{State: view.State, HeadSHA: view.HeadRefOid, BaseRef: view.BaseRefName}
	switch view.State {
	case "MERGED":
		if view.MergeCommit != nil {
			obs.MergedSHA = view.MergeCommit.Oid
		}
	case "OPEN":
		pr, mergeableOK, err := readGitHubOpenPR(p.client, ghURL, view)
		if err != nil {
			return DeliveryProviderObservation{}, err
		}
		if mergeableOK && pr.CanMerge() {
			obs.Mergeability = DeliveryMergeabilityAllowed
		} else {
			obs.Mergeability = DeliveryMergeabilityDenied
		}
	}
	return obs, nil
}

// readGitHubOpenPR reads every acceptance input of an open PR at its observed
// head as the domain.PR that domain.PR.CanMerge decides on. mergeableOK
// reports the separate `mergeable == MERGEABLE` fence (conflicts), which
// CanMerge does not model; when it is false the CI evidence is not read. A CI
// proof that is empty, or that lacks a required check, is an error: an empty
// proof fails closed (it is never read as "nothing failed").
func readGitHubOpenPR(client GitHubDeliveryClient, ghURL domain.GHURL, view githubPRView) (pr domain.PR, mergeableOK bool, err error) {
	if view.Mergeable != "MERGEABLE" {
		return domain.PR{}, false, nil
	}
	if view.HeadRefOid == "" || view.BaseRefName == "" {
		return domain.PR{}, false, fmt.Errorf("GitHub PR observation is missing head or base evidence")
	}
	required, err := client.RequiredCheckNames(ghURL.Owner, ghURL.Repo, view.BaseRefName)
	if err != nil {
		return domain.PR{}, false, err
	}
	reported, err := client.HeadChecks(ghURL.Owner, ghURL.Repo, view.HeadRefOid)
	if err != nil {
		return domain.PR{}, false, err
	}
	checks, err := evaluateGitHubChecks(required, reported)
	if err != nil {
		return domain.PR{}, false, err
	}
	pr = domain.PR{Number: ghURL.Num, Status: domain.PROpen, Checks: checks}
	if strings.EqualFold(view.ReviewDecision, "CHANGES_REQUESTED") {
		pr.Reviews = []domain.Review{{State: domain.ReviewChangesRequested}}
	}
	return pr, true, nil
}

// evaluateGitHubChecks turns the head's reported checks into the domain check
// runs, reading each check's own conclusion (the latest report per name by id).
// It refuses when nothing reported, when a required check never reported, and
// when nothing but skipped optional checks remains. A skipped or neutral
// required check counts as passed, as GitHub's own requirement does; a skipped
// optional check carries no proof and is left out.
func evaluateGitHubChecks(required []string, reported []GitHubCheck) ([]domain.CheckRun, error) {
	if len(reported) == 0 {
		return nil, fmt.Errorf("GitHub reported no check for the head: an empty CI proof is refused")
	}
	latest := map[string]GitHubCheck{}
	for _, c := range reported {
		if prev, ok := latest[c.Name]; !ok || c.ID > prev.ID {
			latest[c.Name] = c
		}
	}
	isRequired := map[string]bool{}
	for _, name := range required {
		if _, ok := latest[name]; !ok {
			return nil, fmt.Errorf("required check %q never reported for the head", name)
		}
		isRequired[name] = true
	}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Strings(names)
	var checks []domain.CheckRun
	for _, name := range names {
		c := latest[name]
		var status domain.CheckStatus
		switch {
		case c.Status != "completed":
			status = domain.CheckPending
		case c.Conclusion == "success":
			status = domain.CheckPassed
		case c.Conclusion == "skipped" || c.Conclusion == "neutral":
			if !isRequired[name] {
				continue
			}
			status = domain.CheckPassed
		default:
			status = domain.CheckFailed
		}
		checks = append(checks, domain.CheckRun{Name: name, Status: status})
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("GitHub reported only skipped optional checks for the head: an empty CI proof is refused")
	}
	return checks, nil
}
