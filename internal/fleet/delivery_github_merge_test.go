package fleet

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

func TestEvaluateGitHubChecks(t *testing.T) {
	done := func(id int64, name, conclusion string) GitHubCheck {
		return GitHubCheck{Source: GitHubCheckRun, ID: id, Name: name, Status: "completed", Conclusion: conclusion}
	}
	status := func(id int64, name, state string) GitHubCheck {
		return GitHubCheck{Source: GitHubCommitStatus, ID: id, Name: name, Status: "completed", Conclusion: state}
	}
	for _, tc := range []struct {
		name     string
		required []string
		reported []GitHubCheck
		want     []domain.CheckRun
		wantErr  string
	}{
		{name: "nothing reported", wantErr: "an empty CI proof is refused"},
		{name: "nothing reported with a requirement", required: []string{"ci"}, wantErr: "an empty CI proof is refused"},
		{name: "required check never reported", required: []string{"ci", "lint"}, reported: []GitHubCheck{done(1, "ci", "success")}, wantErr: `required check "lint" never reported`},
		{name: "only skipped optional checks", reported: []GitHubCheck{done(1, "docs", "skipped"), done(2, "fmt", "neutral")}, wantErr: "only skipped optional checks"},
		{
			name:     "each check reads its own conclusion and sorts by name",
			reported: []GitHubCheck{done(3, "z-ci", "success"), done(1, "a-lint", "failure"), {Source: GitHubCheckRun, ID: 2, Name: "m-e2e", Status: "in_progress"}, done(4, "b-err", "timed_out")},
			want: []domain.CheckRun{
				{Name: "a-lint", Status: domain.CheckFailed}, {Name: "b-err", Status: domain.CheckFailed},
				{Name: "m-e2e", Status: domain.CheckPending}, {Name: "z-ci", Status: domain.CheckPassed},
			},
		},
		{
			name:     "the latest report per name by id wins",
			reported: []GitHubCheck{done(5, "ci", "success"), done(9, "ci", "failure"), done(2, "ci", "failure")},
			want:     []domain.CheckRun{{Name: "ci", Status: domain.CheckFailed}},
		},
		{
			name:     "a passing check run with the larger id does not hide a failing commit status of the same name",
			reported: []GitHubCheck{done(900, "ci", "success"), status(7, "ci", "failure")},
			want:     []domain.CheckRun{{Name: "ci", Status: domain.CheckFailed}},
		},
		{
			name:     "a required check reported only as a commit status counts as reported",
			required: []string{"legacy"},
			reported: []GitHubCheck{done(1, "ci", "success"), status(2, "legacy", "success")},
			want:     []domain.CheckRun{{Name: "ci", Status: domain.CheckPassed}, {Name: "legacy", Status: domain.CheckPassed}},
		},
		{
			name:     "a later success supersedes an earlier failure",
			reported: []GitHubCheck{done(1, "ci", "failure"), done(2, "ci", "success")},
			want:     []domain.CheckRun{{Name: "ci", Status: domain.CheckPassed}},
		},
		{
			name:     "skipped or neutral required is not passed and skipped optional is left out",
			required: []string{"gate", "other"},
			reported: []GitHubCheck{done(1, "gate", "skipped"), done(2, "other", "neutral"), done(3, "docs", "skipped"), done(4, "ci", "success")},
			want: []domain.CheckRun{
				{Name: "ci", Status: domain.CheckPassed}, {Name: "gate", Status: domain.CheckSkipped}, {Name: "other", Status: domain.CheckSkipped},
			},
		},
		{
			name:     "a skipped required report is not hidden by a passing report of the same name from another source",
			required: []string{"gate"},
			reported: []GitHubCheck{done(9, "gate", "skipped"), status(1, "gate", "success")},
			want:     []domain.CheckRun{{Name: "gate", Status: domain.CheckSkipped}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluateGitHubChecks(tc.required, tc.reported)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("checks = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGitHubMergeFlag(t *testing.T) {
	for method, want := range map[string]string{"squash": "--squash", "merge": "--merge", "rebase": "--rebase"} {
		if got, err := githubMergeFlag(method); err != nil || got != want {
			t.Errorf("githubMergeFlag(%q) = %q, %v; want %q", method, got, err, want)
		}
	}
	for _, method := range []string{"", "fast-forward", "SQUASH"} {
		if _, err := githubMergeFlag(method); err == nil || !strings.Contains(err.Error(), "unsupported (squash, merge, rebase)") {
			t.Errorf("githubMergeFlag(%q) err = %v, want an unsupported refusal", method, err)
		}
	}
}

// fakeGitHubDelivery scripts the typed GitHub delivery capability.
type fakeGitHubDelivery struct {
	view     string
	viewErr  error
	required []string
	reqErr   error
	checks   []GitHubCheck
	checkErr error
	mergeErr error

	calls  []string
	merged *DeliveryMergeRequest
	target string
}

func (f *fakeGitHubDelivery) ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error) {
	f.calls = append(f.calls, "view")
	if fields != githubPRViewFields {
		return nil, fmt.Errorf("view fields = %q", fields)
	}
	return []byte(f.view), f.viewErr
}

func (f *fakeGitHubDelivery) RequiredCheckNames(owner, repo, baseRef string) ([]string, error) {
	f.calls = append(f.calls, "required:"+baseRef)
	return f.required, f.reqErr
}

func (f *fakeGitHubDelivery) HeadChecks(owner, repo, headSHA string) ([]GitHubCheck, error) {
	f.calls = append(f.calls, "checks:"+headSHA)
	return f.checks, f.checkErr
}

func (f *fakeGitHubDelivery) MergePR(owner, repo string, number int, request DeliveryMergeRequest) error {
	f.calls = append(f.calls, "merge")
	f.merged, f.target = &request, fmt.Sprintf("%s/%s#%d", owner, repo, number)
	return f.mergeErr
}

const (
	githubDeliveryHead = "1111111111111111111111111111111111111111"
	githubDeliveryURL  = "https://github.com/acme/widgets/pull/7"
)

func githubDeliveryIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider: "github", Owner: "acme", Repo: "widgets", Number: 7, URL: githubDeliveryURL,
		BaseRef: "main", HeadRef: "feature", HeadSHA: githubDeliveryHead, CapturedAt: "2026-08-05T00:00:00Z",
	}
}

func githubDeliveryView(state, mergeable, mergeState, decision, merged string) string {
	mergeCommit := "null"
	if merged != "" {
		mergeCommit = fmt.Sprintf(`{"oid":%q}`, merged)
	}
	return fmt.Sprintf(`{"state":%q,"headRefOid":%q,"headRefName":"feature","baseRefName":"main","mergeable":%q,"mergeStateStatus":%q,"reviewDecision":%q,"mergeCommit":%s}`,
		state, githubDeliveryHead, mergeable, mergeState, decision, mergeCommit)
}

func TestGitHubDeliveryProviderValidateAndMerge(t *testing.T) {
	ident := githubDeliveryIdentity()
	good := DeliveryMergeRequest{Method: "squash", HeadSHA: githubDeliveryHead, BaseRef: "main"}

	t.Run("a matching request merges the exact head", func(t *testing.T) {
		client := &fakeGitHubDelivery{}
		if err := (&githubDeliveryProvider{client: client}).Merge(ident, good); err != nil {
			t.Fatal(err)
		}
		if client.merged == nil || *client.merged != good || client.target != "acme/widgets#7" {
			t.Fatalf("merged %+v on %q, want %+v on acme/widgets#7", client.merged, client.target, good)
		}
	})
	var emptyIdentityHead, emptyIdentityBase bool
	for name, mutate := range map[string]func(*DeliveryMergeRequest){
		"empty head":                     func(r *DeliveryMergeRequest) { r.HeadSHA = "" },
		"empty head, identity with none": func(r *DeliveryMergeRequest) { r.HeadSHA = ""; emptyIdentityHead = true },
		"another head":                   func(r *DeliveryMergeRequest) { r.HeadSHA = strings.Repeat("2", 40) },
		"empty base":                     func(r *DeliveryMergeRequest) { r.BaseRef = "" },
		"empty base, identity with none": func(r *DeliveryMergeRequest) { r.BaseRef = ""; emptyIdentityBase = true },
		"another base":                   func(r *DeliveryMergeRequest) { r.BaseRef = "release" },
		"unsupported method":             func(r *DeliveryMergeRequest) { r.Method = "ff" },
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			emptyIdentityHead, emptyIdentityBase = false, false
			req := good
			mutate(&req)
			id := ident
			if emptyIdentityHead {
				id.HeadSHA = ""
			}
			if emptyIdentityBase {
				id.BaseRef = ""
			}
			client := &fakeGitHubDelivery{}
			p := &githubDeliveryProvider{client: client}
			if err := p.ValidateMergeRequest(id, req); err == nil {
				t.Fatal("ValidateMergeRequest accepted the request")
			}
			if err := p.Merge(id, req); err == nil || client.merged != nil {
				t.Fatalf("Merge err = %v merged = %+v, want a refusal before the mutation", err, client.merged)
			}
		})
	}
	t.Run("no client", func(t *testing.T) {
		if err := (&githubDeliveryProvider{}).Merge(ident, good); err == nil || !strings.Contains(err.Error(), "not composed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("identity url is not a github pull request", func(t *testing.T) {
		bad := ident
		bad.URL = "https://gitlab.com/acme/widgets/-/merge_requests/7"
		client := &fakeGitHubDelivery{}
		if err := (&githubDeliveryProvider{client: client}).Merge(bad, good); err == nil || !strings.Contains(err.Error(), "invalid PR URL in identity") || client.merged != nil {
			t.Fatalf("err = %v merged = %+v", err, client.merged)
		}
	})
	t.Run("the merge failure is returned", func(t *testing.T) {
		client := &fakeGitHubDelivery{mergeErr: errors.New("head moved")}
		if err := (&githubDeliveryProvider{client: client}).Merge(ident, good); err == nil || !strings.Contains(err.Error(), "head moved") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGitHubDeliveryProviderObserve(t *testing.T) {
	ident := githubDeliveryIdentity()
	passing := []GitHubCheck{{ID: 1, Name: "ci", Status: "completed", Conclusion: "success"}}

	t.Run("open, mergeable and green is allowed", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("open", "MERGEABLE", "CLEAN", "APPROVED", ""), required: []string{"ci"}, checks: passing}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil {
			t.Fatal(err)
		}
		want := DeliveryProviderObservation{State: "OPEN", HeadSHA: githubDeliveryHead, BaseRef: "main", Mergeability: DeliveryMergeabilityAllowed}
		if obs != want {
			t.Fatalf("observation = %+v, want %+v", obs, want)
		}
		if !reflect.DeepEqual(client.calls, []string{"view", "required:main", "checks:" + githubDeliveryHead}) {
			t.Fatalf("calls = %v, want the view then the evidence of the observed head", client.calls)
		}
	})
	t.Run("a conflicting PR is denied and reads no CI evidence", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "CONFLICTING", "CLEAN", "", "")}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.Mergeability != DeliveryMergeabilityDenied {
			t.Fatalf("observation = %+v, %v; want denied", obs, err)
		}
		if !reflect.DeepEqual(client.calls, []string{"view"}) {
			t.Fatalf("calls = %v, want the view only", client.calls)
		}
	})
	t.Run("the merge state decides on an otherwise mergeable PR", func(t *testing.T) {
		for _, tc := range []struct {
			state string
			want  DeliveryMergeability
		}{
			{"BEHIND", DeliveryMergeabilityDenied}, {"UNKNOWN", DeliveryMergeabilityDenied}, {"", DeliveryMergeabilityDenied},
			{"FUTURE_STATE", DeliveryMergeabilityDenied},
			{"CLEAN", DeliveryMergeabilityAllowed}, {"BLOCKED", DeliveryMergeabilityAllowed}, {"DIRTY", DeliveryMergeabilityAllowed},
			{"DRAFT", DeliveryMergeabilityAllowed}, {"HAS_HOOKS", DeliveryMergeabilityAllowed}, {"UNSTABLE", DeliveryMergeabilityAllowed},
		} {
			client := &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", tc.state, "", ""), required: []string{"ci"}, checks: passing}
			obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
			if err != nil || obs.Mergeability != tc.want {
				t.Fatalf("merge state %q: observation = %+v, %v; want %q", tc.state, obs, err, tc.want)
			}
		}
		absent := &fakeGitHubDelivery{view: fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"headRefName":"feature","baseRefName":"main","mergeable":"MERGEABLE"}`, githubDeliveryHead), required: []string{"ci"}, checks: passing}
		if obs, err := (&githubDeliveryProvider{client: absent}).Observe(ident); err != nil || obs.Mergeability != DeliveryMergeabilityDenied {
			t.Fatalf("absent merge state: observation = %+v, %v; want denied", obs, err)
		}
	})
	t.Run("changes requested is denied", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "CHANGES_REQUESTED", ""), required: []string{"ci"}, checks: passing}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.Mergeability != DeliveryMergeabilityDenied {
			t.Fatalf("observation = %+v, %v; want denied", obs, err)
		}
	})
	t.Run("a failing check is denied", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", ""), checks: []GitHubCheck{{ID: 1, Name: "ci", Status: "completed", Conclusion: "failure"}}}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.Mergeability != DeliveryMergeabilityDenied {
			t.Fatalf("observation = %+v, %v; want denied", obs, err)
		}
	})
	for _, conclusion := range []string{"skipped", "neutral"} {
		t.Run("a "+conclusion+" required check is denied", func(t *testing.T) {
			client := &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", ""), required: []string{"ci"}, checks: []GitHubCheck{{ID: 1, Name: "ci", Status: "completed", Conclusion: conclusion}}}
			obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
			if err != nil || obs.Mergeability != DeliveryMergeabilityDenied {
				t.Fatalf("observation = %+v, %v; want denied", obs, err)
			}
		})
	}
	t.Run("a merged PR carries its merge commit and reads nothing else", func(t *testing.T) {
		merged := strings.Repeat("3", 40)
		client := &fakeGitHubDelivery{view: githubDeliveryView("MERGED", "UNKNOWN", "CLEAN", "", merged)}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.State != "MERGED" || obs.MergedSHA != merged || obs.Mergeability != "" {
			t.Fatalf("observation = %+v, %v", obs, err)
		}
		if !reflect.DeepEqual(client.calls, []string{"view"}) {
			t.Fatalf("calls = %v", client.calls)
		}
	})
	t.Run("a merged PR without a merge commit has no merged sha", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("MERGED", "", "CLEAN", "", "")}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.MergedSHA != "" {
			t.Fatalf("observation = %+v, %v", obs, err)
		}
	})
	t.Run("a closed PR carries no mergeability", func(t *testing.T) {
		client := &fakeGitHubDelivery{view: githubDeliveryView("CLOSED", "", "CLEAN", "", "")}
		obs, err := (&githubDeliveryProvider{client: client}).Observe(ident)
		if err != nil || obs.State != "CLOSED" || obs.Mergeability != "" {
			t.Fatalf("observation = %+v, %v", obs, err)
		}
	})

	refusals := []struct {
		name    string
		client  *fakeGitHubDelivery
		ident   func(*domain.DeliveryIdentity)
		wantErr string
	}{
		{name: "view failure", client: &fakeGitHubDelivery{viewErr: errors.New("gh down")}, wantErr: "gh down"},
		{name: "view is not json", client: &fakeGitHubDelivery{view: "not json"}, wantErr: "parsing gh pr view output"},
		{name: "required checks unreadable", client: &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", ""), reqErr: errors.New("no protection read")}, wantErr: "no protection read"},
		{name: "head checks unreadable", client: &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", ""), checkErr: errors.New("no checks read")}, wantErr: "no checks read"},
		{name: "empty proof", client: &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", "")}, wantErr: "an empty CI proof is refused"},
		{name: "required check never reported", client: &fakeGitHubDelivery{view: githubDeliveryView("OPEN", "MERGEABLE", "CLEAN", "", ""), required: []string{"ci"}, checks: []GitHubCheck{{ID: 1, Name: "lint", Status: "completed", Conclusion: "success"}}}, wantErr: `required check "ci" never reported`},
		{name: "no head evidence", client: &fakeGitHubDelivery{view: `{"state":"OPEN","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","baseRefName":"main"}`}, wantErr: "missing head or base evidence"},
		{name: "no base evidence", client: &fakeGitHubDelivery{view: fmt.Sprintf(`{"state":"OPEN","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","headRefOid":%q}`, githubDeliveryHead)}, wantErr: "missing head or base evidence"},
		{name: "identity url is not a github pull request", client: &fakeGitHubDelivery{}, ident: func(i *domain.DeliveryIdentity) { i.URL = "https://example.com/x" }, wantErr: "invalid PR URL in identity"},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			id := ident
			if tc.ident != nil {
				tc.ident(&id)
			}
			if _, err := (&githubDeliveryProvider{client: tc.client}).Observe(id); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
	t.Run("refuses without a client", func(t *testing.T) {
		if _, err := (&githubDeliveryProvider{}).Observe(ident); err == nil || !strings.Contains(err.Error(), "not composed") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestProbeGitHubDeliveryCapabilityNeedsBothCLIs(t *testing.T) {
	installFakeGH(t)
	okGH, okAxi := ghCLILookPath, ghAxiLookPath
	missing := func() (string, error) { return "", errors.New("not installed") }
	for _, tc := range []struct {
		name    string
		gh, axi func() (string, error)
		want    backend.State
	}{
		{"both", okGH, okAxi, backend.Ready},
		{"gh missing", missing, okAxi, backend.Absent},
		{"gh-axi missing", okGH, missing, backend.Absent},
		{"both missing", missing, missing, backend.Absent},
	} {
		ghCLILookPath, ghAxiLookPath = tc.gh, tc.axi
		if got := ProbeGitHubDeliveryCapability(); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGhCLIFailureCarriesStderrAndMissingBinary(t *testing.T) {
	installFakeGH(t, ghReply{match: "boom", stderr: "  exploded\n", exit: 3})
	if _, err := ghCLI("boom"); err == nil || !strings.Contains(err.Error(), "gh boom: exploded") {
		t.Fatalf("err = %v, want the trimmed stderr", err)
	}
	ghCLILookPath = func() (string, error) { return "", errors.New("missing") }
	if _, err := ghCLI("x"); err == nil || !strings.Contains(err.Error(), "gh not found on PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestGhAxiClientRequiredCheckNames(t *testing.T) {
	const protection = "branches/main/protection/required_status_checks"
	const rules = "rules/branches/main"
	c := &ghAxiClient{}

	t.Run("protection and rulesets are read together and sorted", func(t *testing.T) {
		installFakeGH(t,
			ghReply{match: protection, stdout: `{"contexts":["legacy","only-context","shared"],"checks":[{"context":"shared"},{"context":"app"}]}`},
			ghReply{match: rules, stdout: `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"ruleset"},{"context":"legacy"}]}},{"type":"pull_request","parameters":{"required_status_checks":[{"context":"ignored"}]}}]`},
		)
		got, err := c.RequiredCheckNames("acme", "widgets", "main")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"app", "legacy", "only-context", "ruleset", "shared"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("names = %v, want %v", got, want)
		}
	})
	for _, answer := range []string{"Branch not protected", "Required status checks not enabled"} {
		t.Run("protection answer "+answer+" holds no required check", func(t *testing.T) {
			installFakeGH(t,
				ghReply{match: protection, stderr: "gh: " + answer + " (HTTP 404)", exit: 1},
				ghReply{match: rules, stdout: `[]`},
			)
			got, err := c.RequiredCheckNames("acme", "widgets", "main")
			if err != nil || len(got) != 0 {
				t.Fatalf("names = %v, %v; want none", got, err)
			}
		})
	}
	t.Run("any other protection failure fails closed", func(t *testing.T) {
		fake := installFakeGH(t, ghReply{match: protection, stderr: "HTTP 500", exit: 1}, ghReply{match: rules, stdout: `[]`})
		if _, err := c.RequiredCheckNames("acme", "widgets", "main"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("err = %v", err)
		}
		if calls := fake.calls(t); len(calls) != 1 {
			t.Fatalf("calls = %v, want the rules read skipped", calls)
		}
	})
	t.Run("unparseable protection fails closed", func(t *testing.T) {
		installFakeGH(t, ghReply{match: protection, stdout: "{"})
		if _, err := c.RequiredCheckNames("acme", "widgets", "main"); err == nil || !strings.Contains(err.Error(), "parsing GitHub branch protection") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rules failure fails closed", func(t *testing.T) {
		installFakeGH(t, ghReply{match: protection, stdout: `{}`}, ghReply{match: rules, stderr: "HTTP 403", exit: 1})
		if _, err := c.RequiredCheckNames("acme", "widgets", "main"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unparseable rules fail closed", func(t *testing.T) {
		installFakeGH(t, ghReply{match: protection, stdout: `{}`}, ghReply{match: rules, stdout: "{"})
		if _, err := c.RequiredCheckNames("acme", "widgets", "main"); err == nil || !strings.Contains(err.Error(), "parsing GitHub branch rules") {
			t.Fatalf("err = %v", err)
		}
	})
	for _, ref := range []string{"", "a b", "a?b", "a#b", "a\tb", "a\nb"} {
		t.Run(fmt.Sprintf("base ref %q is not addressable", ref), func(t *testing.T) {
			fake := installFakeGH(t)
			if _, err := c.RequiredCheckNames("acme", "widgets", ref); err == nil || !strings.Contains(err.Error(), "is not addressable") {
				t.Fatalf("err = %v", err)
			}
			if calls := fake.calls(t); len(calls) != 0 {
				t.Fatalf("calls = %v, want none", calls)
			}
		})
	}
}

func TestGhAxiClientHeadChecks(t *testing.T) {
	const runs = "commits/" + githubDeliveryHead + "/check-runs"
	const statuses = "commits/" + githubDeliveryHead + "/statuses"
	c := &ghAxiClient{}

	t.Run("check runs and commit statuses across pages", func(t *testing.T) {
		fake := installFakeGH(t,
			ghReply{match: runs, stdout: `[{"check_runs":[{"id":11,"name":"ci","status":"completed","conclusion":"success"}]},{"check_runs":[{"id":12,"name":"e2e","status":"in_progress","conclusion":null}]}]`},
			ghReply{match: statuses, stdout: `[[{"id":21,"context":"legacy-ok","state":"success"},{"id":22,"context":"legacy-bad","state":"failure"}],[{"id":23,"context":"legacy-err","state":"error"},{"id":24,"context":"legacy-wait","state":"pending"}]]`},
		)
		got, err := c.HeadChecks("acme", "widgets", githubDeliveryHead)
		if err != nil {
			t.Fatal(err)
		}
		want := []GitHubCheck{
			{Source: GitHubCheckRun, ID: 11, Name: "ci", Status: "completed", Conclusion: "success"},
			{Source: GitHubCheckRun, ID: 12, Name: "e2e", Status: "in_progress"},
			{Source: GitHubCommitStatus, ID: 21, Name: "legacy-ok", Status: "completed", Conclusion: "success"},
			{Source: GitHubCommitStatus, ID: 22, Name: "legacy-bad", Status: "completed", Conclusion: "failure"},
			{Source: GitHubCommitStatus, ID: 23, Name: "legacy-err", Status: "completed", Conclusion: "error"},
			{Source: GitHubCommitStatus, ID: 24, Name: "legacy-wait", Status: "in_progress", Conclusion: "pending"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("checks = %+v, want %+v", got, want)
		}
		for _, call := range fake.calls(t) {
			if !strings.Contains(call, "--paginate --slurp") || !strings.Contains(call, "per_page=100") {
				t.Fatalf("call %q does not read every page", call)
			}
		}
	})
	t.Run("a head with no report reads as no checks", func(t *testing.T) {
		installFakeGH(t, ghReply{match: runs, stdout: `[{"check_runs":[]}]`}, ghReply{match: statuses, stdout: `[[]]`})
		got, err := c.HeadChecks("acme", "widgets", githubDeliveryHead)
		if err != nil || len(got) != 0 {
			t.Fatalf("checks = %v, %v", got, err)
		}
	})
	for _, tc := range []struct {
		name    string
		replies []ghReply
		wantErr string
	}{
		{"check runs unreadable", []ghReply{{match: runs, stderr: "HTTP 500", exit: 1}}, "HTTP 500"},
		{"check runs unparseable", []ghReply{{match: runs, stdout: "{"}}, "parsing GitHub check runs"},
		{"statuses unreadable", []ghReply{{match: runs, stdout: `[]`}, {match: statuses, stderr: "HTTP 502", exit: 1}}, "HTTP 502"},
		{"statuses unparseable", []ghReply{{match: runs, stdout: `[]`}, {match: statuses, stdout: "{"}}, "parsing GitHub commit statuses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installFakeGH(t, tc.replies...)
			if _, err := c.HeadChecks("acme", "widgets", githubDeliveryHead); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestGhAxiClientMergePRPinsTheHeadAndNeverForcesTheMerge(t *testing.T) {
	c := &ghAxiClient{}
	for method, flag := range map[string]string{"squash": "--squash", "merge": "--merge", "rebase": "--rebase"} {
		t.Run(method, func(t *testing.T) {
			fake := installFakeGH(t, ghReply{match: "pr merge"})
			if err := c.MergePR("acme", "widgets", 7, DeliveryMergeRequest{Method: method, HeadSHA: githubDeliveryHead, BaseRef: "main"}); err != nil {
				t.Fatal(err)
			}
			want := "pr merge 7 --repo acme/widgets " + flag + " --match-head-commit " + githubDeliveryHead
			if calls := fake.calls(t); !reflect.DeepEqual(calls, []string{want}) {
				t.Fatalf("calls = %q, want %q", calls, want)
			}
			for _, forbidden := range []string{"--auto", "--admin", "--delete-branch"} {
				if strings.Contains(want, forbidden) {
					t.Fatalf("merge argv carries %s", forbidden)
				}
			}
		})
	}
	t.Run("refuses an unpinned head before gh runs", func(t *testing.T) {
		fake := installFakeGH(t, ghReply{match: "pr merge"})
		if err := c.MergePR("acme", "widgets", 7, DeliveryMergeRequest{Method: "squash"}); err == nil || !strings.Contains(err.Error(), "pinned head SHA") {
			t.Fatalf("err = %v", err)
		}
		if calls := fake.calls(t); len(calls) != 0 {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("refuses an unsupported method before gh runs", func(t *testing.T) {
		fake := installFakeGH(t, ghReply{match: "pr merge"})
		if err := c.MergePR("acme", "widgets", 7, DeliveryMergeRequest{Method: "ff", HeadSHA: githubDeliveryHead}); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("err = %v", err)
		}
		if calls := fake.calls(t); len(calls) != 0 {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("a moved head is GitHub's refusal", func(t *testing.T) {
		installFakeGH(t, ghReply{match: "pr merge", stderr: "Head branch was modified", exit: 1})
		if err := c.MergePR("acme", "widgets", 7, DeliveryMergeRequest{Method: "squash", HeadSHA: githubDeliveryHead}); err == nil || !strings.Contains(err.Error(), "Head branch was modified") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFetchGitHubProviderSnapshotRefusesAnInvalidURL(t *testing.T) {
	if _, err := fetchGitHubProviderSnapshot("https://example.com/not/a/pull"); err == nil || !strings.Contains(err.Error(), "invalid GitHub URL") {
		t.Fatalf("fetchGitHubProviderSnapshot error = %v, want the invalid-URL refusal", err)
	}
}

func TestFetchGitHubProviderSnapshotRefusesWhenTheDeliveryCapabilityIsNotReady(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := fetchGitHubProviderSnapshot("https://github.com/owner/repo/pull/1"); err == nil || !strings.Contains(err.Error(), "GitHub provider not available: delivery capability is") {
		t.Fatalf("fetchGitHubProviderSnapshot error = %v, want the capability refusal", err)
	}
}
