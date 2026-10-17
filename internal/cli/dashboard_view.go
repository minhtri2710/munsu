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
	dashBold    = lipgloss.NewStyle().Bold(true)
	dashBrand   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("4"))
	dashKey     = styleAccent.Bold(true)
	dashHelpSep = styleMuted.Render(" • ")
	dashFocus   = lipgloss.NewStyle().Foreground(styleAccent.GetForeground())
)

// dashSideMin is the terminal width from which Detail sits beside Tasks.
const dashSideMin = 140

// dashLook is how the sections are drawn: flat titled lines, bordered panels
// stacked, or bordered panels with Detail beside Tasks.
type dashLook int

const (
	lookFlat dashLook = iota
	lookBoxed
	lookSide
)

func (m dashboardModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

func (m dashboardModel) theme() dashTheme { return dashThemeFor(m.dark) }

// border is the rows below and the columns beside a panel its border takes.
func (m dashboardModel) border() int {
	if m.look == lookFlat {
		return 0
	}
	return 1
}

// sideW is the width of the Tasks panel when Detail sits beside it.
func (m dashboardModel) sideW() int {
	return m.width - max(50, m.width*3/10)
}

// listW is the width the list rows are drawn in.
func (m dashboardModel) listW() int {
	if m.look == lookSide {
		return m.sideW() - 4
	}
	return m.width - 4*m.border()
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

// resize picks the look and sizes every component for it. The richest look is
// tried first: Detail beside Tasks, then stacked panels, then flat sections. A
// bordered look is taken only when it draws every section the flat look draws
// and every protected part fits inside its border, so the split and then the
// borders give way before a protected part does. It returns the header
// lines, which no look or size changes.
func (m *dashboardModel) resize() (top []dashLine) {
	top = m.header()
	fixed := len(top) + len(m.footer())
	needs, events := m.needsLines(dashNeedsMax+1), m.eventLines()
	flat := *m
	flat.size(lookFlat, fixed, needs, events)
	for _, look := range []dashLook{lookSide, lookBoxed} {
		if look == lookSide && m.width < dashSideMin {
			continue
		}
		c := *m
		c.size(look, fixed, needs, events)
		if c.shown() == flat.shown() && fitsLines(c.sections()) {
			*m = c
			return top
		}
	}
	*m = flat
	return top
}

// shown is which of NEEDS YOU, the list and the feed have lines.
func (m dashboardModel) shown() [3]bool {
	return [3]bool{m.needsH > 0, m.listH > 0, m.feedH > 0}
}

// size sizes every component for a look from the terminal size, the lines the
// header and footer take (fixed), NEEDS YOU at its longest and the
// event lines. The confirm viewport gets its rows first, so the command the
// Human approves is never what gives way; then the list's
// title with one row (when both fit), then the feed's title, then NEEDS YOU
// (fewer items, then its count line alone), then the rest of the feed, then
// the detail pane when the list keeps its title and two rows, then the list.
// A border takes a line below each panel, and a panel without room for both
// borders is not drawn, as is a section with no lines left.
func (m *dashboardModel) size(look dashLook, fixed int, needs []dashLine, events []string) {
	w := m.width
	detail, held := m.detail()
	if look == lookSide && len(detail) == 0 {
		look = lookBoxed
	}
	m.look = look
	b := m.border()
	m.argv.SetWidth(w)
	m.feed.SetWidth(max(0, w-4*b))
	avail := max(0, m.height-fixed)
	if m.mode == modeConfirm {
		rows := min(m.argv.TotalLineCount(), avail)
		m.argv.SetHeight(rows)
		m.argv.SetYOffset(m.argv.YOffset()) // the setters never clamp the offset
		avail -= rows
	}
	items := len(m.list.Items())
	listMin := min(avail, 1)
	if items > 0 {
		listMin = 0
		if avail >= 2+b {
			listMin = 2 + b
		}
	}
	feedMin := 0
	if listMin > 0 && avail >= listMin+1+b {
		feedMin = 1 + b
	}
	boxNeeds := b == 1 && len(needs) > 0
	n := len(needs)
	if boxNeeds {
		n++
	}
	m.needsH = min(n, avail-listMin-feedMin)
	if boxNeeds && m.needsH < 2 {
		m.needsH = 0
	}
	avail -= m.needsH
	m.feedH = max(feedMin, min(m.feedMax()+b, avail/2, avail-listMin))
	if m.feedH < 1+b {
		m.feedH = 0
	}
	rest := avail - m.feedH
	m.detailH = 0
	cost := len(detail)
	if b == 1 || held {
		cost += 2
	}
	if look != lookSide && len(detail) > 0 && rest >= cost+3+b {
		m.detailH = cost
	}
	m.listH = rest - m.detailH
	if b == 1 && items > 0 && m.listH < 3 {
		m.listH = 0
	}

	fitted := make([]string, len(events))
	for i, e := range events {
		if e != "" {
			fitted[i] = ansi.Truncate(e, w-4*b, "…")
		}
	}
	m.feed.SetContentLines(fitted)
	rows := max(0, m.feedH-1-b)
	if m.feedPlaceholder() != "" {
		rows--
	}
	m.feed.SetHeight(max(0, min(rows, m.feed.TotalLineCount())))
	m.feed.GotoBottom()

	m.list.SetSize(max(1, m.listW()), max(1, min(m.listH-1-b, items)))
}

// columnRows is the lines the list section has to spare for the column
// header and, with two, its rule: they never take a row from the list.
func (m dashboardModel) columnRows() int {
	return min(2, max(0, m.listH-1-m.border()-len(m.list.Items())))
}

// eventLines is one line per shown event; the viewport owns which of them
// show. It always shows its newest lines, at most feedMax, so only those are
// drawn here. size cuts a line longer than the feed's width with a visible
// ellipsis, since the viewport would cut it silently.
func (m dashboardModel) eventLines() []string {
	th := m.theme()
	events, _ := m.shownEvents()
	lines := make([]string, len(events))
	for i := max(0, len(events)-m.feedMax()); i < len(events); i++ {
		lines[i] = eventLine(events[i], m.now(), th)
	}
	return lines
}

// shownEvents are the newest events the feed shows, at most dashboardFeedMax,
// and how many events read it hides; it hides only while the last read is
// complete and fresh.
func (m dashboardModel) shownEvents() ([]orchestrator.Record, int) {
	var snap *fleet.DisplaySnapshot
	if s := m.state(); s == stateRefreshed || s == stateEmpty {
		snap = &fleet.DisplaySnapshot{Tasks: m.rows}
	}
	shown, hidden := dashShown(m.events, snap)
	return shown[max(0, len(shown)-dashboardFeedMax):], hidden
}

// dashShown is events less those of tasks the snapshot no longer has, and how
// many it left out. The log is never pruned, so it keeps events of removed
// tasks, and a record names no task, only its producer. An event is left out
// only when munsu appends its type with a task ID as the producer
// (orchestrator.ProducedByTask), its producer is not a captain ("captain:"),
// and no task of the snapshot has that ID. Producers of other types, such as
// munsu's own "munsu:" producers or any given to munsu event append, are never
// matched. A nil snapshot or one with a failed source is not known to hold
// every task, so nothing is left out.
func dashShown(events []orchestrator.Record, snap *fleet.DisplaySnapshot) ([]orchestrator.Record, int) {
	if snap == nil || len(snap.Failures) > 0 {
		return events, 0
	}
	tasks := make(map[string]bool, len(snap.Tasks))
	for _, ts := range snap.Tasks {
		tasks[ts.ID] = true
	}
	var out []orchestrator.Record
	for _, e := range events {
		if orchestrator.ProducedByTask(e.Type) && !strings.HasPrefix(e.Producer, "captain:") && !tasks[e.Producer] {
			continue
		}
		out = append(out, e)
	}
	return out, len(events) - len(out)
}

// dashKeep is a protected part: a state or a count the Human must read whole.
// Only the constructors below build one, from typed values and never from a
// free string, so no free text is protected by accident. The zero value is no
// protected part.
type dashKeep struct{ text string }

// keepState is the brand, the read state and the NEEDS YOU count on the header
// bar. A fresh read is a dim "refreshed Ns ago" and any other state a badge;
// reading adds the faint " (reading)". A NEEDS YOU count above zero follows
// as "needs you N".
func keepState(s dashState, since time.Duration, reading bool, needs int, th dashTheme) dashKeep {
	age := fmt.Sprintf("%ds", int(since/time.Second))
	dim := th.bar.Inherit(th.subtle)
	phrase, color := "", styleFail
	switch s {
	case stateLoading:
		phrase, color = "LOADING", styleWarn
	case stateStale:
		phrase = "STALE last good read " + age + " ago"
	case statePartial:
		phrase, color = "PARTIAL refreshed "+age+" ago", styleWarn
	}
	text := dashBrand.Render(" munsu ") + th.bar.Render(" ")
	if phrase == "" {
		text += dim.Render("refreshed " + age + " ago")
	} else {
		text += pillStyle(color).Render(" " + phrase + " ")
	}
	if reading {
		text += th.bar.Faint(true).Render(" (reading)")
	}
	if needs > 0 {
		text += dim.Render(" · ") + th.bar.Inherit(styleFail.Bold(true)).Render(fmt.Sprintf("needs you %d", needs))
	}
	return dashKeep{text}
}

// dashStem is a fixed stem or line, the closed set of constant protected texts.
type dashStem int

const (
	stemLoadingFleet dashStem = iota + 1
	stemNoTasks
)

func keepStem(s dashStem) dashKeep {
	switch s {
	case stemLoadingFleet:
		return dashKeep{styleMuted.Render("Loading fleet...")}
	case stemNoTasks:
		return dashKeep{styleMuted.Render("No tasks and no failed sources.")}
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

// keepFeedTitle is "Events", the lines the viewport draws of those it holds
// when it draws fewer, the hidden note, the malformed lines note, and
// " - unreadable:" when the last event read failed.
func keepFeedTitle(drawn, held, hidden, skipped int, unreadable bool) dashKeep {
	t := "Events"
	if drawn < held {
		t += fmt.Sprintf(" - Showing %d of %d", drawn, held)
	}
	if hidden > 0 {
		t += fmt.Sprintf(" (%d of removed tasks hidden)", hidden)
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
// that may be cut. room is the cells keep and rest are drawn in, and box, when
// set, draws the fitted text into its panel border.
type dashLine struct {
	keep dashKeep
	rest string
	room int
	box  func(text string) string
}

func (l dashLine) String() string { return l.keep.text + l.rest }

// render is the drawn line: the text cut to its room with a visible ellipsis,
// then boxed.
func (l dashLine) render() string {
	text := ansi.Truncate(l.String(), l.room, "…")
	if l.box != nil {
		return l.box(text)
	}
	return text
}

func dashFree(lines ...string) []dashLine {
	out := make([]dashLine, len(lines))
	for i, l := range lines {
		out[i] = dashLine{rest: l}
	}
	return out
}

// panel draws a section in a rounded border w cells wide: the title line set
// into the top border, then the body, padded with blank lines to n lines in
// all when n is larger.
func panel(title dashLine, body []dashLine, w, n int, border lipgloss.Style) []dashLine {
	top := title
	top.room = w - 5
	top.box = func(t string) string {
		return border.Render("╭─ ") + t + border.Render(" "+strings.Repeat("─", max(0, w-5-ansi.StringWidth(t)))+"╮")
	}
	out := []dashLine{top}
	for len(body) < n-2 {
		body = append(body, dashLine{})
	}
	for _, l := range body {
		l.room = w - 4
		l.box = func(t string) string {
			return border.Render("│ ") + t + strings.Repeat(" ", max(0, w-4-ansi.StringWidth(t))) + border.Render(" │")
		}
		out = append(out, l)
	}
	bottom := border.Render("╰" + strings.Repeat("─", max(0, w-2)) + "╯")
	return append(out, dashLine{room: w, box: func(string) string { return bottom }})
}

// beside draws each right panel line after the left panel line beside it;
// both panels have the same number of lines.
func beside(left, right []dashLine) []dashLine {
	for i := range left {
		box, r := left[i].box, right[i].render()
		left[i].box = func(t string) string { return box(t) + r }
	}
	return left
}

// layout is every line of the frame, top to bottom, cut to the terminal height.
func (m dashboardModel) layout() []dashLine {
	return m.lines(m.resize())
}

// lines lays the frame out below the header (top) in the look and
// sizes resize set; the footer follows the sizes. A line outside a panel has
// the terminal's width for room.
func (m dashboardModel) lines(top []dashLine) []dashLine {
	lines := append(top, m.sections()...)
	if m.mode == modeConfirm {
		lines = append(lines, dashFree(strings.Split(m.argv.View(), "\n")...)...)
	}
	lines = append(lines, m.footer()...)
	for i := range lines {
		if lines[i].box == nil {
			lines[i].room = m.width
		}
	}
	return lines[:min(len(lines), m.height)]
}

// sections are the lines whose look resize picks: NEEDS YOU, the list with
// the detail pane, and the feed.
func (m dashboardModel) sections() []dashLine {
	lines := m.needsSection()
	lines = append(lines, m.middle()...)
	lines = append(lines, m.feedSection()...)
	for i := range lines {
		if lines[i].box == nil {
			lines[i].room = m.width
		}
	}
	return lines
}

// fitsLines reports whether every protected part of the lines is drawn whole:
// at most its line's room, and short of it when free text follows that the
// room cuts, since the cut's "…" takes the last cell.
func fitsLines(lines []dashLine) bool {
	for _, l := range lines {
		keep := lipgloss.Width(l.keep.text)
		if keep > l.room || keep == l.room && lipgloss.Width(l.String()) > l.room {
			return false
		}
	}
	return true
}

func (m dashboardModel) fits() bool { return fitsLines(m.layout()) }

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
// state) followed by free text. When a drawn protected part is wider than its
// room the frame is one "Terminal too small" notice instead, never a count or
// state cut to "..."; otherwise only free text and the lines past the
// terminal's height are cut.
func (m dashboardModel) frame() string {
	lines := m.layout()
	if !fitsLines(lines) {
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
		out[i] = l.render()
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

// header is the header bar: brand, read state and NEEDS YOU count first, then
// the fleet chips and the home path at its right end.
func (m dashboardModel) header() []dashLine {
	th := m.theme()
	left, needs := "", 0
	if !m.lastGood.IsZero() {
		left, needs = m.chips(th), len(m.needs())
	}
	keep := keepState(m.state(), m.now().Sub(m.lastGood), m.reading && m.state() != stateLoading, needs, th)
	return []dashLine{{keep: keep, rest: m.barRest(lipgloss.Width(keep.text), left, th)}}
}

// barRest fills the header bar after its protected part: left, then the home
// path right-aligned with a cell to spare, cut from its start when the bar is
// short. With no room for the path the bar is left to the frame's cut.
func (m dashboardModel) barRest(keepW int, left string, th dashTheme) string {
	if left != "" {
		left = th.bar.Render("  ") + left
	}
	home := dashText(m.home)
	path := th.bar.Inherit(th.subtle)
	space := m.width - keepW - lipgloss.Width(left) - 1
	h := ansi.StringWidth(home)
	switch {
	case space >= h+2:
		return left + th.bar.Render(strings.Repeat(" ", space-h)) + path.Render(home) + th.bar.Render(" ")
	case space >= 4:
		return left + th.bar.Render("  ") + path.Render(ansi.TruncateLeft(home, h-space+3, "…")) + th.bar.Render(" ")
	}
	return left + th.bar.Render("  ") + path.Render(home)
}

// chips are the header bar's fleet summary after the state: the watcher beat,
// the captains with rows, the working tasks and the unacked wakes.
func (m dashboardModel) chips(th dashTheme) string {
	on := func(s lipgloss.Style) lipgloss.Style { return th.bar.Inherit(s) }
	watcher := on(styleOK).Render("watcher " + m.beat.Age.Round(time.Second).String())
	switch {
	case !m.beat.Exists:
		watcher = on(styleFail).Render("watcher absent")
	case m.beat.Stale:
		watcher = on(markFor("stale").style).Render("watcher stale " + m.beat.Age.Round(time.Second).String())
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
	sep, sub := on(styleMuted).Render(" · "), on(th.subtle)
	return watcher + sep + sub.Render(fmt.Sprintf("captains %d", len(captains))) + sep +
		sub.Render(fmt.Sprintf("working %d", working)) + sep + sub.Render("unacked "+unacked)
}

// body is the list section: a title that counts what the list draws, the
// column header when there is room, then the list, in the focused panel. The
// count is the list's own page, not window arithmetic here.
func (m dashboardModel) body() []dashLine {
	if m.listH < 1 {
		return nil
	}
	switch m.state() {
	case stateLoading:
		return []dashLine{{keep: keepStem(stemLoadingFleet)}}
	case stateEmpty:
		return []dashLine{{keep: keepStem(stemNoTasks)}}
	}
	total := len(m.list.Items())
	if total == 0 {
		return nil
	}
	th := m.theme()
	l := m.list
	cols := m.cols()
	l.SetDelegate(dashRows{stale: m.state() == stateStale, cols: cols, now: m.now(), th: th})
	drawn := 0
	var rows []string
	if m.listH > 1+m.border() {
		start, end := l.Paginator.GetSliceBounds(total)
		drawn = end - start
		rows = strings.Split(l.View(), "\n")
	}
	title := dashLine{keep: keepListTitle(len(m.failures) > 0, drawn, total, l.Paginator.Page+1, l.Paginator.TotalPages)}
	var head []string
	if c := m.columnRows(); c > 0 {
		head = append(head, th.subtle.Bold(true).Render("    "+dashSeatLine(cols, func(_ int, s string) string { return s },
			"GROUP", "NAME", "STATE", "AGE", "SINCE", "CTX", "ROLE", "HARNESS", "MODEL", "STATUS")))
		if c > 1 {
			head = append(head, th.border.Render(strings.Repeat("─", max(0, m.listW()))))
		}
	}
	body := dashFree(append(head, rows...)...)
	switch m.look {
	case lookFlat:
		return append([]dashLine{title}, body...)
	case lookSide:
		return panel(title, body, m.sideW(), 0, dashFocus)
	}
	return panel(title, body, m.width, 0, dashFocus)
}

// middle is the list section with the detail pane below it, or beside it in
// its own panel of the list's height.
func (m dashboardModel) middle() []dashLine {
	body := m.body()
	if m.look != lookSide {
		return append(body, m.detailSection(m.width, 0)...)
	}
	if len(body) < 3 {
		return body
	}
	detail, _ := m.detail()
	n := max(len(body), min(m.listH, len(detail)+2))
	if n > len(body) {
		body = append(body[:len(body)-1:len(body)-1], m.padRows(body, n)...)
	}
	return beside(body, m.detailSection(m.width-m.sideW(), n))
}

// padRows is the list panel's blank rows and bottom border that make it n
// lines tall.
func (m dashboardModel) padRows(body []dashLine, n int) []dashLine {
	rows := make([]dashLine, n-len(body))
	blank := body[1]
	blank.keep, blank.rest = dashKeep{}, ""
	for i := range rows {
		rows[i] = blank
	}
	return append(rows, body[len(body)-1])
}

// detailSection is the detail pane in w cells: its own panel n lines tall
// beside the list, a stacked panel, or flat lines. A held decision is a card.
func (m dashboardModel) detailSection(w, n int) []dashLine {
	if m.look != lookSide && m.detailH == 0 {
		return nil
	}
	lines, held := m.detail()
	if m.look == lookFlat {
		if !held {
			return lines
		}
		var text []string
		for _, l := range lines {
			text = append(text, l.String())
		}
		return dashFree(strings.Split(dashCard.Render(strings.Join(text, "\n")), "\n")...)
	}
	title, border := "Detail", m.theme().border
	it, _ := m.list.SelectedItem().(dashItem)
	switch {
	case it.failure != nil:
		title, border = "Failed source", styleFail
	case held:
		title, border = "Decision", styleWarn
	}
	if n > 0 {
		lines = lines[:min(len(lines), n-2)]
	}
	return panel(dashLine{rest: dashBold.Render(title)}, lines, w, n, border)
}

// dashColW are the seat columns' widths, STATUS last and open. GROUP and NAME
// are these at most: cols sizes them to their longest cell.
var dashColW = [...]int{12, 20, 18, 5, 5, 4, 6, 7, 14}

// dashColMin are the GROUP and NAME columns' least widths.
var dashColMin = [2]int{5, 8}

// dashCols is a frame's width of each seat column, 0 for a column not drawn.
type dashCols [len(dashColW)]int

// cols lays the seat columns out for the list width and its rows. GROUP and
// NAME are as wide as their longest cell over every row, not only the page,
// within dashColMin and dashColW; GROUP is not drawn while every task row has
// the same group. The cells the list has beside the row lead (4), GROUP, NAME,
// STATE and ROLE then gate the other columns: AGE and SINCE are drawn when
// STATUS keeps 15 cells with them, CTX when it keeps 15 with CTX added too,
// and HARNESS and MODEL when it keeps 25 with every column. A read moves them
// only by changing GROUP or NAME.
func (m dashboardModel) cols() dashCols {
	c := m.contentCols()
	used := 4
	for _, i := range []int{0, 1, 2, 6} {
		if c[i] > 0 {
			used += c[i] + 1
		}
	}
	age, ctx, seat := c[3]+c[4]+2, c[5]+1, c[7]+c[8]+2
	w := m.listW()
	if used+age+15 > w {
		c[3], c[4] = 0, 0
	}
	if used+age+ctx+15 > w {
		c[5] = 0
	}
	if used+age+ctx+seat+25 > w {
		c[7], c[8] = 0, 0
	}
	return c
}

// contentCols is every seat column at its width, GROUP and NAME sized to
// their content as cols describes.
func (m dashboardModel) contentCols() dashCols {
	c := dashCols(dashColW)
	groups := map[string]bool{}
	group, name := dashColMin[0], dashColMin[1]
	for _, it := range m.list.Items() {
		if it := it.(dashItem); it.row != nil {
			groups[dashGroup(it)] = true
			group = max(group, ansi.StringWidth(dashText(dashGroup(it))))
			name = max(name, ansi.StringWidth(dashText(it.row.ID)))
		}
	}
	c[0], c[1] = min(c[0], group), min(c[1], name)
	if len(groups) < 2 {
		c[0] = 0
	}
	return c
}

// dashSeatLine lays a seat row out: each drawn cell padded to its column and
// painted, then the status.
func dashSeatLine(cols dashCols, paint func(col int, s string) string, cells ...string) string {
	var b strings.Builder
	for i, w := range cols {
		if w == 0 {
			continue
		}
		b.WriteString(paint(i, pad(cells[i], w)+" "))
	}
	b.WriteString(paint(len(dashColW), cells[len(dashColW)]))
	return b.String()
}

// dashRows draws a list item as one line: a failed source in red, a task row
// with its state icon and colour, a Human-needed row with a red "!". The
// selected item has an accent bar and a highlighted background; a stale row
// has no state colour.
type dashRows struct {
	stale bool
	cols  dashCols
	now   time.Time
	th    dashTheme
}

func (dashRows) Height() int                         { return 1 }
func (dashRows) Spacing() int                        { return 0 }
func (dashRows) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d dashRows) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it := item.(dashItem)
	sel := index == m.Index()
	// paint styles text, highlighted on the selected row; nil is plain.
	paint := func(s *lipgloss.Style, text string) string {
		switch {
		case sel && s == nil:
			return d.th.sel.Render(text)
		case sel:
			return d.th.sel.Inherit(*s).Render(text)
		case s == nil:
			return text
		}
		return s.Render(text)
	}
	line := paint(nil, "  ")
	if sel {
		line = paint(&styleAccent, "▌") + paint(nil, " ")
	}
	if f := it.failure; f != nil {
		line += paint(&styleFail, "✗ "+dashText(f.Source)+"  "+dashText(fmt.Sprint(f.Err)))
	} else {
		line += d.task(m, index, it, paint)
	}
	line = ansi.Truncate(line, m.Width(), "…")
	if gap := m.Width() - lipgloss.Width(line); gap > 0 {
		line += paint(nil, strings.Repeat(" ", gap))
	}
	fmt.Fprint(w, line)
}

// task is a task row after its selection mark. A group's first row on the
// page names the group; a captain group's later rows hang under it as a tree.
func (d dashRows) task(m list.Model, index int, it dashItem, paint func(*lipgloss.Style, string) string) string {
	ts := it.row
	mark, phase := dashPhase(*ts)
	state, hnStyle := &mark.style, styleFail.Bold(true)
	hn, hnPaint := " ", &hnStyle
	if fleet.HumanNeeded(*ts) {
		hn = "!"
	}
	if d.stale {
		phase += " [stale]"
		state, hnPaint = nil, nil
	}
	status := dashText(ts.CurrentDescription)
	if status == "" {
		status = dashText(ts.LastStatus)
	}
	items := m.Items()
	g := dashGroup(it)
	first := m.Paginator.Page * m.Paginator.PerPage
	end := min(len(items), first+m.Paginator.PerPage)
	group, groupStyle := "", &dashBold
	switch {
	case index == first || dashGroup(items[index-1].(dashItem)) != g:
		group = dashText(g)
	case g != "general":
		group, groupStyle = "├─", &d.th.subtle
		if index+1 == end || dashGroup(items[index+1].(dashItem)) != g {
			group = "└─"
		}
	}
	sub := &d.th.subtle
	styles := [...]*lipgloss.Style{groupStyle, nil, state, sub, sub, sub, sub, sub, sub, nil}
	return paint(hnPaint, hn) + paint(nil, " ") + dashSeatLine(d.cols, func(col int, s string) string { return paint(styles[col], s) },
		group, dashText(ts.ID), mark.icon+" "+phase, dashAge(d.now, ts.LaunchedAt), dashAge(d.now, ts.PhaseChangedAt),
		dashCtx(ts.ContextUsed, ts.ContextWindow), dashText(ts.Kind), dashText(dashOr(ts.Harness)), dashText(dashOr(ts.Model)), status)
}

// dashAge is the time from at (unix nanoseconds) to now in its largest whole
// unit, "-" when at is unknown.
func dashAge(now time.Time, at int64) string {
	if at == 0 {
		return "-"
	}
	d := max(0, now.Sub(time.Unix(0, at)))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", d/time.Second)
	case d < time.Hour:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dd", d/(24*time.Hour))
}

// dashCtx is a seat's context use: the share of a known window, else the
// tokens in pi's compact form, "-" when unknown. It is at most 4 cells below
// 100M tokens.
func dashCtx(used, window int) string {
	switch {
	case used <= 0:
		return "-"
	case window > 0:
		return fmt.Sprintf("%d%%", (used*100+window/2)/window)
	case used < 1000:
		return fmt.Sprint(used)
	case used < 9950:
		return fmt.Sprintf("%.1fk", float64(used)/1e3)
	case used < 999_500:
		return fmt.Sprintf("%dk", (used+500)/1000)
	case used < 9_950_000:
		return fmt.Sprintf("%.1fM", float64(used)/1e6)
	}
	return fmt.Sprintf("%dM", (used+500_000)/1_000_000)
}

// dashPhase is a row's phase mark and escaped word. A working or blocked row
// whose seat the snapshot could not probe reads "<phase> unknown" with the
// unknown mark, never its phase's colour.
func dashPhase(ts fleet.TaskSnapshot) (stateMark, string) {
	word := fleet.PhaseFromProjection(ts)
	switch taskauthority.Phase(ts.CurrentState) {
	case taskauthority.PhaseWorking, taskauthority.PhaseBlocked:
		if ts.PaneAliveUnknown {
			return unknownMark, dashText(word) + " unknown"
		}
	}
	return markFor(word), dashText(word)
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
	shown, _ := m.shownEvents()
	switch {
	case m.eventErr != nil:
		return ""
	case !m.feedLoaded:
		return styleMuted.Render("Loading events...")
	case len(shown) == 0:
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
	_, hidden := m.shownEvents()
	title := dashLine{keep: keepFeedTitle(m.feed.VisibleLineCount(), m.feed.TotalLineCount(), hidden, m.skipped, m.eventErr != nil)}
	if m.eventErr != nil {
		title.rest = styleFail.Render(" " + dashText(m.eventErr.Error()))
	}
	var body []dashLine
	if p := m.feedPlaceholder(); p != "" && m.feedH > 1+m.border() {
		body = append(body, dashLine{rest: p})
	}
	if m.feed.Height() > 0 {
		body = append(body, dashFree(strings.Split(m.feed.View(), "\n")...)...)
	}
	if m.look == lookFlat {
		return append([]dashLine{title}, body...)
	}
	return panel(title, body, m.width, 0, m.theme().border)
}

// eventLine is one event: the time muted, dated when its day is not now's,
// the type in its family's colour, producer and key subtle, the key only when
// it is not the producer, the payload plain.
func eventLine(e orchestrator.Record, now time.Time, th dashTheme) string {
	paint := func(s lipgloss.Style, text string) string {
		if text == "" {
			return ""
		}
		return s.Render(dashText(text))
	}
	t, layout := time.Unix(0, e.Timestamp).UTC(), "15:04:05"
	if t.Format(time.DateOnly) != now.UTC().Format(time.DateOnly) {
		layout = "Jan 02 15:04:05"
	}
	key := e.Key
	if key == e.Producer {
		key = ""
	}
	var parts []string
	for _, p := range []string{styleMuted.Render(t.Format(layout)), paint(eventStyle(e.Type), e.Type), paint(th.subtle, e.Producer), paint(th.subtle, key), dashText(e.Payload)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// eventStyle colours an event type by its family: errors and refusals red,
// done green, wakes and reports accent.
func eventStyle(typ string) lipgloss.Style {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "error") || strings.Contains(t, "fail") || strings.Contains(t, "refused"):
		return styleFail
	case strings.Contains(t, "done"):
		return styleOK
	case strings.Contains(t, "wake") || strings.Contains(t, "report"):
		return styleAccent
	}
	return lipgloss.NewStyle()
}

// helpLines are the key footer: the movement entry, decide, the actions of the
// table, help and quit, in that order. By default the footer draws only the
// entries that apply (offered), on one line, dropping the last of them while
// help and quit do not fit; with help on it draws every entry, dimming those
// that do not apply. A key not drawn or dimmed still reaches begin and gets
// its notice.
func (m dashboardModel) helpLines() []string {
	it, _ := m.list.SelectedItem().(dashItem)
	move := dashMove
	move.SetEnabled(len(m.list.Items()) >= 2)
	decide := dashDecide
	decide.SetEnabled(it.row != nil && len(m.holdsOf(*it.row)) > 0)
	entries := []key.Binding{move, decide}
	for i := range dashActions {
		b := dashActions[i].keys
		b.SetEnabled(m.offered(&dashActions[i], it))
		entries = append(entries, b)
	}
	help := dashHelp
	if m.help {
		help.SetHelp("?", "less")
		return m.keyLines(append(entries, help, dashQuit)...)
	}
	var on []key.Binding
	for _, e := range entries {
		if e.Enabled() {
			on = append(on, e)
		}
	}
	tail := []key.Binding{help, dashQuit}
	for len(on) > 0 && len(m.keyLines(append(on, tail...)...)) > 1 {
		on = on[:len(on)-1]
	}
	return m.keyLines(append(on, tail...)...)
}

// offered is whether the footer offers the action for the selected item: the
// target begin captures binds, a captain ID resolves from the last read where
// begin needs one (begin reads the same m.registry and m.captainIDs), and for
// a task action the row has a
// canonical phase (a captain metadata row has none) that, with its holds, the
// action's applies admits.
func (m dashboardModel) offered(a *dashAction, it dashItem) bool {
	t := m.sel
	if a.binding == bindCaptain {
		t = captainTarget(t, m.registry)
	}
	if _, problem := a.bind(t, len(m.list.Items()) > 0, m.home); problem != "" {
		return false
	}
	if _, ok := m.captainIDs[t.Home]; a.needsCaptainID && !ok {
		return false
	}
	if a.binding != bindTask {
		return true
	}
	if !taskauthority.Phase(it.row.CurrentState).Valid() {
		return false
	}
	return a.applies == nil || a.applies(*it.row, len(m.holdsOf(*it.row)) > 0)
}

// keyLines draws key entries in the help style, each its key and label, a
// disabled one faint, separated by " • ". Entries fill a line while it fits
// the terminal; the next one starts a new line, and no entry is split.
func (m dashboardModel) keyLines(entries ...key.Binding) []string {
	desc := m.theme().subtle
	sepW := lipgloss.Width(dashHelpSep)
	var lines []string
	line, width := "", 0
	for _, e := range entries {
		h := e.Help()
		text := dashKey.Render(h.Key) + " " + desc.Render(h.Desc)
		if !e.Enabled() {
			text = styleMuted.Render(h.Key + " " + h.Desc)
		}
		w := lipgloss.Width(text)
		if line != "" && m.width > 0 && width+sepW+w > m.width {
			lines = append(lines, line)
			line, width = "", 0
		}
		if line != "" {
			line += dashHelpSep
			width += sepW
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
