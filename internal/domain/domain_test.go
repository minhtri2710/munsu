package domain_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestPRCanMerge(t *testing.T) {
	pr := domain.PR{
		Number:     101,
		Title:      "Feature PR",
		Status:     domain.PROpen,
		BaseBranch: "main",
		HeadBranch: "mu/feature-101",
		Checks: []domain.CheckRun{
			{Name: "test", Status: domain.CheckPassed},
			{Name: "lint", Status: domain.CheckPassed},
		},
		Reviews: []domain.Review{{State: domain.ReviewApproved, Body: "LGTM"}},
	}

	if !pr.CanMerge() {
		t.Error("expected PR.CanMerge() to be true for open PR with green checks and approval")
	}
	pr.Checks = append(pr.Checks, domain.CheckRun{Name: "security", Status: domain.CheckFailed})
	if pr.CanMerge() {
		t.Error("expected PR.CanMerge() to be false when a check fails")
	}
}

func TestValidateIdentity(t *testing.T) {
	identity := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     12,
		URL:        "https://github.com/minhtri2710/munsu/pull/12",
		BaseRef:    "main",
		HeadRef:    "mu/task-12",
		HeadSHA:    "abc1234567890",
		CapturedAt: "2026-07-26T12:00:00Z",
	}
	if err := domain.ValidateIdentity(identity); err != nil {
		t.Errorf("expected valid identity, got: %v", err)
	}
	identity.Number = -1
	if err := domain.ValidateIdentity(identity); err == nil {
		t.Error("expected error for invalid PR number")
	}
}

// --- IssueLink tests ---

func TestValidateIssueLink_Valid(t *testing.T) {
	link := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
	}
	if err := domain.ValidateIssueLink(link); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateIssueLink_Nil(t *testing.T) {
	if err := domain.ValidateIssueLink(nil); err == nil {
		t.Fatal("expected error for nil")
	}
}

func TestValidateIssueLink_MissingURL(t *testing.T) {
	link := &domain.IssueLink{Number: 42, Relation: domain.IssueLinkImplementation, ClosurePolicy: domain.ClosurePolicyAuto}
	if err := domain.ValidateIssueLink(link); err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestValidateIssueLink_InvalidRelation(t *testing.T) {
	link := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Number:        42,
		Relation:      "invalid",
		ClosurePolicy: domain.ClosurePolicyAuto,
	}
	if err := domain.ValidateIssueLink(link); err == nil {
		t.Fatal("expected error for invalid relation")
	}
}

func TestValidateIssueLink_InvalidPolicy(t *testing.T) {
	link := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: "invalid",
	}
	if err := domain.ValidateIssueLink(link); err == nil {
		t.Fatal("expected error for invalid policy")
	}
}

func TestDefaultClosurePolicy(t *testing.T) {
	checks := []struct {
		relation domain.IssueLinkRelation
		want     domain.IssueLinkClosurePolicy
	}{
		{domain.IssueLinkImplementation, domain.ClosurePolicyAuto},
		{domain.IssueLinkRelated, domain.ClosurePolicyNever},
		{domain.IssueLinkParent, domain.ClosurePolicyNever},
		{"unknown", domain.ClosurePolicyManual},
	}
	for _, c := range checks {
		got := domain.DefaultClosurePolicy(c.relation)
		if got != c.want {
			t.Errorf("DefaultClosurePolicy(%q) = %q, want %q", c.relation, got, c.want)
		}
	}
}

func TestIssueLinkToMeta(t *testing.T) {
	link := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}
	meta := link.ToMeta(0)
	if meta["issue_link_0_url"] != "https://github.com/owner/repo/issues/42" {
		t.Errorf("url: got %q", meta["issue_link_0_url"])
	}
	if meta["issue_link_0_provider"] != "github" {
		t.Errorf("provider: got %q", meta["issue_link_0_provider"])
	}
	if meta["issue_link_0_owner"] != "owner" {
		t.Errorf("owner: got %q", meta["issue_link_0_owner"])
	}
	if meta["issue_link_0_repo"] != "repo" {
		t.Errorf("repo: got %q", meta["issue_link_0_repo"])
	}
	if meta["issue_link_0_number"] != "42" {
		t.Errorf("number: got %q", meta["issue_link_0_number"])
	}
	if meta["issue_link_0_relation"] != "implementation" {
		t.Errorf("relation: got %q", meta["issue_link_0_relation"])
	}
	if meta["issue_link_0_policy"] != "auto-close" {
		t.Errorf("policy: got %q", meta["issue_link_0_policy"])
	}
	if meta["issue_link_0_closing_ref"] != "owner/repo#42" {
		t.Errorf("closing_ref: got %q", meta["issue_link_0_closing_ref"])
	}
}

