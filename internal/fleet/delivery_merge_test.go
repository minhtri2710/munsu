//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

var errSnapshotUnmergeable = errors.New("snapshot fetched but domain.PR.CanMerge refuses it")

// githubMergeSnapshot scripts a fake gh and returns the fetched snapshot's
// verdict: the fetch error, or errSnapshotUnmergeable for a snapshot the
// domain acceptance rule refuses.
func githubMergeSnapshot(t *testing.T, replies ...ghReply) error {
	t.Helper()
	installFakeGH(t, replies...)
	snapshot, err := fetchGitHubProviderSnapshot(githubForgeStep, githubSnapshotPR)
	if err != nil {
		return err
	}
	if !snapshot.Mergeable() {
		return errSnapshotUnmergeable
	}
	if len(snapshot.Checks) != 1 || snapshot.Checks[0].Name != "ci" || snapshot.Checks[0].Status != domain.CheckPassed {
		return fmt.Errorf("checks = %+v, want the one passed ci check", snapshot.Checks)
	}
	return nil
}

func gitlabMergeSnapshot(t *testing.T, mrJSON, reviewers string) error {
	t.Helper()
	old := glabRunnerFor
	glabRunnerFor = fixedGlabRunner(mergeabilityRunner(mrJSON, reviewers))
	t.Cleanup(func() { glabRunnerFor = old })
	snapshot, err := fetchGitLabProviderSnapshot(gitlabForgeStep, "https://gitlab.com/owner/project/-/merge_requests/42")
	if err != nil {
		return err
	}
	if !snapshot.Mergeable() {
		return errSnapshotUnmergeable
	}
	return nil
}

// mergePinAcross runs the head/base pin of both providers over one request:
// ValidateMergeRequest must agree with Merge, and a refused Merge must not
// reach the provider client.
func mergePinAcross(t *testing.T, ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	t.Helper()
	ghClient := &fakeGitHubDelivery{}
	glabCalled := false
	glClient := &glabClient{runner: &fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
		glabCalled = true
		return nil, nil
	}}}
	ghIdent := githubDeliveryIdentity()
	ghIdent.HeadSHA, ghIdent.BaseRef = ident.HeadSHA, ident.BaseRef
	glIdent := ident
	glIdent.URL = "https://gitlab.com/owner/project/-/merge_requests/7"
	var refused, accepted []error
	for _, p := range []struct {
		provider DeliveryProvider
		ident    domain.DeliveryIdentity
	}{
		{&githubDeliveryProvider{client: ghClient}, ghIdent},
		{&gitlabDeliveryProvider{client: glClient}, glIdent},
	} {
		validateErr := p.provider.ValidateMergeRequest(p.ident, request)
		mergeErr := p.provider.Merge(p.ident, request)
		if validateErr != nil {
			if mergeErr == nil {
				return errors.New("Merge accepted a request ValidateMergeRequest refused")
			}
			refused = append(refused, validateErr)
			continue
		}
		accepted = append(accepted, mergeErr)
	}
	if len(refused) == 0 {
		return errors.Join(accepted...)
	}
	if len(refused) != 2 {
		return errors.New("the providers disagree on the request")
	}
	if ghClient.merged != nil || glabCalled {
		return errors.New("a refused merge reached the provider client")
	}
	return refused[0]
}

