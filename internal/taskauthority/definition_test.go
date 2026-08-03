package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// TestValidateIssueLinkDefinitionCommitsCleanLinks proves a valid
// implementation auto-close link plus parent/related never-close links pass
// Aggregate definition validation with matching provider evidence.
func TestValidateIssueLinkDefinitionCommitsCleanLinks(t *testing.T) {
	impl := domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}
	parent := domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/43",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        43,
		Relation:      domain.IssueLinkParent,
		ClosurePolicy: domain.ClosurePolicyNever,
	}
	agg := Aggregate{
		IssueLinks: []domain.IssueLink{impl, parent},
		IssueLinkReconciliation: []domain.IssueLinkReconciliationResult{
			{Link: impl, Status: domain.IssueLinkClosed},
			{Link: parent, Status: domain.IssueLinkManualPolicy},
		},
	}
	if err := validateIssueLinkDefinition(agg); err != nil {
		t.Fatalf("clean definition rejected: %v", err)
	}
}

// TestValidateIssueLinkDefinitionRejectsAutoClosePromotion proves parent and
// related links cannot be promoted to automatic closure policy.
func TestValidateIssueLinkDefinitionRejectsAutoClosePromotion(t *testing.T) {
	for _, relation := range []domain.IssueLinkRelation{domain.IssueLinkParent, domain.IssueLinkRelated} {
		link := domain.IssueLink{
			URL:           "https://github.com/owner/repo/issues/44",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        44,
			Relation:      relation,
			ClosurePolicy: domain.ClosurePolicyAuto,
		}
		agg := Aggregate{
			IssueLinks: []domain.IssueLink{link},
			IssueLinkReconciliation: []domain.IssueLinkReconciliationResult{
				{Link: link, Status: domain.IssueLinkOpen},
			},
		}
		if err := validateIssueLinkDefinition(agg); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("auto-close %s link error = %v, want ErrInvalidInput", relation, err)
		}
	}
}

// TestValidateIssueLinkDefinitionRejectsMismatchedEvidence proves the
// definition validator enforces the one-to-one correspondence between links
// and provider evidence.
func TestValidateIssueLinkDefinitionRejectsMismatchedEvidence(t *testing.T) {
	link := domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}
	if err := validateIssueLinkDefinition(Aggregate{
		IssueLinks: []domain.IssueLink{link},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing evidence error = %v, want ErrInvalidInput", err)
	}
	if err := validateIssueLinkDefinition(Aggregate{
		IssueLinks: []domain.IssueLink{link},
		IssueLinkReconciliation: []domain.IssueLinkReconciliationResult{
			{Link: link, Status: domain.IssueLinkClosed},
			{Link: link, Status: domain.IssueLinkManualPolicy},
		},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("extra evidence error = %v, want ErrInvalidInput", err)
	}
	if err := validateIssueLinkDefinition(Aggregate{
		IssueLinks: []domain.IssueLink{link},
		IssueLinkReconciliation: []domain.IssueLinkReconciliationResult{
			{Link: link, Status: "mystery"},
		},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown status error = %v, want ErrInvalidInput", err)
	}
}