func TestIssueLinkRoundTrip(t *testing.T) {
	original := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}
	meta := original.ToMeta(0)
	restored := domain.IssueLinkFromMeta(meta, 0)
	if restored == nil {
		t.Fatal("IssueLinkFromMeta returned nil")
	}
	if restored.URL != original.URL {
		t.Errorf("URL: got %q, want %q", restored.URL, original.URL)
	}
	if restored.Provider != "github" {
		t.Errorf("Provider: got %q, want %q", restored.Provider, "github")
	}
	if restored.Number != 42 {
		t.Errorf("Number: got %d, want 42", restored.Number)
	}
	if restored.Relation != original.Relation {
		t.Errorf("Relation: got %q, want %q", restored.Relation, original.Relation)
	}
	if restored.ClosurePolicy != original.ClosurePolicy {
		t.Errorf("ClosurePolicy: got %q, want %q", restored.ClosurePolicy, original.ClosurePolicy)
	}
}

func TestIssueLinksFromMeta_Multiple(t *testing.T) {
	meta := map[string]string{
		"issue_link_0_url":      "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider": "github",
		"issue_link_0_owner":    "owner",
		"issue_link_0_repo":     "repo",
		"issue_link_0_number":   "42",
		"issue_link_0_relation": "implementation",
		"issue_link_0_policy":   "auto-close",
		"issue_link_1_url":      "https://github.com/owner/repo/issues/43",
		"issue_link_1_provider": "github",
		"issue_link_1_owner":    "owner",
		"issue_link_1_repo":     "repo",
		"issue_link_1_number":   "43",
		"issue_link_1_relation": "related",
		"issue_link_1_policy":   "never-close",
	}
	links := domain.IssueLinksFromMeta(meta)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
	if links[0].Number != 42 {
		t.Errorf("link[0].Number: got %d, want 42", links[0].Number)
	}
	if links[1].Number != 43 {
		t.Errorf("link[1].Number: got %d, want 43", links[1].Number)
	}
	if links[0].Provider != "github" {
		t.Errorf("link[0].Provider: got %q, want %q", links[0].Provider, "github")
	}
}

func TestIssueLinksFromMeta_Empty(t *testing.T) {
	links := domain.IssueLinksFromMeta(map[string]string{"kind": "ship"})
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}
}

func TestIssueLinkClosingReference(t *testing.T) {
	link := &domain.IssueLink{
		Owner:  "owner",
		Repo:   "repo",
		Number: 42,
	}
	if ref := link.ClosingReference(); ref != "owner/repo#42" {
		t.Errorf("ClosingReference: got %q, want %q", ref, "owner/repo#42")
	}
}

func TestIssueLinkClosingReference_Explicit(t *testing.T) {
	link := &domain.IssueLink{
		Owner:      "owner",
		Repo:       "repo",
		Number:     42,
		ClosingRef: "custom/ref#1",
	}
	if ref := link.ClosingReference(); ref != "custom/ref#1" {
		t.Errorf("ClosingReference: got %q, want %q", ref, "custom/ref#1")
	}
}

func TestIssueLinkClosingReference_Empty(t *testing.T) {
	link := &domain.IssueLink{}
	if ref := link.ClosingReference(); ref != "" {
		t.Errorf("ClosingReference: got %q, want empty", ref)
	}
}

func TestIssueLinkStoresProviderInfo(t *testing.T) {
	meta := map[string]string{
		"issue_link_0_url":      "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider": "github",
		"issue_link_0_owner":    "owner",
		"issue_link_0_repo":     "repo",
		"issue_link_0_number":   "42",
		"issue_link_0_relation": "implementation",
		"issue_link_0_policy":   "auto-close",
	}
	link := domain.IssueLinkFromMeta(meta, 0)
	if link == nil {
		t.Fatal("expected non-nil link")
	}
	if link.Provider != "github" {
		t.Errorf("Provider: got %q, want %q", link.Provider, "github")
	}
	if link.Owner != "owner" {
		t.Errorf("Owner: got %q, want %q", link.Owner, "owner")
	}
	if link.Repo != "repo" {
		t.Errorf("Repo: got %q, want %q", link.Repo, "repo")
	}
	if link.Number != 42 {
		t.Errorf("Number: got %d, want 42", link.Number)
	}
}
