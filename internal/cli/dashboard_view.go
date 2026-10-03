package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

var (
	dashBold   = lipgloss.NewStyle().Bold(true)
	dashFaint  = lipgloss.NewStyle().Faint(true)
	dashRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dashGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dashYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dashSelect = lipgloss.NewStyle().Reverse(true)
)

func (m dashboardModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

// phaseStyle never returns green for an unknown phase. A stale row's phase
// carries a " [stale]" suffix, matches no case and renders faint.
func phaseStyle(phase string) lipgloss.Style {
	switch phase {
	case "working", "alive", "done", "resolved":
		return dashGreen
	case "blocked", "dead":
		return dashRed
	case "queued", "registered":
		return dashYellow
	}
	return dashFaint
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// dashText makes text the dashboard did not write safe to draw: every
// non-printable rune (control, format such as a bidi override, invalid UTF-8)
// is shown as an escape, never emitted raw and never dropped. Apply it where
// data enters a line, before any styling.
func dashText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r != ' ' && !strconv.IsPrint(r):
			q := strconv.QuoteToASCII(string(r))
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}

func quoteArg(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\r\n\"'\\$`") || dashText(s) != s {
		return strconv.Quote(s)
	}
	return s
}

func argvLine(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = quoteArg(a)
	}
	return strings.Join(q, " ")
}

// feedMax is the lines of the feed section, title included: the terminal
// height sets how much of the feed a frame tries to show.
func (m dashboardModel) feedMax() int {
	if m.height >= 24 {
		return 6
	}
	return 3
}

// resize sizes every component from the terminal size and the lines the
// header, notes and footer take. The confirm viewport gets its rows first, so
// the command the Human approves is never what gives way; then the feed, then
// the list. A section with no rows left is not drawn.
func (m *dashboardModel) resize() {
	w := m.width
	m.help.SetWidth(w)
	m.argv.SetWidth(w)
	m.feed.SetWidth(w)
	avail := max(0, m.height-len(m.header())-len(m.notes())-len(m.footer()))
	if m.mode == modeConfirm {
		rows := min(m.argv.TotalLineCount(), avail)
		m.argv.SetHeight(rows)
		avail -= rows
	}
	m.feedH = min(m.feedMax(), avail/2)
	m.listH = avail - m.feedH

	m.feed.SetContentLines(m.feedLines(w))
	rows := max(0, m.feedH-1)
	if m.feedPlaceholder() != "" {
		rows--
	}
	m.feed.SetHeight(max(0, min(rows, m.feed.TotalLineCount())))
	m.feed.GotoBottom()

	m.list.SetSize(w, max(1, min(m.listH-1, len(m.list.Items()))))
}

// feedLines is one fitted line per event; the viewport owns which of them
// show. A line longer than the terminal is cut with a visible ellipsis here,
// since the viewport would cut it silently.
func (m dashboardModel) feedLines(w int) []string {
	lines := make([]string, len(m.events))
	for i, e := range m.events {
		lines[i] = ansi.Truncate(eventLine(e), w, "…")
	}
	return lines
}

// dashKeep is a protected part: a state or a count the Human must read whole.
// Only the constructors below build one, from typed values and never from a
// free string, so no free text is protected by accident. The zero value is no
// protected part.
type dashKeep struct{ text string }

// keepState is "munsu dashboard" and the state phrase with the time since the
// last good read; reading adds the faint " (reading)".
func keepState(s dashState, since time.Duration, reading bool) dashKeep {
	age := fmt.Sprintf("%ds", int(since/time.Second))
	var phrase string
	switch s {
	case stateLoading:
		phrase = dashYellow.Render("LOADING")
	case stateFailed:
		phrase = dashRed.Render("FAILED")
	case stateStale:
		phrase = dashRed.Render("STALE last good read " + age + " ago")
	case stateEmpty:
		phrase = dashGreen.Render("EMPTY refreshed " + age + " ago")
	case statePartial:
		phrase = dashYellow.Render("PARTIAL refreshed " + age + " ago")
	default:
		phrase = dashGreen.Render("REFRESHED " + age + " ago")
	}
	if reading {
		phrase += dashFaint.Render(" (reading)")
	}
	return dashKeep{dashBold.Render("munsu dashboard") + " " + phrase}
}

