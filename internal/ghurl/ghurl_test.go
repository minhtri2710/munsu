package ghurl

import (
	"strings"
	"testing"
)

func TestParseGHURL_Valid(t *testing.T) {
	tests := []struct {
		url   string
		owner string
		repo  string
		num   int
	}{
		{"https://github.com/beowulf/munsu/pull/42", "beowulf", "munsu", 42},
		{"https://github.com/owner/repo/pull/1", "owner", "repo", 1},
		{"https://github.com/a/b/pull/99999", "a", "b", 99999},
	}

	for _, tt := range tests {
		gh, err := ParseGHURL(tt.url)
		if err != nil {
			t.Errorf("ParseGHURL(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if gh.Owner != tt.owner {
			t.Errorf("owner = %q, want %q", gh.Owner, tt.owner)
		}
		if gh.Repo != tt.repo {
			t.Errorf("repo = %q, want %q", gh.Repo, tt.repo)
		}
		if gh.Num != tt.num {
			t.Errorf("num = %d, want %d", gh.Num, tt.num)
		}
	}
}

func TestParseGHURL_Invalid(t *testing.T) {
	tests := []struct {
		url string
		msg string
	}{
		{"", "not a github.com URL"},
		{"not-a-url", "not a github.com URL"},
		{"https://gitlab.com/owner/repo/pull/1", "not a github.com URL"},
		{"https://github.com/owner/repo/issue/1", "URL path must be"},
		{"https://github.com/owner/repo/pull/abc", "invalid PR number"},
		{"https://github.com/owner/repo/pull/0", "PR number must be positive"},
		{"https://github.com/owner/repo/pull/-1", "PR number must be positive"},
	}

	for _, tt := range tests {
		_, err := ParseGHURL(tt.url)
		if err == nil {
			t.Errorf("ParseGHURL(%q) expected error containing %q, got nil", tt.url, tt.msg)
			continue
		}
		if !strings.Contains(err.Error(), tt.msg) {
			t.Errorf("ParseGHURL(%q) error = %q, want containing %q", tt.url, err.Error(), tt.msg)
		}
	}
}

func TestGHURL_FormatPRRef(t *testing.T) {
	gh := GHURL{Owner: "beowulf", Repo: "munsu", Num: 42}
	if ref := gh.FormatPRRef(); ref != "beowulf/munsu#42" {
		t.Errorf("FormatPRRef() = %q, want %q", ref, "beowulf/munsu#42")
	}
}

func TestGHURL_FullURL(t *testing.T) {
	gh := GHURL{Owner: "beowulf", Repo: "munsu", Num: 42}
	want := "https://github.com/beowulf/munsu/pull/42"
	if url := gh.FullURL(); url != want {
		t.Errorf("FullURL() = %q, want %q", url, want)
	}
}
