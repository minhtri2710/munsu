package cli

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/home"
)

// enter on a held decision asks with huh, then the choice runs, after the
// same confirmation as every action, through the decision-hold command.
func TestDashboardDecideRunsDecisionHold(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		status      string
	}{
		{"resolve", "1\n1\nUse React\nhuman\nchat\nuse react\n",
			[]string{"decision-hold", "resolve", "approach", "--answer", "Use React", "--from", "alpha", "--grantor", "human", "--channel", "chat", "--quote", "use react"},
			"resolved: Use React [key=approach]"},
		{"complete", "1\n2\nignored\nhuman\nchat\nnoted\n",
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
			m = send(m, m.read())
			if f := ansi.Strip(m.View().Content); strings.Contains(f, "DECISION approach") || strings.Contains(f, "decision alpha") {
				t.Fatalf("the released hold is still shown:\n%s", f)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		err    error
		notice string
	}{
		{"an aborted form runs nothing", huh.ErrUserAborted, ""},
		{"a form that fails says why and runs nothing", errors.New("no terminal"), "decide: no terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := attentionModel(t, attentionHome(t), 200, 40)
			m.ask = func(*huh.Form) tea.Cmd { return func() tea.Msg { return dashAsked{tc.err} } }
			m = selectTask(t, m, "alpha")
			nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = send(nm.(dashboardModel), cmd())
			f := ansi.Strip(m.View().Content)
			if m.mode != modeBrowse || m.pending != nil || strings.Contains(f, "decide:") != (tc.notice != "") || !strings.Contains(f, tc.notice) {
				t.Fatalf("mode %v pending %v:\n%s", m.mode, m.pending, f)
			}
		})
	}

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
