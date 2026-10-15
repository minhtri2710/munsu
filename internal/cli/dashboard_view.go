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
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

var (
	dashBold   = lipgloss.NewStyle().Bold(true)
	dashSelect = lipgloss.NewStyle().Reverse(true)
)

func (m dashboardModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
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
// header, notes, NEEDS YOU and footer take. The confirm viewport gets its rows
// first, so the command the Human approves is never what gives way; then the
// feed, then the detail pane when the list keeps its title and two rows, then
// the list. A section with no rows left is not drawn.
func (m *dashboardModel) resize() {
	w := m.width
	m.argv.SetWidth(w)
	m.feed.SetWidth(w)
	avail := max(0, m.height-len(m.header())-len(m.notes())-len(m.needsSection())-len(m.footer()))
	if m.mode == modeConfirm {
		rows := min(m.argv.TotalLineCount(), avail)
		m.argv.SetHeight(rows)
		m.argv.SetYOffset(m.argv.YOffset()) // the setters never clamp the offset
		avail -= rows
	}
	m.feedH = min(m.feedMax(), avail/2)
	rest := avail - m.feedH
	m.detailH = 0
	if d := len(m.detail()); rest >= d+3 {
		m.detailH = d
	}
	m.listH = rest - m.detailH

	m.feed.SetContentLines(m.feedLines(w))
	rows := max(0, m.feedH-1)
	if m.feedPlaceholder() != "" {
		rows--
	}
	m.feed.SetHeight(max(0, min(rows, m.feed.TotalLineCount())))
	m.feed.GotoBottom()

	m.list.SetSize(w, max(1, min(m.listH-1, len(m.list.Items()))))
}

// columnRows is 1 when the list section has a line to spare for the column
// header: it never takes a row from the list.
func (m dashboardModel) columnRows() int {
	if m.listH-1 > len(m.list.Items()) {
		return 1
	}
	return 0
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
		phrase = styleWarn.Render("LOADING")
	case stateFailed:
		phrase = styleFail.Render("FAILED")
	case stateStale:
		phrase = styleFail.Render("STALE last good read " + age + " ago")
	case stateEmpty:
		phrase = styleOK.Render("EMPTY refreshed " + age + " ago")
	case statePartial:
		phrase = styleWarn.Render("PARTIAL refreshed " + age + " ago")
	default:
		phrase = styleOK.Render("REFRESHED " + age + " ago")
	}
	if reading {
		phrase += styleMuted.Render(" (reading)")
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
	stemNothingNeedsYou
)

func keepStem(s dashStem) dashKeep {
	switch s {
	case stemLastReadFailed:
		return dashKeep{styleFail.Render("Last read failed:")}
	case stemFleetReadFailed:
		return dashKeep{styleFail.Render("Fleet read failed:")}
	case stemLoadingFleet:
		return dashKeep{styleMuted.Render("Loading fleet...")}
	case stemNoTasks:
		return dashKeep{styleMuted.Render("No tasks and no failed sources.")}
	case stemNothingNeedsYou:
		return dashKeep{styleMuted.Render("Nothing needs you.")}
	}
	return dashKeep{}
}

// keepNeedsTitle is "NEEDS YOU" and its count, with the shown count when the
// section is capped.
func keepNeedsTitle(shown, total int) dashKeep {
	title := fmt.Sprintf("NEEDS YOU %d", total)
	if shown < total {
		title += fmt.Sprintf(" - showing %d", shown)
	}
	return dashKeep{styleFail.Bold(true).Render(title)}
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
		return dashKeep{styleFail.Render(t + " - unreadable:")}
	}
	return dashKeep{dashBold.Render(t)}
}