// TestMergeRefusals is the table of the provider merge refusals: a block row
// proves one refusal names its own reason, and the allow row beside it is the
// nearest valid case.
func TestMergeRefusals(t *testing.T) {
	noneRequired := []ghReply{
		{match: "protection/required_status_checks", stderr: "Branch not protected", exit: 1},
		{match: "rules/branches", stdout: `[]`},
	}
	passingCI := func(decision string) []ghReply {
		return []ghReply{
			{match: "pr view", stdout: githubOpenView(decision)},
			{match: "protection/required_status_checks", stdout: `{"contexts":["ci"]}`},
			{match: "rules/branches", stdout: `[]`},
			{match: "check-runs", stdout: `[{"check_runs":[{"id":1,"name":"ci","status":"completed","conclusion":"success"}]}]`},
			{match: "statuses", stdout: `[[]]`},
		}
	}
	const gitlabMR = `{"state":"opened","sha":"abc123","source_branch":"feature","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"status":"success","sha":"abc123"}}`
	head, base := sampleSHA, "main"
	pinned := domain.DeliveryIdentity{HeadSHA: head, BaseRef: base}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T) error
		want error  // the sentinel the refusal carries, when it has one
		msg  string // otherwise the refusal's message; both empty means accepted
	}{
		{
			name: "github: a conflicting PR blocks",
			run: func(t *testing.T) error {
				return githubMergeSnapshot(t, ghReply{match: "pr view", stdout: `{"state":"OPEN","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeable":"CONFLICTING","mergeStateStatus":"CLEAN"}`})
			},
			msg: "GitHub PR is not mergeable",
		},
		{
			name: "github: a branch behind its base blocks",
			run: func(t *testing.T) error {
				return githubMergeSnapshot(t, ghReply{match: "pr view", stdout: `{"state":"OPEN","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"BEHIND"}`})
			},
			msg: `mergeStateStatus "BEHIND"`,
		},
		{
			name: "github: an absent merge state blocks",
			run: func(t *testing.T) error {
				return githubMergeSnapshot(t, ghReply{match: "pr view", stdout: `{"state":"OPEN","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeable":"MERGEABLE"}`})
			},
			msg: `mergeStateStatus ""`,
		},
		{
			name: "github: a head with no reported check blocks",
			run: func(t *testing.T) error {
				return githubMergeSnapshot(t, append(append([]ghReply{{match: "pr view", stdout: githubOpenView("")}}, noneRequired...),
					ghReply{match: "check-runs", stdout: `[{"check_runs":[]}]`}, ghReply{match: "statuses", stdout: `[[]]`})...)
			},
			msg: "GitHub reported no check for the head",
		},
		{
			name: "github: a changes-requested decision blocks",
			run:  func(t *testing.T) error { return githubMergeSnapshot(t, passingCI("CHANGES_REQUESTED")...) },
			want: errSnapshotUnmergeable,
		},
		{
			name: "github: a passing required check and an approved decision reads mergeable",
			run:  func(t *testing.T) error { return githubMergeSnapshot(t, passingCI("APPROVED")...) },
		},
		{
			name: "github: no check reported blocks the evaluation",
			run:  func(*testing.T) error { _, err := evaluateGitHubChecks(nil, nil); return err },
			msg:  "GitHub reported no check for the head",
		},
		{
			name: "github: a required check that never reported blocks the evaluation",
			run: func(*testing.T) error {
				_, err := evaluateGitHubChecks([]string{"ci", "lint"}, []GitHubCheck{{Source: GitHubCheckRun, ID: 1, Name: "ci", Status: "completed", Conclusion: "success"}})
				return err
			},
			msg: `required check "lint" never reported`,
		},
		{
			name: "github: only skipped optional checks block the evaluation",
			run: func(*testing.T) error {
				_, err := evaluateGitHubChecks(nil, []GitHubCheck{
					{Source: GitHubCheckRun, ID: 1, Name: "docs", Status: "completed", Conclusion: "skipped"},
					{Source: GitHubCheckRun, ID: 2, Name: "fmt", Status: "completed", Conclusion: "neutral"},
				})
				return err
			},
			msg: "only skipped optional checks",
		},
		{
			name: "github: a required check reported only as a commit status evaluates",
			run: func(*testing.T) error {
				_, err := evaluateGitHubChecks([]string{"legacy"}, []GitHubCheck{
					{Source: GitHubCheckRun, ID: 1, Name: "ci", Status: "completed", Conclusion: "success"},
					{Source: GitHubCommitStatus, ID: 2, Name: "legacy", Status: "completed", Conclusion: "success"},
				})
				return err
			},
		},
		{
			name: "gitlab: missing mergeability evidence blocks",
			run: func(t *testing.T) error {
				return gitlabMergeSnapshot(t, `{"state":"opened","sha":"abc123","source_branch":"feature","target_branch":"main"}`, `[]`)
			},
			msg: "not mergeable",
		},
		{
			name: "gitlab: a pipeline of another sha blocks",
			run: func(t *testing.T) error {
				return gitlabMergeSnapshot(t, strings.Replace(gitlabMR, `"sha":"abc123"}}`, `"sha":"old456"}}`, 1), `[]`)
			},
			msg: "pipeline SHA evidence",
		},
		{
			name: "gitlab: a requested_changes reviewer blocks",
			run:  func(t *testing.T) error { return gitlabMergeSnapshot(t, gitlabMR, `[{"state":"requested_changes"}]`) },
			want: errSnapshotUnmergeable,
		},
		{
			name: "gitlab: the nested pipeline and approval evidence reads mergeable",
			run:  func(t *testing.T) error { return gitlabMergeSnapshot(t, gitlabMR, `[]`) },
		},
		{
			name: "merge pin: a head the identity does not pin blocks both providers",
			run: func(t *testing.T) error {
				return mergePinAcross(t, pinned, DeliveryMergeRequest{Method: "merge", HeadSHA: sampleSHA[:len(sampleSHA)-1] + "0", BaseRef: base})
			},
			msg: "merge constraints do not match the delivery identity",
		},
		{
			name: "merge pin: a base the identity does not pin blocks both providers",
			run: func(t *testing.T) error {
				return mergePinAcross(t, pinned, DeliveryMergeRequest{Method: "merge", HeadSHA: head, BaseRef: "release"})
			},
			msg: "merge constraints do not match the delivery identity",
		},
		{
			name: "merge pin: an empty head blocks both providers even when the identity has none",
			run: func(t *testing.T) error {
				return mergePinAcross(t, domain.DeliveryIdentity{BaseRef: base}, DeliveryMergeRequest{Method: "merge", BaseRef: base})
			},
			msg: "merge constraints do not match the delivery identity",
		},
		{
			name: "merge pin: an empty base blocks both providers even when the identity has none",
			run: func(t *testing.T) error {
				return mergePinAcross(t, domain.DeliveryIdentity{HeadSHA: head}, DeliveryMergeRequest{Method: "merge", HeadSHA: head})
			},
			msg: "merge constraints do not match the delivery identity",
		},
		{
			name: "merge pin: the exact head and base merge on both providers",
			run: func(t *testing.T) error {
				return mergePinAcross(t, pinned, DeliveryMergeRequest{Method: "merge", HeadSHA: head, BaseRef: base})
			},
		},
		{
			name: "gitlab: a rebase merge method blocks",
			run: func(*testing.T) error {
				return (&gitlabDeliveryProvider{}).ValidateMergeRequest(pinned, DeliveryMergeRequest{Method: "rebase", HeadSHA: head, BaseRef: base})
			},
			msg: "GitLab merge method \"rebase\" is unsupported",
		},
		{
			name: "gitlab: a squash merge method validates",
			run: func(*testing.T) error {
				return (&gitlabDeliveryProvider{}).ValidateMergeRequest(pinned, DeliveryMergeRequest{Method: "squash", HeadSHA: head, BaseRef: base})
			},
		},
		{
			name: "provider mergeability: a denied observation blocks",
			run: func(*testing.T) error {
				return verifyProviderMergeability(DeliveryProviderObservation{State: "OPEN", Mergeability: DeliveryMergeabilityDenied})
			},
			msg: "provider reports delivery is not mergeable",
		},
		{
			name: "provider mergeability: an allowed observation passes",
			run: func(*testing.T) error {
				return verifyProviderMergeability(DeliveryProviderObservation{State: "OPEN", Mergeability: DeliveryMergeabilityAllowed})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			switch {
			case tc.want != nil:
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			case tc.msg != "":
				if err == nil || !strings.Contains(err.Error(), tc.msg) {
					t.Fatalf("err = %v, want %q", err, tc.msg)
				}
			case err != nil:
				t.Fatalf("err = %v, want accepted", err)
			}
		})
	}
}

