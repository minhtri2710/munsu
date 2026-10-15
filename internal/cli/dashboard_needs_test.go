package cli

import (
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// attentionHome is a real home whose snapshot holds one row for each kind of
// item that waits on the operator, rows that do not wait (hotel, the working
// ship delta reviews, among them), and a queued wake.
func attentionHome(t *testing.T) string {
	t.Helper()
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	h := deliveryGuardShip(t, "hotel")
	for _, id := range []string{"alpha", "bravo", "charlie", "echo", "foxtrot", "golf"} {
		cliSeedCanonicalTask(t, h, id, "ship")
	}
	auth := cliCanonicalForHome(t, h)
	review := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskIDFor(t, "delta"), Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, ReviewTaskID: "hotel", ReviewHead: deliveryGuardHead, Reason: "test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-delta", review), review); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "task", "done", "golf", "--home", h); err != nil {
		t.Fatalf("task done: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "decision-hold", "hold", "approach", "--reason", "Pick the UI framework", "--from", "alpha", "--home", h); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}
	for id, line := range map[string]string{
		"bravo":   "failed: CI red",
		"charlie": "blocked: waiting on creds",
		"delta":   domain.ReportStatusLine("done", "PASS at abc123", "review-verdict:x"),
		"echo":    "working: fine",
		"foxtrot": domain.ReportStatusLine("needs-decision", "which db?", "db"),
		"golf":    "failed: an old failure",
	} {
		if err := home.AppendStatus(h, id, line); err != nil {
			t.Fatal(err)
		}
	}
	if err := home.WriteMeta(h, "echo", map[string]string{"id": "echo", "kind": "ship", "harness": "claude", "model": "opus", "mode": "direct-PR", "window": "w-echo", "worktree": "/wt/echo"}); err != nil {
		t.Fatal(err)
	}
	if err := home.EnqueueWake(h, "report", "bravo", "x"); err != nil {
		t.Fatal(err)
	}
	return h
}

func attentionModel(t *testing.T, h string, w, height int) dashboardModel {
	t.Helper()
	m := newDashboardModel(h, "/bin/munsu")
	m.now = func() time.Time { return dashNow }
	m = resized(m, w, height)
	return send(m, m.read())
}

// needsLines are the NEEDS YOU title and items of a frame, spaces collapsed.
func needsLines(frame string) []string {
	var got []string
	in := false
	for _, l := range strings.Split(ansi.Strip(frame), "\n") {
		l = strings.TrimRight(l, " ")
		switch {
		case strings.HasPrefix(l, "NEEDS YOU"):
			in = true
			got = append(got, l)
		case in && strings.HasPrefix(l, "  "):
			got = append(got, strings.Join(strings.Fields(l), " "))
		default:
			in = false
		}
	}
	return got
}

// NEEDS YOU lists, from the real snapshot, the open decisions, the unresolved
// wakes, the failed and stuck seats and the reported verdicts, in that order;
// a working row and a finished one are not listed, and past the cap the title
// says how many it shows.
func TestDashboardNeedsYou(t *testing.T) {
	m := attentionModel(t, attentionHome(t), 120, 40)
	want := []string{
		"NEEDS YOU 6 - showing 5",
		"decision alpha needs-decision: Pick the UI framework [key=approach]",
		"decision foxtrot needs-decision: which db? [key=db]",
		"wakes 1 unresolved",
		"failed bravo failed: CI red",
		"stuck charlie blocked: waiting on creds",
	}
	if got := needsLines(m.View().Content); !slices.Equal(got, want) {
		t.Fatalf("NEEDS YOU =\n%s\nwant\n%s\nframe:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), m.View().Content)
	}

	m.wakes = func(time.Time) (int, error) { return 0, nil }
	m = send(m, m.read())
	want = []string{
		"NEEDS YOU 5",
		"decision alpha needs-decision: Pick the UI framework [key=approach]",
		"decision foxtrot needs-decision: which db? [key=db]",
		"failed bravo failed: CI red",
		"stuck charlie blocked: waiting on creds",
		"verdict delta done: PASS at abc123 [key=review-verdict:x]",
	}
	if got := needsLines(m.View().Content); !slices.Equal(got, want) {
		t.Fatalf("with no wake, NEEDS YOU =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The detail pane draws the selected row's task, seat and phase facts; a held
// decision is a card with its options.
func TestDashboardDetailPane(t *testing.T) {
	m := selectTask(t, attentionModel(t, attentionHome(t), 120, 40), "alpha")
	f := ansi.Strip(m.View().Content)
	assertShown(t, "card", f, "╭", "│ DECISION approach  from alpha", "│ Pick the UI framework", "│ - resolve: record an answer", "│ - complete: note it without an answer", "╰")
	if strings.Contains(f, "last needs-decision") {
		t.Fatalf("a held decision draws the card, not the facts:\n%s", f)
	}
	m = selectTask(t, m, "foxtrot")
	if f := ansi.Strip(m.View().Content); strings.Contains(f, "DECISION db") || !regexp.MustCompile(`(?m)^last needs-decision: which db\? \[key=db\]`).MatchString(f) {
		t.Fatalf("a reported needs-decision is not a hold card:\n%s", f)
	}
	m = selectTask(t, m, "echo")
	assertShown(t, "detail", ansi.Strip(m.View().Content),
		"echo  ship  project munsu  queued  general",
		"seat claude opus  mode direct-PR  window w-echo  pane unknown  worktree /wt/echo",
		"last working: fine")
	m = selectTask(t, m, "golf")
	assertShown(t, "no window", ansi.Strip(m.View().Content), "seat - -  mode -  window -  pane -  worktree -")

	alive := row("t-live", "working", "x", "primary", "")
	alive.Window, alive.PaneAlive = "w-live", true
	m = send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{alive}, nil, nil))
	assertShown(t, "alive", ansi.Strip(m.View().Content), "window w-live  pane alive")
}

// enter on a held decision asks with huh, then the choice runs, after the
// same confirmation as every action, through the decision-hold command.
func TestDashboardDecideRunsDecisionHold(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		status      string
	}{
		{"resolve", "1\nUse React\nhuman\nchat\nuse react\n",
			[]string{"decision-hold", "resolve", "approach", "--answer", "Use React", "--from", "alpha", "--grantor", "human", "--channel", "chat", "--quote", "use react"},
			"resolved: Use React [key=approach]"},
		{"complete", "2\nignored\nhuman\nchat\nnoted\n",
			[]string{"decision-hold", "complete", "alpha", "approach", "--grantor", "human", "--channel", "chat", "--quote", "noted"},
			"resolved: recorded (decision noted) [key=approach]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := attentionHome(t)
			m := attentionModel(t, h, 200, 40)
			m.ask = func(f *huh.Form) tea.Cmd {
				err := f.WithAccessible(true).WithInput(iotest.OneByteReader(strings.NewReader(tc.input))).WithOutput(io.Discard).Run()
				return func() tea.Msg { return dashAsked{err} }
			}
			var ran [][]string
			m.run = func(_ string, args []string) tea.Cmd {
				ran = append(ran, args)
				out, err := runRoot(t, args...)
				return func() tea.Msg { return dashExecDone{argv: args, err: err, output: out} }
			}
			m = selectTask(t, m, "alpha")
			nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = send(nm.(dashboardModel), cmd())
			want := append([]string{"--home", h}, tc.args...)
			if m.mode != modeConfirm || !strings.Contains(dashNoSpace(ansi.Strip(m.View().Content)), dashNoSpace("Run: /bin/munsu "+argvLine(want))) {
				t.Fatalf("mode %v; want the confirmation of %q:\n%s", m.mode, want, m.View().Content)
			}
			nm, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
			m = send(nm.(dashboardModel), cmd())
			if len(ran) != 1 || !slices.Equal(ran[0], want) || m.result.err != nil {
				t.Fatalf("ran %q (err %v, output %s), want %q", ran, m.result.err, m.result.output, want)
			}
			lines, err := home.ReadStatus(h, "alpha")
			if err != nil || lines[len(lines)-1] != tc.status {
				t.Fatalf("alpha status = %q (%v), want last %q", lines, err, tc.status)
			}
			if out, err := runRoot(t, "decision-hold", "verify", "alpha", "--home", h); err != nil {
				t.Fatalf("hold still open after the dashboard decided: %v\n%s", err, out)
			}
		})
	}

	t.Run("an aborted form runs nothing", func(t *testing.T) {
		m := attentionModel(t, attentionHome(t), 200, 40)
		m.ask = func(*huh.Form) tea.Cmd { return func() tea.Msg { return dashAsked{huh.ErrUserAborted} } }
		m = selectTask(t, m, "alpha")
		nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = send(nm.(dashboardModel), cmd())
		if m.mode != modeBrowse || m.pending != nil || strings.Contains(ansi.Strip(m.View().Content), "decide:") {
			t.Fatalf("mode %v pending %v:\n%s", m.mode, m.pending, m.View().Content)
		}
	})

	t.Run("a form that fails says why and runs nothing", func(t *testing.T) {
		m := attentionModel(t, attentionHome(t), 200, 40)
		m.ask = func(*huh.Form) tea.Cmd { return func() tea.Msg { return dashAsked{errors.New("no terminal")} } }
		m = selectTask(t, m, "alpha")
		nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = send(nm.(dashboardModel), cmd())
		if m.mode != modeBrowse || m.pending != nil || !strings.Contains(ansi.Strip(m.View().Content), "decide: no terminal") {
			t.Fatalf("mode %v pending %v:\n%s", m.mode, m.pending, m.View().Content)
		}
	})

	t.Run("enter off a held decision asks nothing", func(t *testing.T) {
		m := attentionModel(t, attentionHome(t), 200, 40)
		m.ask = func(*huh.Form) tea.Cmd { t.Fatal("asked off a held decision"); return nil }
		for _, id := range []string{"foxtrot", "echo"} {
			m = press(selectTask(t, m, id), "enter")
			if m.mode != modeBrowse || !strings.Contains(ansi.Strip(m.View().Content), "decide: select a held decision") {
				t.Fatalf("%s: mode %v:\n%s", id, m.mode, m.View().Content)
			}
		}
	})
}
