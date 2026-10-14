//go:build integration

package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestQueryDeliveryMergeStatus_RefusesNilIdentity(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(githubForgeStep, nil)
	if err == nil || !strings.Contains(err.Error(), "delivery identity is nil") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want nil-identity refusal", err)
	}
}

func TestQueryDeliveryMergeStatus_RefusesUnknownProvider(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(githubForgeStep, &domain.DeliveryIdentity{Provider: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported delivery provider") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want unknown-provider refusal", err)
	}
}

func TestFetchProviderSnapshotForProviderRefusesUnknownProvider(t *testing.T) {
	_, err := fetchProviderSnapshotForProvider(githubForgeStep, "unknown", "https://example.invalid")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("fetchProviderSnapshotForProvider error = %v, want unknown-provider refusal", err)
	}
}

func TestPRMergeStatus_JSONUnmarshal(t *testing.T) {
	// Test the domain.PRMergeStatus can be unmarshaled from gh CLI output,
	// including the headRefOid and mergedSha field tags.
	input := `{"state":"MERGED","merged":true,"headRefOid":"abc123def456","mergedSha":"fedcba654321"}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "MERGED" {
		t.Errorf("expected MERGED, got %s", status.State)
	}
	if !status.Merged {
		t.Error("expected merged=true")
	}
	if status.HeadSHA != "abc123def456" {
		t.Errorf("expected abc123def456, got %s", status.HeadSHA)
	}
	if status.MergedSHA != "fedcba654321" {
		t.Errorf("expected mergedSha fedcba654321, got %s", status.MergedSHA)
	}
}

func TestPRMergeStatus_MergedRequiresValidMergeCommit(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef01234567"
	cases := []string{
		`{"state":"MERGED","headRefOid":"head123"}`,
		`{"state":"MERGED","headRefOid":"head123","mergeCommit":null}`,
		`{"state":"MERGED","headRefOid":"head123","mergeCommit":{"oid":"not-a-git-object-id"}}`,
		`{"state":"MERGED","headRefOid":"head123","mergeCommit":{"oid":"0000000000000000000000000000000000000000"}}`,
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if _, err := parsePRMergeStatus([]byte(input)); err == nil {
				t.Fatal("parsePRMergeStatus accepted invalid merged evidence")
			}
		})
	}
	status, err := parsePRMergeStatus([]byte(fmt.Sprintf(`{"state":"MERGED","headRefOid":"head123","mergeCommit":{"oid":"%s"}}`, valid)))
	if err != nil || status.MergedSHA != valid || !status.Merged {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestPRMergeStatus_Closed(t *testing.T) {
	input := `{"state":"CLOSED","merged":false,"headRefOid":"def456abc123","mergedSha":""}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "CLOSED" {
		t.Errorf("expected CLOSED, got %s", status.State)
	}
	if status.Merged {
		t.Error("expected merged=false")
	}
}

