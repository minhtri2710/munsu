//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

func TestQueryGLMergeStatusForStateRefusesUnknownState(t *testing.T) {
	_, err := queryGLMergeStatusForState(backend.State(99), &domain.DeliveryIdentity{})
	if err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("queryGLMergeStatusForState error = %v, want unknown-state refusal", err)
	}
}

func TestQueryDeliveryMergeStatus_RefusesNilIdentity(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(nil)
	if err == nil || !strings.Contains(err.Error(), "delivery identity is nil") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want nil-identity refusal", err)
	}
}

func TestQueryDeliveryMergeStatus_RefusesUnknownProvider(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(&domain.DeliveryIdentity{Provider: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want unknown-provider refusal", err)
	}
}

func TestFetchProviderSnapshotForProviderRefusesUnknownProvider(t *testing.T) {
	_, err := fetchProviderSnapshotForProvider("unknown", "https://example.invalid")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("fetchProviderSnapshotForProvider error = %v, want unknown-provider refusal", err)
	}
}

func TestPRMergeStatus_JSONUnmarshal(t *testing.T) {
	// Test the domain.PRMergeStatus can be unmarshaled from gh CLI output
	input := `{"state":"MERGED","merged":true,"headRefOid":"abc123def456","mergedSha":"abc123def456"}`
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
	if status.MergedSHA != "abc123def456" {
		t.Errorf("expected mergedSha abc123def456, got %s", status.MergedSHA)
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

func TestProviderSnapshotMergeableRequiresCompleteApprovalEvidence(t *testing.T) {
	base := ProviderSnapshot{
		State:   "OPEN",
		Checks:  []domain.CheckRun{{Status: domain.CheckPassed}},
		Reviews: []domain.Review{{State: domain.ReviewApproved}},
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
		{name: "no approval", mutate: func(s *ProviderSnapshot) { s.Reviews = nil }},
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

func TestNormalizeGitHubReviewState(t *testing.T) {
	cases := map[string]domain.ReviewState{
		"APPROVED":          domain.ReviewApproved,
		"CHANGES_REQUESTED": domain.ReviewChangesRequested,
		"changes-requested": domain.ReviewChangesRequested,
		"DISMISSED":         domain.ReviewDismissed,
		"COMMENTED":         domain.ReviewPending,
	}
	for input, want := range cases {
		if got := normalizeGitHubReviewState(input); got != want {
			t.Errorf("normalizeGitHubReviewState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGitLabProviderSnapshotUsesNestedPipelineAndApprovalEvidence(t *testing.T) {
	old := defaultGlabRunner
	defaultGlabRunner = mergeabilityRunner(`{"state":"opened","sha":"abc123","source_branch":"feature","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"status":"success","sha":"abc123"}}`)
	t.Cleanup(func() { defaultGlabRunner = old })

	snapshot, err := fetchGitLabProviderSnapshot("https://gitlab.com/owner/project/-/merge_requests/42")
	if err != nil {
		t.Fatalf("fetchGitLabProviderSnapshot: %v", err)
	}
	if !snapshot.Mergeable() {
		t.Fatalf("snapshot = %+v, want mergeable", snapshot)
	}
}

func TestGitLabProviderSnapshotRefusesMissingMergeabilityEvidence(t *testing.T) {
	old := defaultGlabRunner
	defaultGlabRunner = mergeabilityRunner(`{"state":"opened","sha":"abc123","source_branch":"feature","target_branch":"main"}`)
	t.Cleanup(func() { defaultGlabRunner = old })

	if _, err := fetchGitLabProviderSnapshot("https://gitlab.com/owner/project/-/merge_requests/42"); err == nil || !strings.Contains(err.Error(), "pipeline evidence") {
		t.Fatalf("fetchGitLabProviderSnapshot error = %v, want missing-evidence refusal", err)
	}
}

func TestGitLabProviderSnapshotRefusesStalePipeline(t *testing.T) {
	old := defaultGlabRunner
	defaultGlabRunner = mergeabilityRunner(`{"state":"opened","sha":"abc123","source_branch":"feature","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"status":"success","sha":"old456"}}`)
	t.Cleanup(func() { defaultGlabRunner = old })

	if _, err := fetchGitLabProviderSnapshot("https://gitlab.com/owner/project/-/merge_requests/42"); err == nil || !strings.Contains(err.Error(), "pipeline evidence") {
		t.Fatalf("fetchGitLabProviderSnapshot error = %v, want stale-pipeline refusal", err)
	}
}

func mergeabilityRunner(json string) *fakeGlabRunner {
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
		return []byte(json), nil
	}}
}

func TestPRMergeStatus_FieldTags(t *testing.T) {
	// Verify the JSON field tags match gh CLI output format
	var status domain.PRMergeStatus
	input := `{"state":"MERGED","merged":true,"headRefOid":"abc","mergedSha":"def"}`
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.HeadSHA != "abc" {
		t.Errorf("expected headRefOid to map to HeadSHA, got %s", status.HeadSHA)
	}
	if status.MergedSHA != "def" {
		t.Errorf("expected mergedSha to map to MergedSHA, got %s", status.MergedSHA)
	}
}
