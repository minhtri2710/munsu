package domain

import "strings"

// Verdict indicates whether a composer row is safe to inject into.
type Verdict int

const (
	// Empty means the composer has no pending content and is a safe injection
	// target. This includes agent prompt glyphs (❯, ›) and ghost/placeholder
	// text that was de-emphasised.
	Empty Verdict = iota
	// Pending means the composer has unsubmitted real content (typed text
	// that the agent has not yet submitted). NOT a safe injection target.
	Pending
	// Unknown means the row is not a recognised harness composer (e.g. a bare
	// dead-shell prompt) and is never a safe injection target.
	Unknown
)

// glyphClaude is the claude-code prompt glyph.
const glyphClaude = "❯" // ❯

// composerRuleMinRunes is the shortest run of U+2500 that counts as a rule line.
const composerRuleMinRunes = 10

// composerBetweenRules returns the rows between the capture's lowest rule line
// and the rule line above it. ok is false when the lower rule is missing or has
// more than footerRows rows below it, the upper rule is missing, or no row lies
// between them.
func composerBetweenRules(capture string, footerRows int) (between []string, ok bool) {
	rows := strings.Split(strings.TrimRight(capture, " \t\r\n"), "\n")
	lower := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if isComposerRule(rows[i]) {
			lower = i
			break
		}
	}
	if lower < 0 || len(rows)-1-lower > footerRows {
		return nil, false
	}
	upper := -1
	for i := lower - 1; i >= 0; i-- {
		if isComposerRule(rows[i]) {
			upper = i
			break
		}
	}
	if upper < 0 || upper+1 == lower {
		return nil, false
	}
	return rows[upper+1 : lower], true
}

// trimComposerSpace trims U+0020 and U+00A0 only: the words approved for the
// gate ignore spaces and NBSP, not tabs or other Unicode spaces.
func trimComposerSpace(row string) string { return strings.Trim(row, "  ") }

func isComposerRule(row string) bool {
	row = trimComposerSpace(row)
	return len([]rune(row)) >= composerRuleMinRunes && strings.Trim(row, "─") == ""
}
