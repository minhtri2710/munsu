package domain

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
		{"http://github.com/owner/repo/pull/5", "must use https scheme"},
		{"https://token@github.com/owner/repo/pull/5", "must not contain userinfo"},
		{"https://u:p@github.com/o/r/pull/5/files?x#y", "must not contain userinfo"},
		{"https://github.com/owner/repo/pull/5?foo=bar", "must not contain query string"},
		{"https://github.com/owner/repo/pull/5#section", "must not contain fragment"},
		{"https://github.com/owner/repo/pull/5/files", "URL path must be"},
		{"https://github.com/owner/repo/pull/5/", "URL path must be"},
		{"https://github.com/owner/repo/pull", "URL path must be"},
		{"https://github.com//repo/pull/5", "owner and repo must not be empty"},
		{"https://github.com/owner/../pull/5", "dot segments not allowed"},
		{"https://github.com/owner/./pull/5", "dot segments not allowed"},
		{"https://github.com/owner%2Fx/repo/pull/5", "percent-encoded path"},
		{"https://github.com/owner/repo/pull/05", "must be the canonical"},
		{"https://github.com/owner/repo/pull/+5", "must be the canonical"},
		{"https://github.com/owner/repo/pull/5?", "must be the canonical"},
		{"https://github.com/owner/repo/pull/5#", "must be the canonical"},
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

func TestGHURL_FullURL(t *testing.T) {
	gh := GHURL{Owner: "beowulf", Repo: "munsu", Num: 42}
	want := "https://github.com/beowulf/munsu/pull/42"
	if url := gh.FullURL(); url != want {
		t.Errorf("FullURL() = %q, want %q", url, want)
	}
}
