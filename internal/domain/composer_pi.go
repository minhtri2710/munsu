package domain

// piFooterRows bounds the rows pi shows below the lower rule: the captures
// show at most 3 (cwd, stats, one extension status row), plus one row for a
// second extension status row.
const piFooterRows = 4

// ClassifyPiComposer judges a capture of the bottom rows of a pi pane. Empty
// means a rule line, one blank row (spaces and NBSP ignored), a rule line, and
// at most piFooterRows further rows. Any other content between the rules is
// Pending; every other capture is Unknown.
func ClassifyPiComposer(capture string) Verdict {
	between, ok := composerBetweenRules(capture, piFooterRows)
	switch {
	case !ok:
		return Unknown
	case len(between) == 1 && trimComposerSpace(between[0]) == "":
		return Empty
	default:
		return Pending
	}
}
