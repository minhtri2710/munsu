// Package composer classifies a captured composer row as empty, pending, or
// unknown, and strips ANSI/ghost text so an AFK injector can decide whether a
// pane is a safe injection target.
//
// All functions are pure (no I/O) and operate on plain or ANSI-styled text.
//
// Reference: the munsu composer classification pattern.
package domain

import (
	"strconv"
	"strings"
	"unicode"
)

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
	// Busy means the composer has a submitted command or action in progress
	// (e.g. OpenCode "o Running..."). The agent is actively executing and
	// will return to Empty when done. NOT a safe injection target, but
	// distinct from Pending for diagnostic purposes.
	Busy
	// Unknown means the row is not a recognised harness composer (e.g. a bare
	// dead-shell prompt) and is never a safe injection target.
	Unknown
)

var verdictStrings = map[Verdict]string{
	Empty:   "empty",
	Pending: "pending",
	Busy:    "busy",
	Unknown: "unknown",
}

func (v Verdict) String() string { return verdictStrings[v] }

// Agent prompt glyphs recognised by every known fleet harness.
const (
	glyphClaude   = "\u276F" // ❯ — claude-code prompt glyph
	glyphCodex    = "\u203A" // › — codex prompt glyph
	glyphOpenCode = "o"      // o — opencode prompt glyph
)

// Shell prompt glyphs that signal a dead shell on bare rows.
var shellGlyphs = []rune{'>', '$', '%', '#'}

// --- Strip functions ---

