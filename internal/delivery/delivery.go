// Package delivery implements delivery operations: review-diff, pr-check,
// pr-merge, merge-local, and no-mistakes pipeline integration.
package delivery

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// GHURL holds the parsed components of a GitHub PR URL.
// Format: https://github.com/<owner>/<repo>/pull/<n>
type GHURL struct {
	Owner string
	Repo  string
	Num   int
}

// ParseGHURL parses a GitHub PR URL and returns the components.
// Accepted formats:
//
//	https://github.com/<owner>/<repo>/pull/<n>
func ParseGHURL(raw string) (GHURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return GHURL{}, fmt.Errorf("invalid URL: %w", err)
	}

	if u.Host != "github.com" {
		return GHURL{}, fmt.Errorf("not a github.com URL: %s", u.Host)
	}

	path := strings.Trim(u.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return GHURL{}, fmt.Errorf("URL path must be /<owner>/<repo>/pull/<n>, got %q", u.Path)
	}

	owner := parts[0]
	repo := parts[1]
	numStr := parts[3]

	num, err := strconv.Atoi(numStr)
	if err != nil {
		return GHURL{}, fmt.Errorf("invalid PR number %q: %w", numStr, err)
	}

	if num <= 0 {
		return GHURL{}, fmt.Errorf("PR number must be positive, got %d", num)
	}

	if owner == "" || repo == "" {
		return GHURL{}, fmt.Errorf("owner and repo must not be empty, got owner=%q repo=%q", owner, repo)
	}

	return GHURL{Owner: owner, Repo: repo, Num: num}, nil
}

// FormatPRRef returns the PR reference string like "owner/repo#123".
func (g GHURL) FormatPRRef() string {
	return fmt.Sprintf("%s/%s#%d", g.Owner, g.Repo, g.Num)
}

// FullURL reconstructs the full PR URL.
func (g GHURL) FullURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", g.Owner, g.Repo, g.Num)
}