func TestPRMergeStatus_Open(t *testing.T) {
	input := `{"state":"OPEN","merged":false,"headRefOid":"xyz789","mergedSha":""}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "OPEN" {
		t.Errorf("expected OPEN, got %s", status.State)
	}
	if status.Merged {
		t.Error("expected merged=false")
	}
}

func TestProviderSnapshotMergeableDelegatesToDomain(t *testing.T) {
	base := ProviderSnapshot{
		State:   "OPEN",
		Checks:  []domain.CheckRun{{Status: domain.CheckPassed}},
		Reviews: []domain.Review{{State: domain.ReviewState("approved")}},
	}
	cases := []struct {
		name   string
		mutate func(*ProviderSnapshot)
		want   bool
	}{
		{name: "open passed approved", want: true},
		{name: "closed", mutate: func(s *ProviderSnapshot) { s.State = "CLOSED" }},
		{name: "merged", mutate: func(s *ProviderSnapshot) { s.State = "MERGED" }},
		{name: "pending check", mutate: func(s *ProviderSnapshot) { s.Checks[0].Status = domain.CheckPending }},
		{name: "failed check", mutate: func(s *ProviderSnapshot) { s.Checks[0].Status = domain.CheckFailed }},
		{name: "no provider review needed", mutate: func(s *ProviderSnapshot) { s.Reviews = nil }, want: true},
		{name: "changes requested", mutate: func(s *ProviderSnapshot) { s.Reviews[0].State = domain.ReviewChangesRequested }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			snapshot.Checks = append([]domain.CheckRun(nil), base.Checks...)
			snapshot.Reviews = append([]domain.Review(nil), base.Reviews...)
			if tc.mutate != nil {
				tc.mutate(&snapshot)
			}
			if got := snapshot.Mergeable(); got != tc.want {
				t.Fatalf("Mergeable() = %t, want %t for %+v", got, tc.want, snapshot)
			}
		})
	}
}

const githubSnapshotPR = "https://github.com/owner/project/pull/42"

// githubOpenView is an OPEN, mergeable PR view with the given review decision.
func githubOpenView(reviewDecision string) string {
	return `{"state":"OPEN","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"` + reviewDecision + `"}`
}

func TestGitHubProviderSnapshotRefusesUnknownState(t *testing.T) {
	installFakeGH(t, ghReply{match: "pr view", stdout: `{"state":"DRAFT","headRefOid":"head123","headRefName":"feature","baseRefName":"main"}`})
	if _, err := fetchGitHubProviderSnapshot(githubForgeStep, githubSnapshotPR); err == nil || !strings.Contains(err.Error(), "unrecognized state") {
		t.Fatalf("fetchGitHubProviderSnapshot error = %v, want unknown-state refusal", err)
	}
}

func TestGitHubProviderSnapshotRefusesMissingMergeCommitEvidence(t *testing.T) {
	installFakeGH(t, ghReply{match: "pr view", stdout: `{"state":"MERGED","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeCommit":null}`})
	if _, err := fetchGitHubProviderSnapshot(githubForgeStep, githubSnapshotPR); err == nil || !strings.Contains(err.Error(), "missing merge commit OID") {
		t.Fatalf("fetchGitHubProviderSnapshot error = %v, want missing-merge-commit refusal", err)
	}
}

func TestGitHubProviderSnapshotTerminalStatesNeedNoMergeabilityEvidence(t *testing.T) {
	for _, tc := range []struct {
		state    string
		merged   bool
		mergeSHA string
		data     string
	}{
		{"MERGED", true, "0123456789abcdef0123456789abcdef01234567", `{"state":"MERGED","headRefOid":"head123","headRefName":"feature","baseRefName":"main","mergeCommit":{"oid":"0123456789abcdef0123456789abcdef01234567"}}`},
		{"CLOSED", false, "", `{"state":"CLOSED","headRefOid":"head123","headRefName":"feature","baseRefName":"main"}`},
	} {
		t.Run(tc.state, func(t *testing.T) {
			gh := installFakeGH(t, ghReply{match: "pr view", stdout: tc.data})
			snapshot, err := fetchGitHubProviderSnapshot(githubForgeStep, githubSnapshotPR)
			if err != nil {
				t.Fatalf("fetchGitHubProviderSnapshot: %v", err)
			}
			if snapshot.State != tc.state || snapshot.Merged != tc.merged || snapshot.HeadSHA != "head123" || snapshot.MergedSHA != tc.mergeSHA {
				t.Fatalf("snapshot = %+v", snapshot)
			}
			if calls := gh.calls(t); len(calls) != 2 || calls[0] != "auth status" {
				t.Fatalf("gh calls = %q, want the auth probe then the PR view and no mergeability read", calls)
			}
		})
	}
}

func TestGitLabProviderSnapshotRefusesMissingMergeCommitEvidence(t *testing.T) {
	old := glabRunnerFor
	glabRunnerFor = fixedGlabRunner(&fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("glab version 1.45.0"), nil
		}
		if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
			return []byte("api access"), nil
		}
		if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
			return []byte("authenticated"), nil
		}
		return []byte(`{"state":"merged","sha":"head123","source_branch":"feature","target_branch":"main","merge_commit_sha":null}`), nil
	}})
	t.Cleanup(func() { glabRunnerFor = old })

	if _, err := fetchGitLabProviderSnapshot(gitlabForgeStep, "https://gitlab.com/owner/project/-/merge_requests/42"); err == nil || !strings.Contains(err.Error(), "missing merge commit sha") {
		t.Fatalf("fetchGitLabProviderSnapshot error = %v, want missing-merge-commit refusal", err)
	}
}

