package domain

import (
	"regexp"
	"strings"
)

// piFooterRows bounds the rows pi shows below the lower rule: the captures
// show at most 3 (cwd, stats, one extension status row), plus one row for a
// second extension status row.
const piFooterRows = 4

// piStats matches the start of pi's stats row: the usage tokens pi 1.2.0's
// footer can print (↑input ↓output Rcache-read Wcache-write CHcache-hit%
// $cost), then the context stat, e.g. "↑9.2k ↓5 3.6%/256k (auto) • xp".
var piStats = regexp.MustCompile(`^(?:(?:[↑↓RW]\d+(?:\.\d)?[kM]?|CH\d+\.\d%|\$\d+\.\d{3}(?: \(sub\))?) )*\d+(\.\d+)?%/\d+k\b`)

// ClassifyPiComposer judges a capture of the bottom rows of a pi pane. Empty
// means a blank row, a rule line, one blank row (spaces and NBSP ignored), a
// rule line of the same width, then 2 to piFooterRows footer rows: the cwd (an
// absolute or ~ path), the stats row, and extension status rows. Any other
// content between such rules is Pending; every other capture is Unknown.
func ClassifyPiComposer(capture string) Verdict {
	box, ok := findComposerBox(capture, piFooterRows)
	switch {
	case !ok, !box.spacedAbove, !box.sameWidth(), !isPiFooter(box.footer):
		return Unknown
	case len(box.between) == 1 && trimComposerSpace(box.between[0]) == "":
		return Empty
	default:
		return Pending
	}
}

func isPiFooter(rows []string) bool {
	if len(rows) < 2 {
		return false
	}
	cwd, stats := trimComposerSpace(rows[0]), trimComposerSpace(rows[1])
	return (strings.HasPrefix(cwd, "/") || strings.HasPrefix(cwd, "~")) && piStats.MatchString(stats) && strings.Contains(stats, " • ")
}
