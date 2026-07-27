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