// keepExit is the result stem "exit N:", green for a clean exit.
func keepExit(code int) dashKeep {
	style := styleFail
	if code == 0 {
		style = styleOK
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
	lines = append(lines, m.needsSection()...)
	lines = append(lines, m.body()...)
	if m.detailH > 0 {
		lines = append(lines, m.detail()...)
	}
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

// argvRows reports whether the confirm viewport has a row and a width to draw
// in. footer calls it directly because fits() lays the footer out.
func (m dashboardModel) argvRows() bool {
	return m.argv.Height() >= 1 && m.argv.Width() >= 1
}

// argvDrawn reports whether the confirm viewport's rows are drawn: the frame
// fits and the viewport has a row. Scroll keys move the viewport only then.
func (m dashboardModel) argvDrawn() bool {
	return m.argvRows() && m.fits()
}

// onScreen is the gate for y: the viewport's rows are drawn and it is at its
// bottom, so the whole command has been shown.
func (m dashboardModel) onScreen() bool {
	return m.argvDrawn() && m.argv.AtBottom()
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
		return styleFail.Render(ansi.Truncate(notice, m.width, "…"))
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
		rest = styleFail.Render(" " + dashText(fmt.Sprint(m.readErr)))
	}
	chips := ""
	if !m.lastGood.IsZero() {
		counts = keepCounts(m.unresolved(), m.humanNeeded(), len(m.failures))
		chips = m.chips()
	}
	return []dashLine{
		{keep: keepState(m.state(), m.now().Sub(m.lastGood), m.reading && m.state() != stateLoading), rest: rest + chips + "  " + styleMuted.Render(dashText(m.home))},
		{keep: counts},
	}
}

// chips are the title line's fleet summary after the state: the watcher beat,
// the captains with rows, the working tasks and the unacked wakes.
func (m dashboardModel) chips() string {
	watcher := styleOK.Render("watcher " + m.beat.Age.Round(time.Second).String())
	switch {
	case !m.beat.Exists:
		watcher = styleFail.Render("watcher absent")
	case m.beat.Stale:
		watcher = markFor("stale").style.Render("watcher stale " + m.beat.Age.Round(time.Second).String())
	}
	captains := map[string]bool{}
	working := 0
	for _, ts := range m.rows {
		if id, ok := strings.CutPrefix(ts.Source, "captain:"); ok {
			captains[id] = true
		}
		if taskauthority.Phase(ts.CurrentState) == taskauthority.PhaseWorking {
			working++
		}
	}
	for _, f := range m.failures {
		if id, ok := strings.CutPrefix(f.Source, "captain:"); ok {
			captains[id] = true
		}
	}
	unacked := strconv.Itoa(m.unacked)
	if m.unackedErr != nil {
		unacked = "?"
	}
	return "  " + watcher + fmt.Sprintf("  captains %d  working %d  unacked %s", len(captains), working, unacked)
}

// notes are the lines between the header and the body.
func (m dashboardModel) notes() []dashLine {
	if m.state() == stateStale && m.readErr != nil {
		return []dashLine{{keep: keepStem(stemLastReadFailed), rest: styleFail.Render(" " + dashText(m.readErr.Error()))}}
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
		return []dashLine{{keep: keepStem(stemFleetReadFailed), rest: styleFail.Render(" " + dashText(fmt.Sprint(m.readErr)))}}
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
	if m.columnRows() == 1 {
		rows = append([]string{styleMuted.Render(dashColumns)}, rows...)
	}
	return append([]dashLine{{keep: title}}, dashFree(rows...)...)
}

// dashColumns heads the seat table; dashSeatLine lays a row out the same way.
var dashColumns = dashSeatLine("    ", "GROUP", "NAME", "STATE", "ROLE", "HARNESS", "MODEL", "STATUS")

func dashSeatLine(lead, group, name, state, role, harness, model, status string) string {
	return lead + pad(group, 12) + " " + pad(name, 20) + " " + pad(state, 16) + " " + pad(role, 6) + " " + pad(harness, 7) + " " + pad(model, 14) + " " + status
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
		fmt.Fprint(w, styleFail.Render(line))
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
	group := ""
	if index == m.Paginator.Page*m.Paginator.PerPage || dashGroup(m.Items()[index-1].(dashItem)) != dashGroup(it) {
		group = dashText(dashGroup(it))
	}
	harness, model := dashText(dashOr(ts.Harness)), dashText(dashOr(ts.Model))
	id, kind := dashText(ts.ID), dashText(ts.Kind)
	if index == m.Index() {
		fmt.Fprint(w, dashSelect.Render(ansi.Truncate(dashSeatLine(mark+hn, group, id, phase, kind, harness, model, status), m.Width(), "…")))
		return
	}
	fmt.Fprint(w, ansi.Truncate(mark+hn+pad(group, 12)+" "+pad(id, 20)+" "+markFor(phase).style.Render(pad(phase, 16))+" "+pad(kind, 6)+" "+pad(harness, 7)+" "+pad(model, 14)+" "+status, m.Width(), "…"))
}

// dashGroup is the group a row is listed under: "general" for the primary
// home, the captain id for a captain's home, "" for a failed source.
func dashGroup(it dashItem) string {
	if it.row == nil {
		return ""
	}
	if id, ok := strings.CutPrefix(it.row.Source, "captain:"); ok {
		return id
	}
	return "general"
}

func dashOr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// feedPlaceholder is the line shown in place of events while there are none.
func (m dashboardModel) feedPlaceholder() string {
	switch {
	case m.eventErr != nil:
		return ""
	case !m.feedLoaded:
		return styleMuted.Render("Loading events...")
	case len(m.events) == 0:
		return styleMuted.Render("No events yet.")
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
		title.rest = styleFail.Render(" " + dashText(m.eventErr.Error()))
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

// helpLines are the key footer: the movement entry, every action of the table,
// decide and quit, in that order. An entry the selection cannot carry (bind
// finds a problem, the test begin applies) is dimmed; its key still reaches
// begin and gets its notice.
func (m dashboardModel) helpLines() []string {
	hasTarget := len(m.list.Items()) > 0
	move := dashMove
	move.SetEnabled(len(m.list.Items()) >= 2)
	entries := []key.Binding{move}
	for i := range dashActions {
		b := dashActions[i].keys
		_, problem := dashActions[i].bind(m.sel, hasTarget, m.home)
		b.SetEnabled(problem == "")
		entries = append(entries, b)
	}
	decide := dashDecide
	held := false
	if it, ok := m.list.SelectedItem().(dashItem); ok && it.row != nil {
		_, _, held = heldDecision(*it.row)
	}
	decide.SetEnabled(held)
	return m.keyLines(append(entries, decide, dashQuit)...)
}

// keyLines draws key entries, each its key and label, a disabled one dimmed.
// Entries fill a line while it fits the terminal; the next one starts a new
// line, and no entry is split.
func (m dashboardModel) keyLines(entries ...key.Binding) []string {
	var lines []string
	line, width := "", 0
	for _, e := range entries {
		h := e.Help()
		text := styleAccent.Render(h.Key) + " " + h.Desc
		if !e.Enabled() {
			text = styleMuted.Render(h.Key + " " + h.Desc)
		}
		w := lipgloss.Width(text)
		if line != "" && m.width > 0 && width+2+w > m.width {
			lines = append(lines, line)
			line, width = "", 0
		}
		if line != "" {
			line += "  "
			width += 2
		}
		line += text
		width += w
	}
	return append(lines, line)
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
		out = append(out, dashFree(m.keyLines(dashFormNext, dashFormCancel)...)...)
	case modeConfirm:
		switch {
		case m.argvRows() && m.argv.AtBottom():
			out = append(out, dashLine{rest: styleMuted.Render("The whole command is shown.")})
		case m.argv.Height() >= 1 && !m.argv.AtBottom():
			out = append(out, dashLine{rest: styleFail.Render("Scroll down (down, pgdn): y runs only once the whole command is shown.")})
		default:
			out = append(out, dashLine{rest: styleFail.Render("Terminal too small to show the whole command: enlarge it, or esc to cancel.")})
		}
		run := dashRun
		run.SetEnabled(m.argvRows() && m.argv.AtBottom())
		out = append(out, dashFree(m.keyLines(run, dashRunCancel)...)...)
	case modeRunning:
		out = append(out, dashLine{rest: styleMuted.Render("Running...")})
	default:
		if r := m.result; r != nil {
			style := styleFail
			if r.exitCode() == 0 {
				style = styleOK
			}
			out = append(out, dashLine{keep: keepExit(r.exitCode()), rest: style.Render(" " + argvLine(r.argv))})
			if r.err != nil && r.exitCode() < 0 {
				out = append(out, dashLine{rest: styleFail.Render(dashText(r.err.Error()))})
			}
			for _, l := range r.outputTail() {
				out = append(out, dashLine{rest: dashText(l)})
			}
		}
		out = append(out, dashFree(m.helpLines()...)...)
	}
	if m.notice != "" {
		out = append(out, dashLine{rest: styleFail.Render(dashText(m.notice))})
	}
	return out
}
