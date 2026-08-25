// Package glurl provides GitLab Merge Request URL parsing and formatting.
// It handles the invariants of https://<host>/<namespace>/<project>/-/merge_requests/<iid> URLs,
// including self-hosted GitLab instances and nested group namespaces.
package domain

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// GLURL holds the parsed components of a GitLab Merge Request URL.
// Format: https://<host>/<namespace>/<project>/-/merge_requests/<iid>
//
// For nested groups, Owner holds the full namespace path (e.g. "group/subgroup")
// and Project holds the project name. This maps naturally to DeliveryIdentity's
// Owner and Repo fields.
type GLURL struct {
	Host    string // e.g. "gitlab.com" or "gitlab.example.com"
	Owner   string // namespace (user or group path, e.g. "owner" or "group/subgroup")
	Project string // project name
	IID     int    // merge request IID
}

// mrSegment is the path segment that identifies a merge request URL.
const mrSegment = "/-/merge_requests/"

// ParseMRURL parses a GitLab Merge Request URL and returns the components.
// Accepted formats:
//
//	https://gitlab.com/<namespace>/<project>/-/merge_requests/<iid>
//	https://<host>/<namespace>/<project>/-/merge_requests/<iid>
//
// Nested groups:
//
//	https://gitlab.com/group/subgroup/project/-/merge_requests/<iid>
//
// Rejects ambiguous/non-MR URLs fail closed.
func ParseMRURL(raw string) (GLURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return GLURL{}, fmt.Errorf("invalid URL: %w", err)
	}

	if u.Host == "" {
		return GLURL{}, fmt.Errorf("URL %q has no host", raw)
	}

	// Reject any RawPath (percent-encoded or escaped path).
	// Even if RawPath equals Path, percent-encoded paths are not valid MR URLs.
	if u.RawPath != "" {
		return GLURL{}, fmt.Errorf("URL must not contain percent-encoded path, got %q", raw)
	}

	// Reject non-HTTPS schemes
	if u.Scheme != "https" {
		return GLURL{}, fmt.Errorf("URL must use https scheme, got %q", u.Scheme)
	}

	// Reject userinfo (credentials in URL)
	if u.User != nil {
		return GLURL{}, fmt.Errorf("URL must not contain userinfo (username:password@host), got %q", raw)
	}

	// Reject query strings
	if u.RawQuery != "" {
		return GLURL{}, fmt.Errorf("URL must not contain query string, got %q", raw)
	}

	// Reject fragments
	if u.Fragment != "" {
		return GLURL{}, fmt.Errorf("URL must not contain fragment, got %q", raw)
	}

	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		return GLURL{}, fmt.Errorf("URL %q has no path", raw)
	}

	// Find the MR segment in the path
	idx := strings.Index(path, mrSegment)
	if idx < 0 {
		return GLURL{}, fmt.Errorf("URL path must contain /-/merge_requests/<n>, got %q", u.Path)
	}

	// Everything after mrSegment is the MR IID
	iidStr := path[idx+len(mrSegment):]
	iid, err := strconv.Atoi(iidStr)
	if err != nil {
		return GLURL{}, fmt.Errorf("invalid merge request IID %q: %w", iidStr, err)
	}

	if iid <= 0 {
		return GLURL{}, fmt.Errorf("merge request IID must be positive, got %d", iid)
	}

	// The namespace+project is everything before the MR segment
	// We need to split it into Owner (namespace) and Project
	namespacePath := strings.Trim(path[:idx], "/")
	if namespacePath == "" {
		return GLURL{}, fmt.Errorf("namespace/project path is empty in URL %q", raw)
	}

	// Split the namespace path. Everything before the last segment is the namespace,
	// the last segment is the project name.
	nsParts := strings.Split(namespacePath, "/")

	// Reject empty segments (e.g., "group//project") and dot segments
	for _, part := range nsParts {
		if part == "" {
			return GLURL{}, fmt.Errorf("empty namespace/project segment in URL %q", raw)
		}
		if part == "." || part == ".." {
			return GLURL{}, fmt.Errorf("dot segments not allowed in URL path, got %q", raw)
		}
	}

	project := nsParts[len(nsParts)-1]
	// Owner is everything before the last segment (could be empty for flat namespace)
	owner := strings.Join(nsParts[:len(nsParts)-1], "/")

	if owner == "" {
		return GLURL{}, fmt.Errorf("namespace (owner) is empty in URL %q; expected /<namespace>/<project>/-/merge_requests/<iid>", raw)
	}

	return GLURL{
		Host:    u.Host,
		Owner:   owner,
		Project: project,
		IID:     iid,
	}, nil
}

// FullURL reconstructs the full MR URL.
func (g GLURL) FullURL() string {
	return fmt.Sprintf("https://%s/%s/%s/-/merge_requests/%d", g.Host, g.Owner, g.Project, g.IID)
}