func keepCounts(unresolved, humanNeeded, failed int) dashKeep {
	return dashKeep{fmt.Sprintf("unresolved %d  Human-needed %d  failed sources %d", unresolved, humanNeeded, failed)}
}

// dashStem is a fixed stem or line, the closed set of constant protected texts.
type dashStem int

const (
	stemLastReadFailed dashStem = iota + 1
	stemFleetReadFailed
	stemLoadingFleet
	stemNoTasks
)

func keepStem(s dashStem) dashKeep {
	switch s {
	case stemLastReadFailed:
		return dashKeep{dashRed.Render("Last read failed:")}
	case stemFleetReadFailed:
		return dashKeep{dashRed.Render("Fleet read failed:")}
	case stemLoadingFleet:
		return dashKeep{dashFaint.Render("Loading fleet...")}
	case stemNoTasks:
		return dashKeep{dashFaint.Render("No tasks and no failed sources.")}
	}
	return dashKeep{}
}

// keepListTitle names the list and, when a page clips it, counts what it draws.
func keepListTitle(failures bool, drawn, total, page, pages int) dashKeep {
	title := "Tasks"
	if failures {
		title = "Failed sources and tasks"
	}
	if drawn < total {
		title += fmt.Sprintf(" - Showing %d of %d (page %d of %d)", drawn, total, page, pages)
	}
	return dashKeep{dashBold.Render(title)}
}

// keepFeedTitle is "Events", the count of what the viewport draws when it
// draws fewer lines than it holds, the malformed lines note, and " - unreadable:"
// when the last event read failed.
func keepFeedTitle(shown, read, skipped int, unreadable bool) dashKeep {
	t := "Events"
	if shown < read {
		t += fmt.Sprintf(" - Showing %d of the last %d read", shown, read)
	}
	if skipped > 0 {
		t += fmt.Sprintf(" (%d malformed lines skipped)", skipped)
	}
	if unreadable {
		return dashKeep{dashRed.Render(t + " - unreadable:")}
	}
	return dashKeep{dashBold.Render(t)}
}

// keepExit is the result stem "exit N:", green for a clean exit.
func keepExit(code int) dashKeep {
	style := dashRed
	if code == 0 {
		style = dashGreen
	}
	return dashKeep{style.Render(fmt.Sprintf("exit %d:", code))}
}

// dashLine is one frame line: keep is the protected part and rest is free text
// that may be cut.
type dashLine struct {
	keep dashKeep
	rest string
}

func (l dashLine) String() string { return l.keep.text + l.rest }

func dashFree(lines ...string) []dashLine {
	out := make([]dashLine, len(lines))
	for i, l := range lines {
		out[i] = dashLine{rest: l}
	}
	return out
}

// layout is every line of the frame, top to bottom, cut to the terminal height.
func (m dashboardModel) layout() []dashLine {
	m.resize()
	lines := m.header()
	lines = append(lines, m.notes()...)
	lines = append(lines, m.body()...)
	lines = append(lines, m.feedSection()...)
	if m.mode == modeConfirm {
		lines = append(lines, dashFree(strings.Split(m.argv.View(), "\n")...)...)
	}
	lines = append(lines, m.footer()...)
	return lines[:min(len(lines), m.height)]
}

// fits reports whether every protected part of the drawn lines is drawn whole:
// at most the terminal's width, and short of it when free text follows that
// the width cuts, since the cut's "…" takes the last cell.
func (m dashboardModel) fits() bool {
	for _, l := range m.layout() {
		keep := lipgloss.Width(l.keep.text)
		if keep > m.width || keep == m.width && lipgloss.Width(l.String()) > m.width {
			return false
		}
	}
	return true
}

// onScreen is the gate for y: the whole command has been shown by the confirm
// viewport and the frame that draws it fits the terminal.
func (m dashboardModel) onScreen() bool {
	return m.argvShown() && m.fits()
}

