package fleet

import (
	"github.com/minhtri2710/munsu/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func validIdentity() *domain.DeliveryIdentity {
	return &domain.DeliveryIdentity{
		Provider: "github", Owner: "minhtri2710", Repo: "munsu", Number: 42,
		URL: "https://github.com/minhtri2710/munsu/pull/42", BaseRef: "main",
		HeadRef: "feature/test", HeadSHA: "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
}

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

func TestIdentityFromMeta_LegacyPRKey(t *testing.T) {
	meta := map[string]string{
		"pr": "https://github.com/minhtri2710/munsu/pull/42",
	}
	id, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta with legacy pr key: %v", err)
	}
	if id == nil {
		t.Fatal("expected non-nil identity from legacy key")
	}
	if id.URL != "https://github.com/minhtri2710/munsu/pull/42" {
		t.Errorf("URL: got %q, want %q", id.URL, "https://github.com/minhtri2710/munsu/pull/42")
	}
	if id.Number != 42 {
		t.Errorf("Number: got %d, want 42", id.Number)
	}
	// Legacy key should derive owner/repo from URL
	if id.Owner == "" {
		t.Error("expected Owner to be derived from URL")
	}
	if id.Repo == "" {
		t.Error("expected Repo to be derived from URL")
	}
}

func TestIdentityFromMeta_PartialWithLegacy(t *testing.T) {
	// Mix of new-style and legacy keys
	meta := map[string]string{
		"pr":          "https://github.com/minhtri2710/munsu/pull/42",
		"pr_head":     "abc123def456abc123def456abc123def456abc1",
		"pr_provider": "github",
	}
	id, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if id == nil {
		t.Fatal("expected non-nil identity")
	}
	if id.HeadSHA != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("HeadSHA: got %q", id.HeadSHA)
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
		"pr":     "https://github.com/minhtri2710/munsu/pull/42",
		// Missing pr_head (headSHA), pr_base, pr_head_ref, etc.
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
		"pr_base", "pr_base_ref", "pr_head_ref", "pr_head", "pr_head_sha",
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

// --- Legacy backward compatibility: read-only migration refuses destructive action ---

func TestRequireIdentity_LegacyPRKeyOnly(t *testing.T) {
	homeDir := t.TempDir()
	id := "legacy-task"

	// Simulate an old meta file that only has "pr" and "pr_head" (no new-style keys)
	meta := map[string]string{
		"pr":      "https://github.com/minhtri2710/munsu/pull/42",
		"pr_head": "abc123def456abc123def456abc123def456abc1",
		"kind":    "ship",
		"project": "munsu",
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// The legacy key migration should succeed for read-only, but
	// IdentityFromMeta will produce an identity missing pr_base, pr_head_ref,
	// pr_timestamp, etc. So RequireIdentity should still fail because
	// ValidateIdentity catches the missing required fields.
	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if ident == nil {
		t.Fatal("IdentityFromMeta should return identity from legacy keys")
	}
	if ident.URL != "https://github.com/minhtri2710/munsu/pull/42" {
		t.Errorf("URL: got %q", ident.URL)
	}
	if ident.HeadSHA != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("HeadSHA: got %q", ident.HeadSHA)
	}
	// The validation should fail because baseRef/headRef/capturedAt are missing
	if err := domain.ValidateIdentity(ident); err == nil {
		t.Error("expected ValidateIdentity to fail for legacy-only identity (missing baseRef, headRef, capturedAt)")
	}
}

// --- PRCheck writes new identity keys ---

func TestPRCheck_WritesIdentityKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	requireGH(t)

	homeDir := t.TempDir()
	id := "identity-check-task"

	meta := map[string]string{
		"kind":    "ship",
		"project": "munsu",
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	prURL := "https://github.com/minhtri2710/munsu/pull/24"
	if err := PRCheck(homeDir, id, prURL); err != nil {
		t.Fatalf("PRCheck: %v", err)
	}

	// Verify meta contains all identity keys
	readMeta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	// Check new identity keys exist
	identityKeys := []string{
		"pr_provider", "pr_owner", "pr_repo",
		"pr_number", "pr_url",
		"pr_base", "pr_head_ref", "pr_head",
		"pr_timestamp",
	}
	for _, k := range identityKeys {
		if v, ok := readMeta[k]; !ok || v == "" {
			t.Errorf("expected key %q to be set after PRCheck, got value: %q", k, v)
		}
	}

	// Verify legacy keys still work
	if readMeta["pr"] != prURL {
		t.Errorf("legacy pr key: got %q, want %q", readMeta["pr"], prURL)
	}
	if readMeta["pr_head"] == "" {
		t.Error("legacy pr_head key should be set")
	}

	// Verify IdentityFromMeta round-trips from the meta
	ident, err := domain.IdentityFromMeta(readMeta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if ident == nil {
		t.Fatal("IdentityFromMeta returned nil")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		t.Errorf("ValidateIdentity: %v", err)
	}
}

// --- CaptureIdentity tests ---

func TestCaptureIdentity_InvalidURL(t *testing.T) {
	_, err := CaptureIdentity("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestCaptureIdentity_NonGithubURL(t *testing.T) {
	_, err := CaptureIdentity("https://gitlab.com/owner/repo/pull/1")
	if err == nil {
		t.Fatal("expected error for non-github URL")
	}
}

// --- PRMerge identity validation tests ---

func TestPRMerge_ValidatesIdentity(t *testing.T) {
	// This test verifies that PRMerge rejects a merge when no identity exists,
	// without needing gh-axi.
	homeDir := t.TempDir()
	id := "no-identity-task"

	// Write meta without PR identity
	meta := map[string]string{
		"kind":    "ship",
		"project": "munsu",
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// PRMerge without identity should fail before gh-axi is even looked up
	err := PRMerge(homeDir, id, "https://github.com/minhtri2710/munsu/pull/999999", nil)
	if err == nil {
		t.Fatal("expected error for merge without identity")
	}
	if !strings.Contains(err.Error(), "cannot merge without valid delivery identity") &&
		!strings.Contains(err.Error(), "delivery identity") {
		t.Errorf("expected identity-related error, got: %v", err)
	}
}

func TestIdentityFromMeta_RejectsMalformedURL(t *testing.T) {
	meta := validIdentity().ToMeta()
	meta["pr_url"] = "not-a-pr-url"
	if _, err := domain.IdentityFromMeta(meta); err == nil {
		t.Fatal("expected malformed pr_url error")
	}
}

func TestIdentityFromMeta_RejectsURLFieldMismatch(t *testing.T) {
	meta := validIdentity().ToMeta()
	meta["pr_owner"] = "different-owner"
	if _, err := domain.IdentityFromMeta(meta); err == nil {
		t.Fatal("expected URL/field mismatch error")
	}
}

func TestPRMerge_RejectsURLMismatch(t *testing.T) {
	homeDir := t.TempDir()
	id := "mismatch-task"

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

	// Different PR URL should be rejected
	err := PRMerge(homeDir, id, "https://github.com/minhtri2710/munsu/pull/999999", nil)
	if err == nil {
		t.Fatal("expected error for URL mismatch")
	}
	if !strings.Contains(err.Error(), "PR URL mismatch") {
		t.Errorf("expected 'PR URL mismatch' error, got: %v", err)
	}
}

func TestPRMerge_RejectsLiveIdentityDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*domain.DeliveryIdentity)
	}{
		{"force push", func(id *domain.DeliveryIdentity) { id.HeadSHA = "def456def456def456def456def456def456def4" }},
		{"base retarget", func(id *domain.DeliveryIdentity) { id.BaseRef = "release" }},
		{"head ref change", func(id *domain.DeliveryIdentity) { id.HeadRef = "feature/renamed" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			stored := validIdentity()
			if err := home.WriteMeta(homeDir, "ship", stored.ToMeta()); err != nil {
				t.Fatal(err)
			}
			live := *stored
			tc.mutate(&live)
			old := fetchLiveIdentity
			fetchLiveIdentity = func(string) (*domain.DeliveryIdentity, error) { return &live, nil }
			t.Cleanup(func() { fetchLiveIdentity = old })
			err := PRMerge(homeDir, "ship", stored.URL, nil)
			if err == nil || !strings.Contains(err.Error(), "live PR identity changed") || !strings.Contains(err.Error(), "re-run pr-check") {
				t.Fatalf("error = %v, want live identity refusal", err)
			}
		})
	}
}

func TestIdentityFromMeta_RejectsCorruptNumber(t *testing.T) {
	meta := validIdentity().ToMeta()
	meta["pr_number"] = "not-a-number"
	if _, err := domain.IdentityFromMeta(meta); err == nil {
		t.Fatal("expected corrupt pr_number error")
	}
}

func TestIdentityFromMeta_RejectsPartialIdentityWithoutURL(t *testing.T) {
	if _, err := domain.IdentityFromMeta(map[string]string{"pr_head": "abc123"}); err == nil {
		t.Fatal("expected partial identity error")
	}
}

// --- ReviewDiff uses stored identity ---

func TestReviewDiff_LegacyPRKeyRead(t *testing.T) {
	// Test that ReviewDiff reads the legacy pr key from meta
	// without requiring a full domain.DeliveryIdentity.
	// The git diff error proves the legacy path was reached.
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	homeDir := t.TempDir()

	repoDir := filepath.Join(homeDir, "projects", "test-project")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	initGitRepo(t, repoDir, "")

	id := "legacy-review-task"
	meta := map[string]string{
		"project":  "test-project",
		"worktree": repoDir,
		"pr":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_head":  "abc123def456abc123def456abc123def456abc1",
	}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// ReviewDiff should reach the legacy pr key path but fail at git diff
	// because the PR ref doesn't exist in the repo. This proves the legacy
	// path was activated before destructive actions are attempted.
	err := ReviewDiff(homeDir, id)
	if err == nil {
		t.Fatal("expected git error from ReviewDiff with non-existent PR ref")
	}
	// Error should be about git, not about identity being missing
	if strings.Contains(err.Error(), "delivery identity") {
		t.Errorf("error should not be about missing identity, got: %v", err)
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
		"pr_head":      "abc123def456abc123def456abc123def456abc1",
		"pr_head_ref":  "fm/feature-branch",
		"pr_base":      "main",
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
