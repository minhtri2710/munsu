package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// dashNeedsMax bounds the NEEDS YOU items a frame draws.
const dashNeedsMax = 5

// dashNeed is one item waiting on the operator.
type dashNeed struct {
	kind string
	id   string
	text string
}

var dashNeedStyle = map[string]lipgloss.Style{
	"decision": styleWarn,
	"wakes":    styleWarn,
	"failed":   styleFail,
	"stuck":    styleFail,
	"verdict":  styleAccent,
}

func dashFinished(ts fleet.TaskSnapshot) bool {
	switch taskauthority.Phase(ts.CurrentState) {
	case taskauthority.PhaseDone, taskauthority.PhaseResolved, taskauthority.PhaseRetired:
		return true
	}
	return false
}

// needs lists what waits on the operator, from the last good read: open
// decisions, unresolved wakes, failed and stuck seats, and review verdicts
// reported on a review task that is not finished. Rows keep list order.
func (m dashboardModel) needs() []dashNeed {
	var decisions, failed, stuck, verdicts []dashNeed
	for _, it := range m.items() {
		ts := it.(dashItem).row
		if ts == nil || dashFinished(*ts) {
			continue
		}
		line := domain.ReportLineText(ts.LastStatus)
		n := dashNeed{id: ts.ID, text: line}
		switch verb := domain.LineVerb(ts.LastStatus); {
		case verb == "needs-decision":
			n.kind = "decision"
			decisions = append(decisions, n)
		case verb == "failed":
			n.kind = "failed"
			failed = append(failed, n)
		case verb == "blocked" || taskauthority.Phase(ts.CurrentState) == taskauthority.PhaseBlocked:
			n.kind = "stuck"
			if n.text == "" {
				n.text = ts.CurrentDescription
			}
			stuck = append(stuck, n)
		case ts.Kind == taskauthority.KindReview && verb == "done" && domain.IsReportLine(ts.LastStatus):
			n.kind = "verdict"
			verdicts = append(verdicts, n)
		}
	}
	out := decisions
	if m.unacked > 0 && m.unackedErr == nil {
		out = append(out, dashNeed{kind: "wakes", text: fmt.Sprintf("%d unresolved", m.unacked)})
	}
	out = append(out, failed...)
	out = append(out, stuck...)
	return append(out, verdicts...)
}

// needsSection is the NEEDS YOU title and its items, or one muted line when
// nothing waits. Nothing is drawn before the first good read.
func (m dashboardModel) needsSection() []dashLine {
	if m.lastGood.IsZero() {
		return nil
	}
	needs := m.needs()
	if len(needs) == 0 {
		return []dashLine{{keep: keepStem(stemNothingNeedsYou)}}
	}
	shown := needs[:min(len(needs), dashNeedsMax)]
	out := []dashLine{{keep: keepNeedsTitle(len(shown), len(needs))}}
	stale := m.state() == stateStale
	for _, n := range shown {
		kind := pad(n.kind, 9)
		if !stale {
			kind = dashNeedStyle[n.kind].Render(kind)
		}
		out = append(out, dashLine{rest: "  " + kind + pad(dashText(n.id), 20) + " " + dashText(n.text)})
	}
	return out
}

// heldDecision reports the decision hold a row waits on: the needs-decision
// line `decision-hold hold` writes, with its key. A report line is not a hold.
func heldDecision(ts fleet.TaskSnapshot) (key, reason string, ok bool) {
	if dashFinished(ts) || domain.LineVerb(ts.LastStatus) != "needs-decision" || domain.IsReportLine(ts.LastStatus) {
		return "", "", false
	}
	msg, key := home.ParseStatusKey(ts.LastStatus)
	if key == "" {
		return "", "", false
	}
	_, reason, _ = strings.Cut(msg, ":")
	return key, strings.TrimSpace(reason), true
}

var dashCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styleWarn.GetForeground()).Padding(0, 1)

// detail describes the selected item: a failed source, a held decision as a
// card with its options, or a task's task, seat and phase facts.
func (m dashboardModel) detail() []dashLine {
	it, ok := m.list.SelectedItem().(dashItem)
	if !ok {
		return nil
	}
	if f := it.failure; f != nil {
		return dashFree(
			styleFail.Render("source "+dashText(f.Source))+"  home "+dashText(dashOr(f.Home)),
			styleFail.Render(dashText(fmt.Sprint(f.Err))),
		)
	}
	ts := it.row
	if key, reason, ok := heldDecision(*ts); ok {
		body := []string{dashBold.Render("DECISION "+dashText(key)) + "  from " + dashText(ts.ID), dashText(reason)}
		for _, o := range dashDecideOptions {
			body = append(body, "- "+o.Key)
		}
		return dashFree(strings.Split(dashCard.Render(strings.Join(body, "\n")), "\n")...)
	}
	phase := dashText(fleet.PhaseFromProjection(*ts))
	if m.state() == stateStale {
		phase += " [stale]"
	} else {
		phase = markFor(phase).style.Render(phase)
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
	)
}

// dashDecision holds the huh form's values while the form runs.
type dashDecision struct {
	target                                  dashTarget
	key                                     string
	action, answer, grantor, channel, quote string
}

// dashAsked reports a finished decision form.
type dashAsked struct{ err error }

// dashForm runs a huh form in the terminal the dashboard gives up while it runs.
type dashForm struct{ *huh.Form }

func (f dashForm) SetStdin(r io.Reader)  { f.WithInput(r) }
func (f dashForm) SetStdout(w io.Writer) { f.WithOutput(w) }
func (dashForm) SetStderr(io.Writer)     {}

func dashAsk(f *huh.Form) tea.Cmd {
	return tea.Exec(dashForm{f}, func(err error) tea.Msg { return dashAsked{err} })
}

// decide asks for the selected held decision's option, answer and words.
func (m dashboardModel) decide() (dashboardModel, tea.Cmd) {
	it, _ := m.list.SelectedItem().(dashItem)
	if it.row == nil {
		m.notice = "decide: select a held decision"
		return m, nil
	}
	key, reason, ok := heldDecision(*it.row)
	if !ok {
		m.notice = "decide: select a held decision"
		return m, nil
	}
	d := &dashDecision{target: m.sel, key: key}
	form := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().Title("Decision "+key+" from "+it.row.ID).Description(reason).Options(dashDecideOptions...).Value(&d.action)),
		huh.NewGroup(huh.NewInput().Title("answer").Value(&d.answer)).WithHideFunc(func() bool { return d.action != "decision-hold resolve" }),
		huh.NewGroup(
			huh.NewInput().Title("grantor").Value(&d.grantor),
			huh.NewInput().Title("channel").Value(&d.channel),
			huh.NewInput().Title("quote (verbatim)").Value(&d.quote),
		),
	)
	m.mode, m.deciding, m.result = modeRunning, d, nil
	return m, m.ask(form)
}

// decided hands the form's values to the chosen action's confirmation.
func (m *dashboardModel) decided(msg dashAsked) {
	d := m.deciding
	m.mode, m.deciding = modeBrowse, nil
	if msg.err != nil {
		if !errors.Is(msg.err, huh.ErrUserAborted) {
			m.notice = "decide: " + msg.err.Error()
		}
		return
	}
	a := actionNamed(d.action)
	home, _ := a.bind(d.target, true, m.home)
	m.pending = &dashPending{action: a, target: d.target, home: home}
	m.confirm(map[string]string{"key": d.key, "keys": d.key, "answer": d.answer, "grantor": d.grantor, "channel": d.channel, "quote": d.quote})
}
