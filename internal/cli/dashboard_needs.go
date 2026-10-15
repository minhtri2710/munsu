package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
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

// needKind names what a Human-needed row waits on: the first Human-needed
// verb in its status line, else stuck (a canonical blocked row).
func needKind(ts fleet.TaskSnapshot) string {
	line := strings.ToLower(ts.LastStatus)
	kind, at := "stuck", len(line)
	for verb, k := range map[string]string{"needs-decision:": "decision", "blocked:": "stuck", "failed:": "failed"} {
		if i := strings.Index(line, verb); i >= 0 && i < at {
			kind, at = k, i
		}
	}
	return kind
}

// needs lists what waits on the operator, from the last good read: open
// decision holds and decision lines, unresolved wakes, failed sources and
// seats, stuck seats, and review verdicts reported on a review task that is
// not finished. A row is listed by status exactly when fleet.HumanNeeded
// counts it. Rows keep list order.
func (m dashboardModel) needs() []dashNeed {
	var decisions, failed, stuck, verdicts []dashNeed
	if m.holdsErr != nil {
		failed = append(failed, dashNeed{kind: "failed", id: "decision holds", text: m.holdsErr.Error()})
	}
	for _, f := range m.failures {
		failed = append(failed, dashNeed{kind: "failed", id: f.Source, text: fmt.Sprint(f.Err)})
	}
	for _, it := range m.items() {
		ts := it.(dashItem).row
		if ts == nil || dashFinished(*ts) {
			continue
		}
		holds := m.holdsOf(*ts)
		for _, h := range holds {
			decisions = append(decisions, dashNeed{kind: "decision", id: ts.ID, text: h.key + ": " + h.reason})
		}
		n := dashNeed{id: ts.ID, text: domain.ReportLineText(ts.LastStatus)}
		switch {
		case fleet.HumanNeeded(*ts):
			n.kind = needKind(*ts)
		case ts.Kind == taskauthority.KindReview && domain.LineVerb(ts.LastStatus) == "done" && domain.IsReportLine(ts.LastStatus):
			n.kind = "verdict"
		default:
			continue
		}
		if n.text == "" {
			n.text = ts.CurrentDescription
		}
		switch n.kind {
		case "decision":
			if len(holds) == 0 {
				decisions = append(decisions, n)
			}
		case "failed":
			failed = append(failed, n)
		case "stuck":
			stuck = append(stuck, n)
		default:
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

// needsLines is the NEEDS YOU section in at most n lines: the title and the
// items that fit, or one muted line when nothing waits. Nothing is drawn
// before the first good read.
func (m dashboardModel) needsLines(n int) []dashLine {
	if m.lastGood.IsZero() || n < 1 {
		return nil
	}
	needs := m.needs()
	if len(needs) == 0 {
		return []dashLine{{keep: keepStem(stemNothingNeedsYou)}}
	}
	shown := needs[:min(len(needs), dashNeedsMax, n-1)]
	out := []dashLine{{keep: keepNeedsTitle(len(shown), len(needs))}}
	stale := m.state() == stateStale
	for _, n := range shown {
		kind := pad(n.kind, 9)
		if !stale {
			kind = dashNeedStyle[n.kind].Render(kind)
		}
		out = append(out, dashLine{rest: kind + pad(dashText(n.id), 20) + " " + dashText(n.text)})
	}
	return out
}

// needsSection is NEEDS YOU in the lines resize gave it: a red panel, or a
// title with its items indented below it. "Nothing needs you." is one line.
func (m dashboardModel) needsSection() []dashLine {
	if m.needsH < 1 {
		return nil
	}
	lines := m.needsLines(m.needsH)
	if lines[0].keep == keepStem(stemNothingNeedsYou) {
		return lines
	}
	if m.look == lookFlat {
		for i := range lines[1:] {
			lines[i+1].rest = "  " + lines[i+1].rest
		}
		return lines
	}
	border := styleFail
	if m.state() == stateStale {
		border = m.theme().border
	}
	lines = m.needsLines(m.needsH - 1)
	return panel(lines[0], lines[1:], m.width, 0, border)
}
