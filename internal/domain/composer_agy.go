package domain

import "regexp"

// glyphAgy is the Antigravity CLI prompt glyph.
const glyphAgy = ">"

// agyFooter matches agy's one footer row by its trailing "<model> · <effort>"
// segment: the only part every captured footer row carries, whether the
// prefix is "? for shortcuts", "esc to cancel" or blank.
var agyFooter = regexp.MustCompile(`\S · [a-z]+$`)

// ClassifyAgyComposer judges a capture of the bottom rows of an agy pane.
// Empty means a rule line, one row that is exactly the prompt glyph (spaces
// and NBSP ignored), a rule line of the same width, and one footer row ending
// in the model segment. A single blank row between the rules is Unknown. Any
// other content between such rules is Pending; every other capture is Unknown.
func ClassifyAgyComposer(capture string) Verdict {
	box, ok := findComposerBox(capture, 1)
	switch {
	case !ok, !box.sameWidth(), len(box.footer) != 1, !agyFooter.MatchString(trimComposerSpace(box.footer[0])):
		return Unknown
	case len(box.between) == 1 && trimComposerSpace(box.between[0]) == glyphAgy:
		return Empty
	case len(box.between) == 1 && trimComposerSpace(box.between[0]) == "":
		return Unknown
	default:
		return Pending
	}
}
