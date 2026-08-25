package domain

import (
	"strings"
	"testing"
)

func TestIdentityFromMetaRejectsIdentityFieldWithoutURL(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{"pr_head": "abc123"})
	if err == nil || !strings.Contains(err.Error(), "delivery identity has pr_head but no pr_url") {
		t.Fatalf("IdentityFromMeta error = %v, want missing-URL refusal", err)
	}
}

func TestIdentityFromMetaRejectsProviderURLMismatch(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_provider": "github",
		"pr_url":      "https://gitlab.com/owner/project/-/merge_requests/42",
	})
	if err == nil || !strings.Contains(err.Error(), "provider mismatch") {
		t.Fatalf("IdentityFromMeta error = %v, want provider-mismatch refusal", err)
	}
}

func TestIdentityFromMetaRejectsOwnerURLMismatch(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_url":   "https://github.com/owner/project/pull/42",
		"pr_owner": "other-owner",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match pr_url owner") {
		t.Fatalf("IdentityFromMeta error = %v, want owner-mismatch refusal", err)
	}
}

func TestIdentityFromMetaRejectsRepoURLMismatch(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_url":  "https://github.com/owner/project/pull/42",
		"pr_repo": "other-project",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match pr_url repo") {
		t.Fatalf("IdentityFromMeta error = %v, want repo-mismatch refusal", err)
	}
}

func TestIdentityFromMetaRejectsNumberURLMismatch(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_url":    "https://github.com/owner/project/pull/42",
		"pr_number": "7",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match pr_url number") {
		t.Fatalf("IdentityFromMeta error = %v, want number-mismatch refusal", err)
	}
}

func TestIdentityFromMetaRejectsHeadFieldConflict(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_url":      "https://github.com/owner/project/pull/42",
		"pr_head_sha": "sha-one",
		"pr_head":     "sha-two",
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with pr_head") {
		t.Fatalf("IdentityFromMeta error = %v, want head-field conflict refusal", err)
	}
}

func TestIdentityFromMetaRejectsBaseFieldConflict(t *testing.T) {
	_, err := IdentityFromMeta(map[string]string{
		"pr_url":      "https://github.com/owner/project/pull/42",
		"pr_base_ref": "main",
		"pr_base":     "develop",
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with pr_base") {
		t.Fatalf("IdentityFromMeta error = %v, want base-field conflict refusal", err)
	}
}

func TestParseGHURLRejectsEmptyOwnerOrRepo(t *testing.T) {
	_, err := ParseGHURL("https://github.com/owner//pull/1")
	if err == nil || !strings.Contains(err.Error(), "owner and repo must not be empty") {
		t.Fatalf("ParseGHURL error = %v, want empty-owner-or-repo refusal", err)
	}
}

func TestParseMRURLRejectsEmptyNamespaceProjectPath(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/-/merge_requests/1")
	if err == nil || !strings.Contains(err.Error(), "namespace/project path is empty") {
		t.Fatalf("ParseMRURL error = %v, want empty-namespace/project refusal", err)
	}
}
