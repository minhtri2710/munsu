package cli

import (
	"fmt"

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
	pane := "unknown"
	switch {
	case ts.Window == "":
		pane = "-"
	case ts.PaneAlive:
		pane = "alive"
	}
	return dashFree(
		dashBold.Render(dashText(ts.ID))+"  "+dashText(dashOr(ts.Kind))+"  project "+dashText(dashOr(ts.Project))+"  "+phase+"  "+dashText(dashGroup(it)),
		"seat "+dashText(dashOr(ts.Harness))+" "+dashText(dashOr(ts.Model))+"  mode "+dashText(dashOr(ts.Mode))+"  window "+dashText(dashOr(ts.Window))+"  pane "+pane+"  worktree "+dashText(dashOr(ts.Worktree)),
		"last "+dashText(dashOr(ts.LastStatus)),
	), false
}
