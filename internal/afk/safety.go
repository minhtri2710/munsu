package afk

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// PaneCapture is the interface for capturing pane content.
// session.Backend implements this via its Capture method.
type PaneCapture interface {
	Capture(windowID string, lines int) (string, error)
}

// IsSafeInjectTarget captures the composer row of a pane and classifies it
// using the composer package. Returns true only when the composer is Empty
// (safe to inject — agent is waiting for input).
//
// Returns false (with verdict Pending) when there is unsubmitted typed content.
// Returns false (with verdict Unknown) for dead-shell bare prompts and other
// unrecognised harness states — these are NEVER safe injection targets.
func IsSafeInjectTarget(cap PaneCapture, paneHandle string) (bool, domain.Verdict, error) {
	output, err := cap.Capture(paneHandle, 4)
	if err != nil {
		return false, domain.Unknown, fmt.Errorf("capturing pane %q: %w", paneHandle, err)
	}

	// Normalise line endings and take the last non-empty line (composer row).
	output = strings.TrimRight(output, "\n\r")
	lines := strings.Split(output, "\n")
	composerLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			composerLine = lines[i]
			break
		}
	}

	// Strip ghost text and ANSI for classification.
	stripped := domain.StripGhost(composerLine)
	trimmed := strings.TrimSpace(stripped)
	plain := strings.TrimSpace(domain.StripANSI(composerLine))

	// Determine bordered status heuristically.
	// Agent glyphs (❯, ›) and detected borders → bordered=true (safe for
	// ghost-only placeholders). Shell prompt glyphs → bordered=false.
	bordered := false

	// Check for visible border characters from any captured line.
	for _, line := range lines {
		if hasBorderChars(line) {
			bordered = true
			break
		}
	}

	// Agent prompt glyphs also indicate a bordered composer even without
	// visible frame characters (the frame may be in a different capture region).
	if !bordered && (strings.HasPrefix(plain, "\u276F") || strings.HasPrefix(plain, "\u203A")) {
		bordered = true
	}

	// If ghost-stripped content is empty and plain text exists, classify
	// with bordered=true when the plain text is NOT a shell glyph. This
	// handles the "dim/grok-only placeholder inside a bordered composer"
	// case correctly while keeping bare dead-shell glyphs as Unknown.
	if !bordered && trimmed == "" && plain != "" && !isShellGlyphStart(plain) {
		bordered = true
	}

	verdict := domain.ClassifyContent(trimmed, plain, bordered)
	safe := verdict == domain.Empty
	return safe, verdict, nil
}

// hasBorderChars returns true when s contains a box-drawing character
// commonly used in terminal UIs for bordered composer boxes.
func hasBorderChars(s string) bool {
	chars := []string{"\u250C", "\u2510", "\u2514", "\u2518", "\u2502",
		"\u256D", "\u256E", "\u2570", "\u256F", "\u251C", "\u2524"}
	for _, c := range chars {
		if strings.Contains(s, c) {
			return true
		}
	}
	return false
}

// isShellGlyphStart returns true when s starts with a bare shell prompt glyph.
func isShellGlyphStart(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := string([]rune(s)[0])
	switch first {
	case ">", "$", "%", "#":
		return true
	}
	return false
}
