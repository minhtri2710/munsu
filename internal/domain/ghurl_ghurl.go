// Package ghurl provides GitHub PR URL parsing and formatting.
// It handles the invariants of https://github.com/<owner>/<repo>/pull/<n> URLs.
package domain

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
// The only accepted format is the canonical one, so a parsed URL always equals
// its FullURL:
//
//	https://github.com/<owner>/<repo>/pull/<n>
//
// Rejects non-https schemes, userinfo, query strings, fragments, encoded or
// trailing path segments and non-canonical numbers fail closed.
func ParseGHURL(raw string) (GHURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return GHURL{}, fmt.Errorf("invalid URL: %w", err)
	}

	if u.Host != "github.com" {
		return GHURL{}, fmt.Errorf("not a github.com URL: %s", u.Host)
	}
	if u.Scheme != "https" {
		return GHURL{}, fmt.Errorf("URL must use https scheme, got %q", u.Scheme)
	}
	if u.User != nil {
		return GHURL{}, fmt.Errorf("URL must not contain userinfo (username:password@host), got %q", raw)
	}
	if u.RawQuery != "" {
		return GHURL{}, fmt.Errorf("URL must not contain query string, got %q", raw)
	}
	if u.Fragment != "" {
		return GHURL{}, fmt.Errorf("URL must not contain fragment, got %q", raw)
	}
	if u.RawPath != "" {
		return GHURL{}, fmt.Errorf("URL must not contain percent-encoded path, got %q", raw)
	}

	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
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
	for _, part := range []string{owner, repo} {
		if part == "." || part == ".." {
			return GHURL{}, fmt.Errorf("dot segments not allowed in URL path, got %q", raw)
		}
	}

	gh := GHURL{Owner: owner, Repo: repo, Num: num}
	if gh.FullURL() != raw {
		return GHURL{}, fmt.Errorf("URL must be the canonical %q, got %q", gh.FullURL(), raw)
	}
	return gh, nil
}

// FullURL reconstructs the full PR URL.
func (g GHURL) FullURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", g.Owner, g.Repo, g.Num)
}
