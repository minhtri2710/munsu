package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var dashCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styleWarn.GetForeground()).Padding(0, 1)

// detail describes the selected item: a failed source, a row with open
// decision holds (held) with their options, or a task's task, seat and phase
// facts.
func (m dashboardModel) detail() (lines []dashLine, held bool) {
	it, ok := m.list.SelectedItem().(dashItem)
	if !ok {
		return nil, false
	}
	if f := it.failure; f != nil {
		return dashFree(
			styleFail.Render("source "+dashText(f.Source))+"  home "+dashText(dashOr(f.Home)),
			styleFail.Render(dashText(fmt.Sprint(f.Err))),
		), false
	}
	ts := it.row
	if holds := m.holdsOf(*ts); len(holds) > 0 {
		var body []string
		for _, h := range holds {
			body = append(body, dashBold.Render("DECISION "+dashText(h.key))+"  from "+dashText(ts.ID), dashText(h.reason))
		}
		for _, o := range dashDecideOptions {
			body = append(body, "- "+o.Key)
		}
		return dashFree(body...), true
	}
	mark, phase := dashPhase(*ts)
	if m.state() == stateStale {
		phase += " [stale]"
	} else {
		phase = mark.style.Render(phase)
	}
	pane := ""
	if ts.Window != "" {
		pane = "unknown"
		if ts.PaneAlive {
			pane = "alive"
		}
	}
	th := m.theme()
	text := []string{strings.TrimRight(dashBold.Render(dashText(ts.ID))+"  "+phase+"  "+
		dashFields(th, "kind", ts.Kind, "project", ts.Project, "group", dashGroup(it)), " ")}
	for _, l := range []string{
		dashFields(th, "harness", ts.Harness, "model", ts.Model, "mode", ts.Mode, "window", ts.Window, "pane", pane, "worktree", ts.Worktree),
		dashFields(th, "last", ts.LastStatus),
	} {
		if l != "" {
			text = append(text, l)
		}
	}
	return dashFree(text...), false
}

// dashFields draws label and value pairs, each a dim label and its escaped
// value, two spaces apart; a pair with no value is left out.
func dashFields(th dashTheme, pairs ...string) string {
	var out []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			out = append(out, th.subtle.Render(pairs[i])+" "+dashText(pairs[i+1]))
		}
	}
	return strings.Join(out, "  ")
}
