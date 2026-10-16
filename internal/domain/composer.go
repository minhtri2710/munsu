package domain

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
const glyphClaude = "\u276F" // ❯
