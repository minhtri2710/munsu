package domain

// claudeFooterRows is how many rows Claude Code shows below the lower rule.
const claudeFooterRows = 3

// ClassifyClaudeComposer judges a capture of the bottom rows of a Claude Code
// pane. Empty means the rows read, in order, a rule line, one row that is
// exactly the prompt glyph (spaces and NBSP ignored), a rule line, and at most
// claudeFooterRows further rows. Pending means the rows between the two rules
// hold anything else (typed text, more than one row). Every other capture,
// including a missing or cut-off rule, is Unknown. Only Empty is safe; nothing
// here reads ANSI, so styled text fails closed.
func ClassifyClaudeComposer(capture string) Verdict {
	between, ok := composerBetweenRules(capture, claudeFooterRows)
	switch {
	case !ok:
		return Unknown
	case len(between) == 1 && trimComposerSpace(between[0]) == glyphClaude:
		return Empty
	default:
		return Pending
	}
}
