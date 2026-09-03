//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
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

// --- ToMeta / IdentityFromMeta round-trip tests ---

func TestIdentityRoundTrip(t *testing.T) {
	original := &domain.DeliveryIdentity{
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

	meta := original.ToMeta()
	restored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if restored == nil {
		t.Fatal("IdentityFromMeta returned nil")
	}

	// Compare fields
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Provider", restored.Provider, original.Provider},
		{"Owner", restored.Owner, original.Owner},
		{"Repo", restored.Repo, original.Repo},
		{"URL", restored.URL, original.URL},
		{"BaseRef", restored.BaseRef, original.BaseRef},
		{"HeadRef", restored.HeadRef, original.HeadRef},
		{"HeadSHA", restored.HeadSHA, original.HeadSHA},
		{"CapturedAt", restored.CapturedAt, original.CapturedAt},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if restored.Number != original.Number {
		t.Errorf("Number: got %d, want %d", restored.Number, original.Number)
	}
}

func TestIdentityFromMeta_EmptyMeta(t *testing.T) {
	meta := map[string]string{}
	id, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta on empty meta: %v", err)
	}
	if id != nil {
		t.Error("expected nil for empty meta")
	}
}

// --- RequireIdentity tests ---

func TestRequireIdentity_Success(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-task"

	original := &domain.DeliveryIdentity{
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
	if err := home.WriteMeta(homeDir, id, original.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := RequireIdentity(homeDir, id)
	if err != nil {
		t.Fatalf("RequireIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("RequireIdentity returned nil")
	}
	if got.Number != 42 {
		t.Errorf("Number: got %d, want 42", got.Number)
	}
}

func TestRequireIdentity_MissingMeta(t *testing.T) {
	homeDir := t.TempDir()
	_, err := RequireIdentity(homeDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if !strings.Contains(err.Error(), "reading task meta") {
		t.Errorf("expected 'reading task meta' error, got: %v", err)
	}
}

func TestRequireIdentity_NoPRURL(t *testing.T) {
	homeDir := t.TempDir()
	id := "no-pr-task"

	meta := map[string]string{
		"kind":    "ship",
		"project": "munsu",
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := RequireIdentity(homeDir, id)
	if err == nil {
		t.Fatal("expected error for task without PR URL")
	}
	if !strings.Contains(err.Error(), "no delivery identity found") {
		t.Errorf("expected 'no delivery identity found' error, got: %v", err)
	}
}

func TestRequireIdentity_Incomplete(t *testing.T) {
	homeDir := t.TempDir()
	id := "partial-task"

	meta := map[string]string{
		"pr_url": "https://github.com/minhtri2710/munsu/pull/42",
		// Missing pr_head_sha, pr_base_ref, pr_head_ref, etc.
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := RequireIdentity(homeDir, id)
	if err == nil {
		t.Fatal("expected error for incomplete identity")
	}
	if !strings.Contains(err.Error(), "incomplete delivery identity") {
		t.Errorf("expected 'incomplete delivery identity' error, got: %v", err)
	}
}

// --- MetaKeys test ---

func TestIdentity_MetaKeys(t *testing.T) {
	id := &domain.DeliveryIdentity{}
	keys := id.MetaKeys()
	expected := []string{
		"pr_provider", "pr_owner", "pr_repo",
		"pr_number", "pr_url",
		"pr_base_ref", "pr_head_ref", "pr_head_sha",
		"pr_timestamp",
	}
	if len(keys) != len(expected) {
		t.Errorf("got %d keys, want %d: %v", len(keys), len(expected), keys)
	}
	for _, ek := range expected {
		found := false
		for _, k := range keys {
			if k == ek {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected key %q not found in MetaKeys", ek)
		}
	}
}

func TestIdentityFromMeta_RejectsMultipleFieldsWithoutURL(t *testing.T) {
	// Multiple identity fields present but no pr_url — must fail closed.
	// This regression catches the case where the teardown package incorrectly
	// treats any non-empty identity field as "no identity" when pr_url is absent.
	meta := map[string]string{
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_sha":  "abc123def456abc123def456abc123def456abc1",
		"pr_head_ref":  "fm/feature-branch",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}
	_, err := domain.IdentityFromMeta(meta)
	if err == nil {
		t.Fatal("expected error for partial identity with multiple fields but no pr_url")
	}
	if !strings.Contains(err.Error(), "no pr_url") {
		t.Errorf("expected 'no pr_url' error, got: %v", err)
	}
}