// frame draws the dashboard. Each line is a protected part (a count or a
// state) followed by free text. When a drawn protected part is wider than the
// terminal the frame is one "Terminal too small" notice instead, never a count
// or state cut to "..."; otherwise only free text and the lines past the
// terminal's height are cut.
func (m dashboardModel) frame() string {
	lines := m.layout()
	if !m.fits() {
		if m.height < 1 {
			return ""
		}
		notice := "Terminal too small: enlarge it."
		if m.mode == modeForm || m.mode == modeConfirm {
			notice = "Terminal too small: enlarge it, or esc to cancel."
		}
		return dashRed.Render(ansi.Truncate(notice, m.width, "…"))
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l.String(), m.width, "…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

// header is the title line, state phrase first and the home path after it, and
// the counters line.
func (m dashboardModel) header() []dashLine {
	var counts dashKeep
	rest := ""
	if m.state() == stateFailed {
		rest = dashRed.Render(" " + dashText(fmt.Sprint(m.readErr)))
	}
	if !m.lastGood.IsZero() {
		counts = keepCounts(m.unresolved(), m.humanNeeded(), len(m.failures))
	}
	return []dashLine{
		{keep: keepState(m.state(), m.now().Sub(m.lastGood), m.reading && m.state() != stateLoading), rest: rest + "  " + dashFaint.Render(dashText(m.home))},
		{keep: counts},
	}
}

// notes are the lines between the header and the body.
func (m dashboardModel) notes() []dashLine {
	if m.state() == stateStale && m.readErr != nil {
		return []dashLine{{keep: keepStem(stemLastReadFailed), rest: dashRed.Render(" " + dashText(m.readErr.Error()))}}
	}
	return nil
}

// body is the list section: a title that counts what the list draws, then the
// list. The count is the list's own page, not window arithmetic here.
func (m dashboardModel) body() []dashLine {
	if m.listH < 1 {
		return nil
	}
	switch m.state() {
	case stateLoading:
		return []dashLine{{keep: keepStem(stemLoadingFleet)}}
	case stateFailed:
		return []dashLine{{keep: keepStem(stemFleetReadFailed), rest: dashRed.Render(" " + dashText(fmt.Sprint(m.readErr)))}}
	case stateEmpty:
		return []dashLine{{keep: keepStem(stemNoTasks)}}
	}
	total := len(m.list.Items())
	if total == 0 {
		return nil
	}
	l := m.list
	l.SetDelegate(dashRows{stale: m.state() == stateStale})
	drawn := 0
	var rows []string
	if m.listH > 1 {
		start, end := l.Paginator.GetSliceBounds(total)
		drawn = end - start
		rows = strings.Split(l.View(), "\n")
	}
	title := keepListTitle(len(m.failures) > 0, drawn, total, l.Paginator.Page+1, l.Paginator.TotalPages)
	return append([]dashLine{{keep: title}}, dashFree(rows...)...)
}

// dashRows draws a list item as one line: a failed source in red, a task row
// with its phase colour. The selected item is marked and reversed.
type dashRows struct{ stale bool }

func (dashRows) Height() int                         { return 1 }
func (dashRows) Spacing() int                        { return 0 }
func (dashRows) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d dashRows) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it := item.(dashItem)
	mark := "  "
	if index == m.Index() {
		mark = "> "
	}
	if it.failure != nil {
		line := mark + "x " + dashText(it.failure.Source) + "  " + dashText(fmt.Sprint(it.failure.Err))
		if index == m.Index() {
			fmt.Fprint(w, dashSelect.Render(line))
			return
		}
		fmt.Fprint(w, dashRed.Render(line))
		return
	}
	ts := it.row
	phase := dashText(fleet.PhaseFromProjection(*ts))
	if d.stale {
		phase += " [stale]"
	}
	status := dashText(ts.CurrentDescription)
	if status == "" {
		status = dashText(ts.LastStatus)
	}
	hn := "  "
	if fleet.HumanNeeded(*ts) {
		hn = "! "
	}
	src := dashText(ts.Source)
	if src == "" {
		src = "primary"
	}
	id, kind := dashText(ts.ID), dashText(ts.Kind)
	if index == m.Index() {
		fmt.Fprint(w, dashSelect.Render(mark+hn+pad(id, 22)+" "+pad(phase, 16)+" "+pad(kind, 7)+" "+pad(src, 16)+" "+status))
		return
	}
	fmt.Fprint(w, mark+hn+pad(id, 22)+" "+phaseStyle(phase).Render(pad(phase, 16))+" "+pad(kind, 7)+" "+pad(src, 16)+" "+status)
}

// feedPlaceholder is the line shown in place of events while there are none.
func (m dashboardModel) feedPlaceholder() string {
	switch {
	case m.eventErr != nil:
		return ""
	case !m.feedLoaded:
		return dashFaint.Render("Loading events...")
	case len(m.events) == 0:
		return dashFaint.Render("No events yet.")
	}
	return ""
}

