package domain_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestPRCanMerge(t *testing.T) {
	commented := []domain.Review{{State: domain.ReviewState("commented"), Body: "LGTM"}}
	passed := []domain.CheckRun{{Name: "test", Status: domain.CheckPassed}, {Name: "lint", Status: domain.CheckPassed}}
	cases := []struct {
		name    string
		status  domain.PRStatus
		checks  []domain.CheckRun
		reviews []domain.Review
		want    bool
	}{
		{name: "open passed no reviews", status: domain.PROpen, checks: passed, want: true},
		{name: "open passed non-objecting review", status: domain.PROpen, checks: passed, reviews: commented, want: true},
		{name: "closed", status: domain.PRClosed, checks: passed, reviews: commented},
		{name: "merged", status: domain.PRMerged, checks: passed, reviews: commented},
		{name: "pending check", status: domain.PROpen, checks: []domain.CheckRun{{Status: domain.CheckPending}}, reviews: commented},
		{name: "failed check", status: domain.PROpen, checks: []domain.CheckRun{{Status: domain.CheckFailed}}, reviews: commented},
		{name: "failed check after passed checks", status: domain.PROpen, checks: append(append([]domain.CheckRun{}, passed...), domain.CheckRun{Name: "security", Status: domain.CheckFailed}), reviews: commented},
		{name: "changes requested", status: domain.PROpen, checks: passed, reviews: []domain.Review{{State: domain.ReviewChangesRequested}}},
		{name: "changes requested after another review", status: domain.PROpen, checks: passed, reviews: append(append([]domain.Review{}, commented...), domain.Review{State: domain.ReviewChangesRequested})},
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

func TestToMetaEmitsExactlyMetaKeys(t *testing.T) {
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
	got := slices.Sorted(maps.Keys(id.ToMeta()))
	want := slices.Sorted(slices.Values(id.MetaKeys()))
	if !slices.Equal(got, want) {
		t.Fatalf("ToMeta keys = %v, want MetaKeys %v", got, want)
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
