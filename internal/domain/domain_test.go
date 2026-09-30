package domain_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestPRCanMerge(t *testing.T) {
	approved := []domain.Review{{State: domain.ReviewApproved, Body: "LGTM"}}
	passed := []domain.CheckRun{{Name: "test", Status: domain.CheckPassed}, {Name: "lint", Status: domain.CheckPassed}}
	cases := []struct {
		name    string
		status  domain.PRStatus
		checks  []domain.CheckRun
		reviews []domain.Review
		want    bool
	}{
		{name: "open passed approved", status: domain.PROpen, checks: passed, reviews: approved, want: true},
		{name: "closed", status: domain.PRClosed, checks: passed, reviews: approved},
		{name: "merged", status: domain.PRMerged, checks: passed, reviews: approved},
		{name: "pending check", status: domain.PROpen, checks: []domain.CheckRun{{Status: domain.CheckPending}}, reviews: approved},
		{name: "failed check", status: domain.PROpen, checks: []domain.CheckRun{{Status: domain.CheckFailed}}, reviews: approved},
		{name: "failed check after passed checks", status: domain.PROpen, checks: append(append([]domain.CheckRun{}, passed...), domain.CheckRun{Name: "security", Status: domain.CheckFailed}), reviews: approved},
		{name: "no approval", status: domain.PROpen, checks: passed},
		{name: "changes requested", status: domain.PROpen, checks: passed, reviews: []domain.Review{{State: domain.ReviewChangesRequested}}},
		{name: "approval vetoed by changes requested", status: domain.PROpen, checks: passed, reviews: []domain.Review{{State: domain.ReviewApproved}, {State: domain.ReviewChangesRequested}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := domain.PR{Number: 101, Title: "Feature PR", Status: tc.status, BaseBranch: "main", HeadBranch: "mu/feature-101", Checks: tc.checks, Reviews: tc.reviews}
			if got := pr.CanMerge(); got != tc.want {
				t.Fatalf("CanMerge() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestLegacyPRMetaAliasesRemoved(t *testing.T) {
	legacyAliases := []string{"pr", "pr_base", "pr_head"}
	canonicalKeys := []string{
		"pr_provider", "pr_owner", "pr_repo",
		"pr_number", "pr_url",
		"pr_base_ref", "pr_head_ref", "pr_head_sha",
		"pr_timestamp",
	}

	// 1) ToMeta must emit only canonical keys, never the legacy aliases.
	id := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	meta := id.ToMeta()
	for _, k := range legacyAliases {
		if _, ok := meta[k]; ok {
			t.Errorf("ToMeta emitted legacy alias key %q; it must be removed", k)
		}
	}
	for _, k := range canonicalKeys {
		if _, ok := meta[k]; !ok {
			t.Errorf("ToMeta missing canonical key %q", k)
		}
	}

	// 2) MetaKeys must no longer advertise the legacy aliases.
	for _, k := range id.MetaKeys() {
		for _, legacy := range legacyAliases {
			if k == legacy {
				t.Errorf("MetaKeys still advertises legacy alias %q", legacy)
			}
		}
	}

	// 3) The hard cut on read: a dev .meta carrying ONLY the legacy aliases and
	// no canonical key must no longer resolve to a delivery identity. The
	// fallback/conflict-reconciliation read branches were deleted, so IdentityFromMeta
	// must treat legacy-only state as having no identity (nil, nil) and must not
	// synthesize pr_url/headSHA/baseRef from pr/pr_head/pr_base.
	legacyOnly := map[string]string{
		"pr":      "https://github.com/minhtri2710/munsu/pull/42",
		"pr_base": "main",
		"pr_head": "abc123def456abc123def456abc123def456abc1",
		"kind":    "ship",
	}
	resolved, err := domain.IdentityFromMeta(legacyOnly)
	if err != nil {
		t.Fatalf("IdentityFromMeta returned error on legacy-only meta: %v (legacy keys must be silently ignored, not reconciled)", err)
	}
	if resolved != nil {
		t.Fatalf("legacy-only meta resolved to identity %+v; legacy aliases pr/pr_base/pr_head must be dead on read", resolved)
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
