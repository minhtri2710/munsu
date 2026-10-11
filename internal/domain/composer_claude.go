package domain

import (
	"strings"
	"unicode"
)

// ClaudeComposerRows is how many bottom pane rows the caller captures for
// ClassifyClaudeComposer.
const ClaudeComposerRows = 12

// claudeRuleMinRunes is the shortest run of U+2500 that counts as a rule line.
const claudeRuleMinRunes = 10

// claudeFooterRows is how many rows Claude Code shows below the lower rule.
const claudeFooterRows = 3

// ClassifyClaudeComposer judges a capture of the bottom ClaudeComposerRows rows
// of a Claude Code pane (a longer capture is judged by the same bottom rule). Empty means the rows read, in order, a rule line, one
// row that is exactly the prompt glyph (spaces and NBSP ignored), a rule line,
// and at most claudeFooterRows further rows. Pending means the rows between
// the two rules hold anything else (typed text, more than one row). Every
// other capture, including a missing or cut-off rule, is Unknown. Only Empty
// is safe; nothing here reads ANSI, so styled text fails closed.
func ClassifyClaudeComposer(capture string) Verdict {
	rows := strings.Split(strings.TrimRight(capture, " \t\r\n"), "\n")
	lower := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if isClaudeRule(rows[i]) {
			lower = i
			break
		}
	}
	if lower < 0 || len(rows)-1-lower > claudeFooterRows {
		return Unknown
	}
	upper := -1
	for i := lower - 1; i >= 0; i-- {
		if isClaudeRule(rows[i]) {
			upper = i
			break
		}
	}
	switch between := rows[upper+1 : lower]; {
	case upper < 0, len(between) == 0:
		return Unknown
	case len(between) == 1 && strings.TrimFunc(between[0], unicode.IsSpace) == glyphClaude:
		return Empty
	default:
		return Pending
	}
}

func isClaudeRule(row string) bool {
	row = strings.TrimFunc(row, unicode.IsSpace)
	return len([]rune(row)) >= claudeRuleMinRunes && strings.Trim(row, "─") == ""
}