// StripANSI removes every CSI escape sequence from s, leaving plain text.
// Handles the full ECMA-48 CSI grammar: ESC [ parameter-bytes intermediate-bytes final-byte.
// Parameter bytes: 0x30-0x3F (digits, ;, :, etc.)
// Intermediate bytes: 0x20-0x2F
// Final byte: 0x40-0x7E
func StripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2 // skip ESC[
			// Consume parameter bytes (0x30-0x3F)
			for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3F {
				i++
			}
			// Consume intermediate bytes (0x20-0x2F)
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2F {
				i++
			}
			// Consume final byte (0x40-0x7E)
			if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7E {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isDimCode returns true when code is a dim/faint SGR selector (2).
// code is the numeric base after stripping any colon suffix.
func isDimCode(code string) bool { return code == "2" }

// isResetCode returns true when code is an SGR reset (0).
func isResetCode(code string) bool { return code == "0" }

// isNormalIntensityCode returns true when code is a normal-intensity selector (22).
func isNormalIntensityCode(code string) bool { return code == "22" }

// isDefaultFgCode returns true when code resets foreground to default (39).
func isDefaultFgCode(code string) bool { return code == "39" }

// isFg38Code returns true when code is a foreground-colour selector (38).
func isFg38Code(code string) bool { return code == "38" }

// isFg48Code returns true when code is a background-colour selector (48).
func isFg48Code(code string) bool { return code == "48" }

// isFg58Code returns true when code is an underline-colour selector (58).
func isFg58Code(code string) bool { return code == "58" }

// isBaseFgColour returns true when code is a base foreground colour (30-37 or 90-97).
func isBaseFgColour(numCode int) bool {
	return (numCode >= 30 && numCode <= 37) || (numCode >= 90 && numCode <= 97)
}

// sgrBase returns the numeric part of an SGR parameter, stripping the colon
// suffix. E.g. "38:2::100:200:50" → "38", "2" → "2", "0" → "0".
func sgrBase(v string) string {
	if idx := strings.IndexByte(v, ':'); idx >= 0 {
		return v[:idx]
	}
	return v
}

// hasColon returns true when v contains a colon separator (ITU/colon-form SGR).
func hasColon(v string) bool { return strings.IndexByte(v, ':') >= 0 }

// splitSGRParams splits a CSI-SGR parameter string into individual parameters.
// Parameters are separated by ';'. Colon-form SGR (38:2::r:g:b) keeps the entire
// colour descriptor as one parameter.
func splitSGRParams(params string) []string {
	if params == "" {
		return nil
	}
	return strings.Split(params, ";")
}

// fg38IsDark returns true when the SGR 38 foreground starting at params[p] is
// a truecolor (type 2) whose perceived luminance is below lumaMax. 256-colour
// palette selects (type 5) are never classified as dark, matching the bash
// reference (no fleet harness uses 256-colour for ghost text).
func fg38IsDark(params []string, p int, lumaMax int) bool {
	spec := params[p]
	if idx := strings.IndexByte(spec, ':'); idx >= 0 {
		// Colon form: 38:2::r:g:b — the whole colour is in one param.
		parts := strings.Split(spec, ":")
		// Expected: [38, 2, ..., r, g, b] where parts[1] is the colour type.
		if len(parts) < 6 || parts[1] != "2" {
			return false
		}
		r, _ := strconv.Atoi(parts[len(parts)-3])
		g, _ := strconv.Atoi(parts[len(parts)-2])
		b, _ := strconv.Atoi(parts[len(parts)-1])
		return (299*r+587*g+114*b)/1000 < lumaMax
	}
	// Semicolon form: 38;2;r;g;b
	if p+1 >= len(params) || params[p+1] != "2" || p+4 >= len(params) {
		return false
	}
	r, _ := strconv.Atoi(params[p+2])
	g, _ := strconv.Atoi(params[p+3])
	b, _ := strconv.Atoi(params[p+4])
	return (299*r+587*g+114*b)/1000 < lumaMax
}

// skipColorPayload advances p past the colour payload for SGR 38/48/58,
// matching the awk skip_color_payload logic. Returns the last index to assign
// back to p (the caller's loop also increments p).
func skipColorPayload(params []string, p int) int {
	if hasColon(params[p]) {
		return p // colon form: everything in one param
	}
	if p+1 >= len(params) {
		return p
	}
	mode := sgrBase(params[p+1])
	if hasColon(params[p+1]) {
		return p + 1
	}
	switch mode {
	case "5":
		return p + 2 // 38;5;n
	case "2":
		return p + 4 // 38;2;r;g;b
	default:
		return p + 1
	}
}

// StripGhost removes de-emphasised runs (dim/faint SGR 2, and dark truecolor
// foreground) from s, returning only "real typed content". This matches the
// logic in the munsu fm_composer_strip_ghost pattern.
//
// De-emphasis tracking:
//   - dim (SGR 2): how claude and codex render ghost/suggestion text.
//     SGR 0 (reset) and SGR 22 (normal intensity) end a dim run.
//   - dark foreground (SGR 38;2;r;g;b below luminance 128): how grok renders
//     placeholder and hint text (assumes a dark terminal theme).
//     SGR 0, 39 (default fg), 30-37, 90-97, or a lighter truecolor ends it.
//
// 256-colour foregrounds (38;5;n) are NOT luminance-tested — no fleet harness
// uses them for ghost text, so they are kept.
func StripGhost(s string) string {
	const lumaMax = 128
	var b strings.Builder
	b.Grow(len(s))

	dim := false
	darkfg := false

	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			// Find end of CSI sequence
			j := i + 2
			for j < len(s) {
				c := s[j]
				if c >= '@' && c <= '~' {
					break
				}
				j++
			}
			terminal := byte(0)
			if j < len(s) {
				terminal = s[j]
			}

			if terminal == 'm' {
				// SGR sequence — parse de-emphasis state changes
				paramsStr := s[i+2 : j]
				if paramsStr == "" {
					// ESC [ m is equivalent to ESC [ 0 m — reset
					dim = false
					darkfg = false
				} else {
					params := splitSGRParams(paramsStr)
					for p := 0; p < len(params); p++ {
						code := sgrBase(params[p])
						switch {
						case isResetCode(code):
							dim = false
							darkfg = false
						case isDimCode(code):
							dim = true
						case isNormalIntensityCode(code):
							dim = false
						case isFg38Code(code):
							darkfg = fg38IsDark(params, p, lumaMax)
							p = skipColorPayload(params, p)
						case isDefaultFgCode(code):
							darkfg = false
						case isFg48Code(code), isFg58Code(code):
							p = skipColorPayload(params, p)
						default:
							if n, err := strconv.Atoi(code); err == nil && isBaseFgColour(n) {
								darkfg = false
							}
						}
					}
				}
			}
			i = j + 1
			continue
		}
		if !dim && !darkfg {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

// --- Classification ---

// isAgentPromptGlyph returns true when s is exactly one of the known agent
// prompt glyphs (❯ for claude, › for codex, o for opencode).
func isAgentPromptGlyph(s string) bool {
	return s == glyphClaude || s == glyphCodex || s == glyphOpenCode
}

// isShellPromptGlyph returns true when s is exactly one of the known shell
// prompt glyphs (>, $, %, #).
func isShellPromptGlyph(s string) bool {
	if len(s) != 1 {
		return false
	}
	r := []rune(s)[0]
	for _, g := range shellGlyphs {
		if r == g {
			return true
		}
	}
	return false
}

// hasPromptGlyphPrefix returns the stripped suffix when s starts with a known
// prompt glyph followed by a space (2-byte prefix like "❯ ") or just the glyph
// (1-byte prefix like ">ls"). The general return value indicates whether a
// prefix was found and stripped.
func stripPromptPrefix(s string) (string, bool) {
	if len(s) == 0 {
		return s, false
	}

	// Check 2-character prefixes: glyph + space
	if len(s) >= 3 {
		prefix2 := s[:2]
		if prefix2 == glyphClaude+" " || prefix2 == glyphCodex+" " || isShellPromptGlyph(prefix2[:1]) && s[1] == ' ' {
			return s[2:], true
		}
	}

	// Check 1-character prefixes: glyph only (no space)
	r := []rune(s)
	if len(r) >= 1 {
		firstRune := string(r[0])
		if isAgentPromptGlyph(firstRune) || isShellPromptGlyph(firstRune) {
			// Remove first rune (may be multi-byte)
			return s[len([]byte(firstRune)):], true
		}
	}

	// Check explicit 1-byte shell glyphs
	if len(s) >= 1 {
		c := s[0]
		if c == '>' || c == '$' || c == '%' || c == '#' {
			if len(s) >= 2 && s[1] == ' ' {
				return s[2:], true
			}
			return s[1:], true
		}
	}

	return s, false
}

// trimSpace returns s with leading and trailing whitespace removed.
func trimSpace(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

// ClassifyContent determines whether a captured composer row is empty (safe to
// inject into), pending (has unsubmitted content), busy (agent executing action),
// or unknown (not a recognised harness — likely a dead shell).
//
// row is the ghost-stripped, border-stripped, whitespace-trimmed content.
// plainRow is the ANSI-stripped (but NOT ghost-stripped) content, used for
// structural detection of agent vs shell prompt glyphs when ghost stripping
// removed the entire visible row.
// bordered should be true when the row came from inside a bordered composer box.
func ClassifyContent(row, plainRow string, bordered bool) Verdict {
	// When ghost stripping removed everything and the row is not bordered,
	// the plain content determines the verdict: agent prompt glyph → empty,
	// anything else (including a bare shell glyph) → unknown.
	if !bordered && row == "" && plainRow != "" {
		if isAgentPromptGlyph(trimSpace(plainRow)) {
			return Empty
		}
		return Unknown
	}

	// Exact match: agent prompt glyph = empty composer, bordered or bare.
	if isAgentPromptGlyph(row) {
		return Empty
	}

	// Exact match: shell prompt glyph. Inside a composer box it is the
	// harness's own prompt (empty); on a bare row it is a dead shell (unknown).
	if isShellPromptGlyph(row) {
		if bordered {
			return Empty
		}
		return Unknown
	}

	// Empty row (after ghost strip + border strip + trim) = empty composer.
	if row == "" {
		return Empty
	}

	// Strip a leading prompt glyph and re-judge.
	afterPrefix, hadPrefix := stripPromptPrefix(row)
	if hadPrefix {
		afterPrefix = trimSpace(afterPrefix)
		if afterPrefix == "" {
			return Empty
		}
		// Busy-queued content (agent executing command): recognisable action
		// keywords after the prompt glyph signal a busy agent, not pending input.
		if isBusyContent(afterPrefix) {
			return Busy
		}
		// Non-empty content after the glyph → real pending text.
		return Pending
	}

	// Non-empty content with no prompt glyph → pending.
	return Pending
}

// isBusyContent returns true when s starts with a known busy-action keyword
// that signals an agent executing a command rather than waiting for input.
// These include action participles like Running, Thinking, Working, Processing,
// Building, Installing, Searching, Generating, Analyzing.
func isBusyContent(s string) bool {
	busyPrefixes := []string{
		"Running",
		"Thinking",
		"Working",
		"Processing",
		"Building",
		"Installing",
		"Searching",
		"Generating",
		"Analyzing",
		"Fetching",
		"Compiling",
		"Executing",
		"Waiting",
	}
	for _, prefix := range busyPrefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
