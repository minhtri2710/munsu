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

func (m dashboardModel) age() string {
	return fmt.Sprintf("%ds", int(m.now().Sub(m.lastGood)/time.Second))
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

func (m dashboardModel) frame() string {
	m.resize()
	lines := m.header()
	lines = append(lines, m.notes()...)
	lines = append(lines, m.body()...)
	lines = append(lines, m.feedSection()...)
	if m.mode == modeConfirm {
		lines = append(lines, strings.Split(m.argv.View(), "\n")...)
	}
	lines = append(lines, m.footer()...)

	// The one bound left on the frame: header, notes, footer and the row
	// titles are plain lines no component bounds, and a terminal shorter than
	// header plus footer cannot hold them. Every component is sized to fit the
	// rest, so nothing else is cut here.
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	lines = lines[:min(len(lines), m.height)]
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m dashboardModel) header() []string {
	title := dashBold.Render("munsu dashboard") + " " + dashFaint.Render(dashText(m.home))
	var badge, counts string
	switch m.state() {
	case stateLoading:
		badge = dashYellow.Render("LOADING")
	case stateFailed:
		badge = dashRed.Render("FAILED " + dashText(fmt.Sprint(m.readErr)))
	case stateStale:
		badge = dashRed.Render("STALE last good read " + m.age() + " ago")
	case stateEmpty:
		badge = dashGreen.Render("EMPTY refreshed " + m.age() + " ago")
	case statePartial:
		badge = dashYellow.Render("PARTIAL refreshed " + m.age() + " ago")
	default:
		badge = dashGreen.Render("REFRESHED " + m.age() + " ago")
	}
	if m.reading && m.state() != stateLoading {
		badge += dashFaint.Render(" (reading)")
	}
	if !m.lastGood.IsZero() {
		counts = fmt.Sprintf("unresolved %d  Human-needed %d  failed sources %d", m.unresolved(), m.humanNeeded(), len(m.failures))
	}
	return []string{title + "  " + badge, counts}
}

// notes are the lines between the header and the body.
func (m dashboardModel) notes() []string {
	if m.state() == stateStale && m.readErr != nil {
		return []string{dashRed.Render("Last read failed: " + dashText(m.readErr.Error()))}
	}
	return nil
}

// body is the list section: a title that counts what the list draws, then the
// list. The count is the list's own page, not window arithmetic here.
func (m dashboardModel) body() []string {
	if m.listH < 1 {
		return nil
	}
	switch m.state() {
	case stateLoading:
		return []string{dashFaint.Render("Loading fleet...")}
	case stateFailed:
		return []string{dashRed.Render("Fleet read failed: " + dashText(fmt.Sprint(m.readErr)))}
	case stateEmpty:
		return []string{dashFaint.Render("No tasks and no failed sources.")}
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
	title := "Tasks"
	if len(m.failures) > 0 {
		title = "Failed sources and tasks"
	}
	if drawn < total {
		title += fmt.Sprintf(" - Showing %d of %d (page %d of %d)", drawn, total, l.Paginator.Page+1, l.Paginator.TotalPages)
	}
	return append([]string{dashBold.Render(title)}, rows...)
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

// feedSection is the event feed: a title that counts what the viewport draws,
// then the viewport.
func (m dashboardModel) feedSection() []string {
	if m.feedH < 1 {
		return nil
	}
	t := "Events"
	if shown, all := m.feed.VisibleLineCount(), m.feed.TotalLineCount(); shown < all {
		t += fmt.Sprintf(" - Showing %d of the last %d read", shown, all)
	}
	var title string
	if m.eventErr != nil {
		title = dashRed.Render(t + " - unreadable: " + dashText(m.eventErr.Error()))
	} else {
		if m.skipped > 0 {
			t += fmt.Sprintf(" (%d malformed lines skipped)", m.skipped)
		}
		title = dashBold.Render(t)
	}
	out := []string{title}
	if p := m.feedPlaceholder(); p != "" && m.feedH > 1 {
		out = append(out, p)
	}
	if m.feed.Height() > 0 {
		out = append(out, strings.Split(m.feed.View(), "\n")...)
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

func (m dashboardModel) footer() []string {
	var out []string
	switch m.mode {
	case modeForm:
		p := m.pending
		out = append(out, dashBold.Render(p.action.name))
		for i, f := range p.action.fields {
			cur := "  "
			if i == p.field {
				cur = "> "
			}
			req := ""
			if f.required {
				req = " (required)"
			}
			out = append(out, fieldView(p.inputs[i], cur+f.label+req+": ", m.width))
		}
		out = append(out, dashFaint.Render("enter next/submit  esc cancel"))
	case modeConfirm:
		switch {
		case m.argvShown():
			out = append(out, dashFaint.Render("The whole command is shown."))
		case m.argv.Height() >= 1 && !m.argv.AtBottom():
			out = append(out, dashRed.Render("Scroll down (down, pgdn): y runs only once the whole command is shown."))
		default:
			out = append(out, dashRed.Render("Terminal too small to show the whole command: enlarge it, or esc to cancel."))
		}
		out = append(out, dashFaint.Render("y run  esc cancel"))
	case modeRunning:
		out = append(out, dashFaint.Render("Running..."))
	default:
		if r := m.result; r != nil {
			head := fmt.Sprintf("exit %d: %s", r.exitCode(), argvLine(r.argv))
			if r.exitCode() == 0 {
				out = append(out, dashGreen.Render(head))
			} else {
				out = append(out, dashRed.Render(head))
			}
			if r.err != nil && r.exitCode() < 0 {
				out = append(out, dashRed.Render(dashText(r.err.Error())))
			}
			for _, l := range r.outputTail() {
				out = append(out, dashText(l))
			}
		}
		out = append(out, m.helpLines()...)
	}
	if m.notice != "" {
		out = append(out, dashRed.Render(dashText(m.notice)))
	}
	return out
}