func TestGitLabProviderSnapshotTerminalStatesNeedNoMergeabilityEvidence(t *testing.T) {
	old := glabRunnerFor
	glabRunnerFor = fixedGlabRunner(&fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
		if len(args) >= 1 && args[0] == "--version" {
			return []byte("glab version 1.45.0"), nil
		}
		if len(args) >= 2 && args[0] == "api" && args[1] == "--help" {
			return []byte("api access"), nil
		}
		if len(args) >= 2 && args[0] == "auth" {
			return []byte("authenticated"), nil
		}
		return []byte(`{"state":"merged","sha":"head123","source_branch":"feature","target_branch":"main","merge_commit_sha":"0123456789abcdef0123456789abcdef01234567"}`), nil
	}})
	t.Cleanup(func() { glabRunnerFor = old })
	for _, state := range []string{"merged", "closed"} {
		t.Run(state, func(t *testing.T) {
			glabRunnerFor = fixedGlabRunner(&fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
				if len(args) >= 1 && args[0] == "--version" {
					return []byte("glab version 1.45.0"), nil
				}
				if len(args) >= 2 && args[0] == "api" && args[1] == "--help" {
					return []byte("api access"), nil
				}
				if len(args) >= 2 && args[0] == "auth" {
					return []byte("authenticated"), nil
				}
				return []byte(fmt.Sprintf(`{"state":"%s","sha":"head123","source_branch":"feature","target_branch":"main","merge_commit_sha":"0123456789abcdef0123456789abcdef01234567"}`, state)), nil
			}})
			snapshot, err := fetchGitLabProviderSnapshot(gitlabForgeStep, "https://gitlab.com/owner/project/-/merge_requests/42")
			if err != nil {
				t.Fatalf("fetchGitLabProviderSnapshot: %v", err)
			}
			if snapshot.State != strings.ToUpper(state) || snapshot.Merged != (state == "merged") {
				t.Fatalf("snapshot = %+v", snapshot)
			}
		})
	}
}

func TestGitLabProviderSnapshotRefusesUnknownState(t *testing.T) {
	old := glabRunnerFor
	glabRunnerFor = fixedGlabRunner(&fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("glab version 1.45.0"), nil
		}
		if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
			return []byte("api access"), nil
		}
		if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
			return []byte("authenticated"), nil
		}
		return []byte(`{"state":"draft","sha":"head123","source_branch":"feature","target_branch":"main"}`), nil
	}})
	t.Cleanup(func() { glabRunnerFor = old })

	if _, err := fetchGitLabProviderSnapshot(gitlabForgeStep, "https://gitlab.com/owner/project/-/merge_requests/42"); err == nil || !strings.Contains(err.Error(), "unrecognized state") {
		t.Fatalf("fetchGitLabProviderSnapshot error = %v, want unknown-state refusal", err)
	}
}

func mergeabilityRunner(json, reviewers string) *fakeGlabRunner {
	return &fakeGlabRunner{runFn: func(args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("glab version 1.45.0"), nil
		}
		if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
			return []byte("api access\n"), nil
		}
		if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
			return []byte("authenticated to gitlab.com\n"), nil
		}
		if len(args) >= 2 && args[0] == "api" && strings.HasSuffix(args[1], "/approvals") {
			return []byte(`{"approved":true,"approved_by":[{"user":{"username":"reviewer"}}]}`), nil
		}
		if len(args) >= 2 && args[0] == "api" && strings.HasSuffix(args[1], "/reviewers") {
			return []byte(reviewers), nil
		}
		return []byte(json), nil
	}}
}

// TestClassifyLanding pins the one "delivery landed" rule: merged needs merge
// commit evidence and the exact identity head, each refused row beside the
// nearest row it allows.
func TestClassifyLanding(t *testing.T) {
	head := strings.Repeat("a", 40)
	merge := strings.Repeat("b", 40)
	ident := domain.DeliveryIdentity{Number: 9, HeadSHA: head}
	cases := []struct {
		name    string
		status  domain.PRMergeStatus
		want    landing
		wantErr string
	}{
		{"merged at the identity head lands", domain.PRMergeStatus{State: "MERGED", Merged: true, HeadSHA: head, MergedSHA: merge}, landingLanded, ""},
		{"merged with no head is unknown", domain.PRMergeStatus{State: "MERGED", Merged: true, MergedSHA: merge}, landingUnknown, "empty head SHA"},
		{"merged with no merge commit is unknown", domain.PRMergeStatus{State: "MERGED", Merged: true, HeadSHA: head, MergedSHA: "abc123"}, landingUnknown, "no merge-result evidence"},
		{"merged at another head is unknown", domain.PRMergeStatus{State: "MERGED", Merged: true, HeadSHA: strings.Repeat("c", 40), MergedSHA: merge}, landingUnknown, "PR head SHA mismatch"},
		{"open is open", domain.PRMergeStatus{State: "OPEN", HeadSHA: head}, landingOpen, ""},
		{"closed is closed", domain.PRMergeStatus{State: "CLOSED", Closed: true, HeadSHA: head}, landingClosed, ""},
		{"an unknown state is unknown", domain.PRMergeStatus{State: "LOCKED"}, landingUnknown, "unexpected state \"LOCKED\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyLanding(tc.status, ident)
			if got != tc.want {
				t.Fatalf("landing = %v (%v), want %v", got, err, tc.want)
			}
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
