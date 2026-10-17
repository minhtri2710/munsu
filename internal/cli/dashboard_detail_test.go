package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// The detail pane draws the selected row's task, seat and phase facts; a row
// with an open decision hold is a card with the hold and its options, whatever
// its last status line.
func TestDashboardDetailPane(t *testing.T) {
	m := selectTask(t, attentionModel(t, attentionHome(t), 120, 40), "foxtrot")
	if f := ansi.Strip(m.View().Content); strings.Contains(f, "DECISION db") || !regexp.MustCompile(`(?m)^│ last needs-decision: which db\? \[key=db\]`).MatchString(f) {
		t.Fatalf("a reported needs-decision is not a hold card:\n%s", f)
	}
	m = selectTask(t, m, "alpha")
	f := ansi.Strip(m.View().Content)
	assertShown(t, "card", f, "╭", "│ DECISION approach  from alpha", "│ Pick the UI framework", "│ - resolve: record an answer", "│ - complete: note it without an answer", "╰")
	if strings.Contains(f, "last working") {
		t.Fatalf("a held decision draws the card, not the facts:\n%s", f)
	}
	m = selectTask(t, m, "echo")
	assertShown(t, "detail", ansi.Strip(m.View().Content),
		"│ echo  working unknown  kind ship  project munsu  group general",
		"│ harness claude  model opus  mode direct-PR  window w-echo  pane unknown  worktree /wt/echo",
		"│ last working: fine")
	if sub := m.theme().subtle; !strings.Contains(m.View().Content, sub.Render("harness")+" claude") {
		t.Fatalf("a label is not drawn dim before its plain value:\n%q", m.View().Content)
	}
	// A task with no seat draws neither its seat line nor an unknown field.
	m = selectTask(t, m, "golf")
	f = ansi.Strip(m.View().Content)
	assertShown(t, "no window", f, "│ golf  done  kind ship  project munsu  group general  ", "│ last failed: an old failure")
	if strings.Contains(f, "window") || strings.Contains(f, "pane") {
		t.Fatalf("a task with no seat draws its seat fields:\n%s", f)
	}

	alive := row("t-live", "working", "x", "primary", "")
	alive.Window, alive.PaneAlive = "w-live", true
	m = send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{alive}, nil, nil))
	assertShown(t, "alive", ansi.Strip(m.View().Content), "window w-live  pane alive")
}
