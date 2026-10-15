package domain

import (
	"strings"
	"testing"
)

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