// feedSection is the event feed: a title, then the viewport. The title is
// composed once: "Events", the count of what the viewport draws, the malformed
// lines note, and on a read error " - unreadable:" with the error as free text.
func (m dashboardModel) feedSection() []dashLine {
	if m.feedH < 1 {
		return nil
	}
	title := dashLine{keep: keepFeedTitle(m.feed.VisibleLineCount(), m.feed.TotalLineCount(), m.skipped, m.eventErr != nil)}
	if m.eventErr != nil {
		title.rest = dashRed.Render(" " + dashText(m.eventErr.Error()))
	}
	out := []dashLine{title}
	if p := m.feedPlaceholder(); p != "" && m.feedH > 1 {
		out = append(out, dashLine{rest: p})
	}
	if m.feed.Height() > 0 {
		out = append(out, dashFree(strings.Split(m.feed.View(), "\n")...)...)
	}
	return out
}

func eventLine(e orchestrator.Record) string {
	at := time.Unix(0, e.Timestamp).UTC().Format("15:04:05")
	return strings.TrimRight(fmt.Sprintf("%s %s %s %s %s", at, dashText(e.Type), dashText(e.Producer), dashText(e.Key), dashText(e.Payload)), " ")
}

// helpLines are the key footer, built from the action table. The first line
// holds the movement keys and the task actions, the second the rest.
func (m dashboardModel) helpLines() []string {
	move := key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("j/k", "move"))
	quit := key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	first, second := []key.Binding{move}, []key.Binding{}
	for i := range dashActions {
		if i < 8 {
			first = append(first, dashActions[i].keyBinding())
		} else {
			second = append(second, dashActions[i].keyBinding())
		}
	}
	return []string{m.help.ShortHelpView(first), m.help.ShortHelpView(append(second, quit))}
}

// fieldView draws one form field. A value holding text the dashboard must not
// draw raw (a pasted bidi override, say) is shown escaped, with the cursor at
// its end; the field keeps the real value.
func fieldView(in textinput.Model, prompt string, w int) string {
	in.Prompt = prompt
	in.SetWidth(max(1, w-lipgloss.Width(prompt)-1))
	if v := in.Value(); dashText(v) != v {
		in.SetValue(dashText(v))
		in.CursorEnd()
	}
	return in.View()
}

func (m dashboardModel) footer() []dashLine {
	var out []dashLine
	switch m.mode {
	case modeForm:
		p := m.pending
		out = append(out, dashLine{rest: dashBold.Render(p.action.name)})
		for i, f := range p.action.fields {
			cur := "  "
			if i == p.field {
				cur = "> "
			}
			req := ""
			if f.required {
				req = " (required)"
			}
			out = append(out, dashLine{rest: fieldView(p.inputs[i], cur+f.label+req+": ", m.width)})
		}
		out = append(out, dashLine{rest: dashFaint.Render("enter next/submit  esc cancel")})
	case modeConfirm:
		switch {
		case m.argvShown():
			out = append(out, dashLine{rest: dashFaint.Render("The whole command is shown.")})
		case m.argv.Height() >= 1 && !m.argv.AtBottom():
			out = append(out, dashLine{rest: dashRed.Render("Scroll down (down, pgdn): y runs only once the whole command is shown.")})
		default:
			out = append(out, dashLine{rest: dashRed.Render("Terminal too small to show the whole command: enlarge it, or esc to cancel.")})
		}
		out = append(out, dashLine{rest: dashFaint.Render("y run  esc cancel")})
	case modeRunning:
		out = append(out, dashLine{rest: dashFaint.Render("Running...")})
	default:
		if r := m.result; r != nil {
			style := dashRed
			if r.exitCode() == 0 {
				style = dashGreen
			}
			out = append(out, dashLine{keep: keepExit(r.exitCode()), rest: style.Render(" " + argvLine(r.argv))})
			if r.err != nil && r.exitCode() < 0 {
				out = append(out, dashLine{rest: dashRed.Render(dashText(r.err.Error()))})
			}
			for _, l := range r.outputTail() {
				out = append(out, dashLine{rest: dashText(l)})
			}
		}
		out = append(out, dashFree(m.helpLines()...)...)
	}
	if m.notice != "" {
		out = append(out, dashLine{rest: dashRed.Render(dashText(m.notice))})
	}
	return out
}
