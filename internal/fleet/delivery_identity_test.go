//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// --- ValidateIdentity tests ---

func TestValidateIdentity_Valid(t *testing.T) {
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
	if err := domain.ValidateIdentity(id); err != nil {
		t.Errorf("expected valid identity, got: %v", err)
	}
}

func TestValidateIdentity_Nil(t *testing.T) {
	err := domain.ValidateIdentity(nil)
	if err == nil {
		t.Fatal("expected error for nil identity")
	}
	if !strings.Contains(err.Error(), "delivery identity is nil") {
		t.Errorf("expected 'is nil' error, got: %v", err)
	}
}

func TestValidateIdentity_MissingProvider(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Owner:   "minhtri2710",
		Repo:    "munsu",
		Number:  42,
		URL:     "https://github.com/minhtri2710/munsu/pull/42",
		HeadSHA: "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Errorf("expected provider error, got: %v", err)
	}
}

func TestValidateIdentity_MissingOwner(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider: "github",
		Repo:     "munsu",
		Number:   42,
		URL:      "https://github.com/minhtri2710/munsu/pull/42",
		HeadSHA:  "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "owner is required") {
		t.Errorf("expected owner error, got: %v", err)
	}
}

func TestValidateIdentity_MissingRepo(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "minhtri2710",
		Number:   42,
		URL:      "https://github.com/minhtri2710/munsu/pull/42",
		HeadSHA:  "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "repo is required") {
		t.Errorf("expected repo error, got: %v", err)
	}
}

func TestValidateIdentity_InvalidNumber(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "minhtri2710",
		Repo:     "munsu",
		Number:   0,
		URL:      "https://github.com/minhtri2710/munsu/pull/42",
		HeadSHA:  "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "PR number must be positive") {
		t.Errorf("expected positive number error, got: %v", err)
	}
}

func TestValidateIdentity_MissingURL(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "minhtri2710",
		Repo:     "munsu",
		Number:   42,
		HeadSHA:  "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "URL is required") {
		t.Errorf("expected URL error, got: %v", err)
	}
}

func TestValidateIdentity_MissingBaseRef(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		HeadRef:    "feature/test",
		HeadSHA:    "abc123",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "baseRef is required") {
		t.Errorf("expected baseRef error, got: %v", err)
	}
}

func TestValidateIdentity_MissingHeadRef(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadSHA:    "abc123",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "headRef is required") {
		t.Errorf("expected headRef error, got: %v", err)
	}
}

func TestValidateIdentity_MissingHeadSHA(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "headSHA is required") {
		t.Errorf("expected headSHA error, got: %v", err)
	}
}

func TestValidateIdentity_MissingCapturedAt(t *testing.T) {
	id := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "minhtri2710",
		Repo:     "munsu",
		Number:   42,
		URL:      "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:  "main",
		HeadRef:  "feature/test",
		HeadSHA:  "abc123",
	}
	err := domain.ValidateIdentity(id)
	if err == nil || !strings.Contains(err.Error(), "capturedAt is required") {
		t.Errorf("expected capturedAt error, got: %v", err)
	}
}
