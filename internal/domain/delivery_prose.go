package domain

import (
	"path"
	"strings"
)

// DeliveryChange is one path a delivery changes between its base and head,
// listed with renames off. Status is git's letter: A, M, D, or anything else
// that is not prose.
type DeliveryChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Binary bool   `json:"binary"`
}

// DeliveryDiff is the change set of one delivery's base..head range. Listed is
// false when git could not list the range; an unlisted diff is never prose.
type DeliveryDiff struct {
	Listed  bool             `json:"listed"`
	Changes []DeliveryChange `json:"changes,omitempty"`
}

// ProseOnly reports whether every change is a text prose path added, modified
// or deleted. It fails closed: an unlisted, empty, binary, renamed, typed or
// non-prose change needs a head-bound ReviewVerdict instead.
//
// Words (Human, direct chat with the Lead, 2026-10-13), the grant behind the
// prose-only exemption:
//
//	Words{Grantor: "Human", Channel: "direct chat with the Lead, 2026-10-13",
//	      Quote: "Thay đổi chỉ docs/văn xuôi không cần Reviewer độc lập; mọi đường dẫn khác vẫn cần verdict."}
func (d DeliveryDiff) ProseOnly() bool {
	if !d.Listed || len(d.Changes) == 0 {
		return false
	}
	for _, c := range d.Changes {
		if c.Binary || !proseStatus(c.Status) || !isProsePath(c.Path) {
			return false
		}
	}
	return true
}

func proseStatus(status string) bool {
	return status == "A" || status == "M" || status == "D"
}

// isProsePath is the closed prose class: *.md, *.txt, or anything under docs/,
// minus doctrine and gating text. Dot-segments (.agents, .github), testdata,
// AGENTS.md and CLAUDE.md, and the Go-embedded skill and manual files are
// never prose.
func isProsePath(p string) bool {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "testdata" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	switch path.Base(p) {
	case "AGENTS.md", "CLAUDE.md":
		return false
	}
	if strings.HasPrefix(p, "internal/cli/skills/") || p == "internal/cli/seed_orchestrator_manual.md" {
		return false
	}
	if strings.HasPrefix(p, "docs/") {
		return true
	}
	switch path.Ext(p) {
	case ".md", ".txt":
		return true
	}
	return false
}
