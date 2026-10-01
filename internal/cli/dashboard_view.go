package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

var dashHelp = []string{
	"j/k move  b block  u unblock  d done  t retry  o reopen  x teardown  s send  p promote",
	"h hold  e resolve  c complete  R retire  V recover  C converge  v record-verdict  m pr-merge  q quit",
}

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

// wrap breaks s into lines of at most w cells, preferring to break after a
// space. It drops and adds no character: the lines concatenate back to s.
func wrap(s string, w int) []string {
	w = max(w, 1)
	var out []string
	for lipgloss.Width(s) > w {
		cut, used, brk := 0, 0, 0
		for i, r := range s {
			rw := lipgloss.Width(string(r))
			if used+rw > w {
				break
			}
			used += rw
			cut = i + len(string(r))
			if r == ' ' {
				brk = cut
			}
		}
		if cut == 0 {
			cut = len(string([]rune(s)[:1]))
		}
		if brk > 0 {
			cut = brk
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
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

func (m dashboardModel) frame() string {
	feedN := 3
	if m.height >= 24 {
		feedN = 6
	}
	head, foot, feed := m.header(), m.footer(), m.feed(feedN)
	if m.mode == modeConfirm && !m.confirmFits() {
		return strings.Join(wrap(confirmTooSmall, m.width), "\n")
	}

	// The body and feed give up rows before the footer does: the confirm
	// block shows the whole argv the Human approves.
	mid := append(m.body(m.height-len(head)-len(foot)-len(feed)), feed...)
	if room := max(0, m.height-len(head)-len(foot)); len(mid) > room {
		mid = mid[:room]
	}
	out := append([]string{}, head...)
	out = append(out, mid...)
	out = append(out, foot...)

	for i, l := range out {
		out[i] = ansi.Truncate(l, m.width, "…")
	}
	if len(out) > m.height {
		out = out[:m.height]
	}
	return strings.Join(out, "\n")
}

const confirmTooSmall = "Terminal too small to show the full command. Enlarge it, or esc to cancel."

// confirmFits reports whether the header and the whole confirm block fit the
// terminal. Without that the Human cannot see what y would run.
func (m dashboardModel) confirmFits() bool {
	return len(m.header())+len(m.footer()) <= m.height
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

// body renders the failed sources and the task rows within avail lines.
func (m dashboardModel) body(avail int) []string {
	var out []string
	switch m.state() {
	case stateLoading:
		return []string{dashFaint.Render("Loading fleet...")}
	case stateFailed:
		return []string{dashRed.Render("Fleet read failed: " + dashText(fmt.Sprint(m.readErr)))}
	case stateEmpty:
		return []string{dashFaint.Render("No tasks and no failed sources.")}
	}
	stale := m.state() == stateStale
	if stale && m.readErr != nil {
		out = append(out, dashRed.Render("Last read failed: "+dashText(m.readErr.Error())))
	}
	for i := range m.failures {
		f := m.failures[i]
		line := m.mark(i) + "x " + dashText(f.Source) + "  " + dashText(fmt.Sprint(f.Err))
		out = append(out, m.styleLine(i, line, dashRed))
	}

	slots := max(1, avail-len(out)-1)
	title := "Tasks"
	start := 0
	if r := m.cur - len(m.failures); r >= slots {
		start = r - slots + 1
	}
	shown := m.rows[min(start, len(m.rows)):min(start+slots, len(m.rows))]
	if len(shown) < len(m.rows) {
		title = fmt.Sprintf("Tasks - Showing %d of %d", len(shown), len(m.rows))
	}
	out = append(out, dashBold.Render(title))
	for j, ts := range shown {
		i := len(m.failures) + start + j
		phase := dashText(fleet.PhaseFromProjection(ts))
		if stale {
			phase += " [stale]"
		}
		status := dashText(ts.CurrentDescription)
		if status == "" {
			status = dashText(ts.LastStatus)
		}
		hn := "  "
		if fleet.HumanNeeded(ts) {
			hn = "! "
		}
		src := dashText(ts.Source)
		if src == "" {
			src = "primary"
		}
		id, kind := dashText(ts.ID), dashText(ts.Kind)
		plain := m.mark(i) + hn + pad(id, 22) + " " + pad(phase, 16) + " " + pad(kind, 7) + " " + pad(src, 16) + " " + status
		if i == m.cur {
			out = append(out, dashSelect.Render(plain))
			continue
		}
		cells := m.mark(i) + hn + pad(id, 22) + " " + phaseStyle(phase).Render(pad(phase, 16)) + " " + pad(kind, 7) + " " + pad(src, 16) + " " + status
		out = append(out, cells)
	}
	return out
}

func (m dashboardModel) mark(i int) string {
	if i == m.cur {
		return "> "
	}
	return "  "
}

func (m dashboardModel) styleLine(i int, line string, st lipgloss.Style) string {
	if i == m.cur {
		return dashSelect.Render(line)
	}
	return st.Render(line)
}

func (m dashboardModel) feed(n int) []string {
	shown := m.events[max(0, len(m.events)-(n-1)):]
	var title string
	if m.eventErr != nil {
		title = dashRed.Render("Events - unreadable: " + dashText(m.eventErr.Error()))
	} else {
		t := "Events"
		if m.skipped > 0 {
			t += fmt.Sprintf(" (%d malformed lines skipped)", m.skipped)
		}
		if len(shown) < len(m.events) {
			t += fmt.Sprintf(" - Showing %d of the last %d read", len(shown), len(m.events))
		}
		title = dashBold.Render(t)
	}
	out := []string{title}
	switch {
	case !m.feedLoaded && m.eventErr == nil:
		out = append(out, dashFaint.Render("Loading events..."))
	case len(m.events) == 0 && m.eventErr == nil:
		out = append(out, dashFaint.Render("No events yet."))
	}
	for _, e := range shown {
		out = append(out, eventLine(e))
	}
	return out
}

func eventLine(e orchestrator.Record) string {
	at := time.Unix(0, e.Timestamp).UTC().Format("15:04:05")
	return strings.TrimRight(fmt.Sprintf("%s %s %s %s %s", at, dashText(e.Type), dashText(e.Producer), dashText(e.Key), dashText(e.Payload)), " ")
}

func (m dashboardModel) footer() []string {
	var out []string
	switch m.mode {
	case modeForm:
		p := m.pending
		out = append(out, dashBold.Render(p.action.name))
		for i, f := range p.action.fields {
			cur := " "
			if i == p.field {
				cur = ">"
			}
			req := ""
			if f.required {
				req = " (required)"
			}
			val := dashText(p.values[i])
			if i == p.field {
				val += "_"
			}
			out = append(out, fmt.Sprintf("%s %s%s: %s", cur, f.label, req, val))
		}
		out = append(out, dashFaint.Render("enter next/submit  esc cancel"))
	case modeConfirm:
		out = append(out, wrap("Run: "+argvLine(append([]string{m.exe}, m.pending.argv...)), m.width)...)
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
		for _, l := range dashHelp {
			out = append(out, dashFaint.Render(l))
		}
	}
	if m.notice != "" {
		out = append(out, dashRed.Render(dashText(m.notice)))
	}
	return out
}
