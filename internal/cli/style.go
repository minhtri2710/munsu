package cli

import (
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

var (
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleMuted  = lipgloss.NewStyle().Faint(true)
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)

// dashTheme is the dashboard's grays and backgrounds, picked for the
// terminal's background; the state colors stay the ANSI ones above.
type dashTheme struct {
	subtle, border, bar, sel lipgloss.Style
}

func dashThemeFor(dark bool) dashTheme {
	ld := lipgloss.LightDark(dark)
	fg := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(ld(lipgloss.Color(light), lipgloss.Color(dark)))
	}
	bg := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Background(ld(lipgloss.Color(light), lipgloss.Color(dark)))
	}
	return dashTheme{
		subtle: fg("#6C6C6C", "#8A8A8A"),
		border: fg("#BCBCBC", "#4E4E4E"),
		bar:    bg("#E4E4E4", "#262626"),
		sel:    bg("#DCE2EE", "#2B3245"),
	}
}

// pillStyle is a state badge: bold text on a state color.
func pillStyle(state lipgloss.Style) lipgloss.Style {
	fg := lipgloss.Color("0")
	if state.GetForeground() == styleFail.GetForeground() {
		fg = lipgloss.Color("15")
	}
	return lipgloss.NewStyle().Bold(true).Foreground(fg).Background(state.GetForeground())
}

type stateMark struct {
	icon  string
	style lipgloss.Style
}

// unknownMark keeps an unlisted word, such as a stale row's "working [stale]",
// muted and never ok.
var unknownMark = stateMark{"?", styleMuted}

// stateMarks is the one state vocabulary for human output. The printed word
// stays the caller's own, so the line reads the same without icon or color.
var stateMarks = map[string]stateMark{
	"done":                {"✓", styleOK},
	"resolved":            {"✓", styleOK},
	"current":             {"✓", styleOK},
	"installed":           {"✓", styleOK},
	"FOUND":               {"✓", styleOK},
	"INSTALLED":           {"✓", styleOK},
	"GH_AUTH":             {"✓", styleOK},
	"working":             {"●", styleOK},
	"alive":               {"●", styleOK},
	"running":             {"●", styleAccent},
	"starting":            {"●", styleAccent},
	"queued":              {"○", styleWarn},
	"registered":          {"○", styleWarn},
	"seeded":              {"○", styleWarn},
	"blocked":             {"!", styleFail},
	"NEEDS_GH_AUTH":       {"!", styleWarn},
	"stale":               {"!", styleWarn},
	"drifted":             {"!", styleWarn},
	"unresponsive":        {"!", styleWarn},
	"dead":                {"✗", styleFail},
	"failed":              {"✗", styleFail},
	"error":               {"✗", styleFail},
	"absent":              {"✗", styleFail},
	"MISSING":             {"✗", styleFail},
	"INSTALL_FAILED":      {"✗", styleFail},
	"BASE_CONFIG_INVALID": {"✗", styleFail},
	"retired":             {"·", styleMuted},
	"unsupported":         {"·", styleMuted},
	"unresolved":          {"·", styleMuted},
}

func markFor(word string) stateMark {
	if m, ok := stateMarks[word]; ok {
		return m
	}
	return unknownMark
}

type markMode int

const (
	markPlain markMode = iota
	markIcons
	markColor
)

// isTerminal is term.IsTerminal. Tests replace it.
var isTerminal = term.IsTerminal

// outputMarks picks the mode for w: plain unless w is a terminal, and no color
// when NO_COLOR is set. Tests replace it.
var outputMarks = func(w io.Writer) markMode {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(int(f.Fd())) {
		return markPlain
	}
	if os.Getenv("NO_COLOR") != "" {
		return markIcons
	}
	return markColor
}

// mark returns text, which holds word, prefixed with word's icon and styled.
func (m markMode) mark(word, text string) string {
	if m == markPlain {
		return text
	}
	s := markFor(word)
	text = s.icon + " " + text
	if m == markColor {
		return s.style.Render(text)
	}
	return text
}

// markAfter marks the first word that follows prefix in line.
func (m markMode) markAfter(line, prefix, word string) string {
	if m == markPlain || word == "" {
		return line
	}
	return strings.Replace(line, prefix+word, prefix+m.mark(word, word), 1)
}

// markLead marks the token before the first colon of line when it is a state word.
func (m markMode) markLead(line string) string {
	word, rest, ok := strings.Cut(line, ":")
	if _, known := stateMarks[word]; m == markPlain || !ok || !known {
		return line
	}
	return m.mark(word, word) + ":" + rest
}