// TestDeliverRefusesProviderDriftBeforeTheMerge pins that Deliver's last fence
// before the irreversible merge fails closed on a head or base ref that drifted
// from the pinned identity, and delivers when they match. The merge lands in the
// PR's current base, so a base changed inside the capture-to-merge window would
// land on a branch that was never authorized.
func TestDeliverRefusesProviderDriftBeforeTheMerge(t *testing.T) {
	pinned := deliveryTestIdentity()
	for _, tc := range []struct {
		name    string
		obs     DeliveryProviderObservation
		wantErr string
	}{
		{"head drift blocks", DeliveryProviderObservation{State: "OPEN", HeadSHA: "9999888877776666555544443333222211110000", BaseRef: pinned.BaseRef, Mergeability: DeliveryMergeabilityAllowed}, "provider head changed since capture"},
		{"base ref drift blocks", DeliveryProviderObservation{State: "OPEN", HeadSHA: pinned.HeadSHA, BaseRef: "release/1.0", Mergeability: DeliveryMergeabilityAllowed}, "provider base ref changed since capture"},
		{"a matching head and base deliver", DeliveryProviderObservation{State: "OPEN", HeadSHA: pinned.HeadSHA, BaseRef: pinned.BaseRef, Mergeability: DeliveryMergeabilityAllowed}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID, deliveryTestGitHubForge)
			provider := newFakeDeliveryProvider().script(tc.obs)
			if tc.wantErr == "" {
				provider.script(DeliveryProviderObservation{State: "MERGED", HeadSHA: pinned.HeadSHA, BaseRef: pinned.BaseRef, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
			}
			installDeliveryProviderFor(t, provider)

			result, err := Deliver(homeDir, taskID, deliverRequest())
			if tc.wantErr == "" {
				if err != nil || provider.merges != 1 || result.Status != "completed" {
					t.Fatalf("Deliver = %+v, %v with %d merges, want a completed delivery with one merge", result, err, provider.merges)
				}
				return
			}
			var failClosed *DeliveryFailClosedError
			if !errors.As(err, &failClosed) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Deliver err = %T %v, want *DeliveryFailClosedError carrying %q", err, err, tc.wantErr)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0 (the irreversible mutation must not run)", provider.merges)
			}
			if out, oerr := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); oerr == nil {
				t.Fatalf("fail-closed path committed a terminal outcome: %+v", out)
			}
		})
	}
}
