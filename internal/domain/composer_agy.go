package domain

// glyphAgy is the Antigravity CLI prompt glyph.
const glyphAgy = ">"

// agyFooterRows is how many rows agy shows below the lower rule.
const agyFooterRows = 1

// ClassifyAgyComposer judges a capture of the bottom rows of an agy pane.
// Empty means a rule line, one row that is exactly the prompt glyph (spaces
// and NBSP ignored), a rule line, and at most agyFooterRows further rows. A
// single blank row between the rules is Unknown. Any other content between
// the rules is Pending; every other capture is Unknown.
func ClassifyAgyComposer(capture string) Verdict {
	between, ok := composerBetweenRules(capture, agyFooterRows)
	switch {
	case !ok:
		return Unknown
	case len(between) == 1 && trimComposerSpace(between[0]) == glyphAgy:
		return Empty
	case len(between) == 1 && trimComposerSpace(between[0]) == "":
		return Unknown
	default:
		return Pending
	}
}
