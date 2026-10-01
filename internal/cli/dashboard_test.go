package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// Golden frames live in internal/cli/testdata/dashboard/. Regenerate
// intentionally (only after a reviewed output change) with:
//
//	UPDATE_GOLDEN=1 go test ./internal/cli -run 'TestDashboardGolden'

var dashNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func testDashModel() dashboardModel {
	m := newDashboardModel("/h", "/bin/munsu")
	m.now = func() time.Time { return dashNow }
	m.width, m.height = 100, 24
	return m
}

func send(m dashboardModel, msg tea.Msg) dashboardModel {
	nm, _ := m.Update(msg)
	return nm.(dashboardModel)
}

func press(m dashboardModel, keys ...string) dashboardModel {
	for _, k := range keys {
		switch k {
		case "enter":
			m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			m = send(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		default:
			m = send(m, tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
		}
	}
	return m
}

func typeText(m dashboardModel, s string) dashboardModel {
	for _, r := range s {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func goodRead(at time.Time, tasks []fleet.TaskSnapshot, failures []fleet.SourceFailure, events []orchestrator.Record) dashRead {
	return dashRead{at: at, latest: true, snap: &fleet.DisplaySnapshot{Tasks: tasks, Failures: failures}, events: events}
}

func ev(id uint64, typ, producer, key, payload string) orchestrator.Record {
	return orchestrator.Record{ID: id, Timestamp: dashNow.Add(time.Duration(id) * time.Second).UnixNano(), Type: typ, Producer: producer, Key: key, Payload: payload}
}

func row(id, state, desc, source, home string) fleet.TaskSnapshot {
	return fleet.TaskSnapshot{ID: id, Project: "munsu", Kind: "ship", CurrentState: state, CurrentDescription: desc, Source: source, Home: home}
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	got = ansiSeq.ReplaceAllString(got, "") + "\n"
	path := filepath.Join("testdata", "dashboard", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestDashboardGoldenHealthy(t *testing.T) {
	m := testDashModel()
	m = send(m, goodRead(dashNow.Add(-3*time.Second), []fleet.TaskSnapshot{
		row("t-work", "working", "implementing", "primary", ""),
		row("t-human", "blocked", "needs a decision", "primary", ""),
		row("t-cap", "working", "in captain home", "captain:alpha", "/h/captains/alpha"),
		row("t-done", "done", "merged", "primary", ""),
	}, nil, []orchestrator.Record{
		ev(1, "task.status", "t-work", "k1", "working: started"),
		ev(2, "task.status", "t-human", "k2", "blocked: needs a decision"),
	}))
	m.skipped = 2
	assertGolden(t, "healthy", m.frame())
}

func TestDashboardGoldenLoading(t *testing.T) {
	assertGolden(t, "loading", testDashModel().frame())
}

func TestDashboardGoldenEmpty(t *testing.T) {
	m := send(testDashModel(), goodRead(dashNow, nil, nil, nil))
	assertGolden(t, "empty", m.frame())
}

func TestDashboardGoldenFailedHome(t *testing.T) {
	m := testDashModel()
	r := goodRead(dashNow, []fleet.TaskSnapshot{row("t-work", "working", "implementing", "primary", "")},
		[]fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("reading canonical task authority state for /h/captains/beta: home: not initialized")}}, nil)
	r.latest, r.eventErr = true, errors.New("open /h/state/events.log: permission denied")
	m = send(m, r)
	assertGolden(t, "failed_home", m.frame())
}

func TestDashboardGoldenStale(t *testing.T) {
	m := testDashModel()
	m = send(m, goodRead(dashNow.Add(-45*time.Second), []fleet.TaskSnapshot{
		row("t-work", "working", "implementing", "primary", ""),
		row("t-q", "queued", "waiting", "primary", ""),
	}, nil, nil))
	m = send(m, dashRead{at: dashNow, snapErr: errors.New("reading authoritative current state: no current-state query provided")})
	assertGolden(t, "stale", m.frame())
}

func TestDashboardGoldenTruncated(t *testing.T) {
	var tasks []fleet.TaskSnapshot
	for i := 0; i < 30; i++ {
		tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", "implementing", "primary", ""))
	}
	var events []orchestrator.Record
	for i := 1; i <= 40; i++ {
		events = append(events, ev(uint64(i), "task.status", "t", "k", fmt.Sprintf("working: step %d", i)))
	}
	m := send(testDashModel(), goodRead(dashNow, tasks, nil, events))
	assertGolden(t, "truncated", m.frame())
}

func ids(tasks []fleet.TaskSnapshot) []string {
	var out []string
	for _, ts := range tasks {
		out = append(out, ts.Source+"/"+ts.ID)
	}
	sort.Strings(out)
	return out
}

// For a healthy home the dashboard shows exactly Snapshot's rows.
func TestDashboardRowsEqualSnapshot(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskMeta(t, homeDir, "alpha", "ship")
	cliSeedCanonicalTask(t, homeDir, "bravo", "scout")
	cliSeedCanonicalTask(t, homeDir, "charlie", "ship")

	want, err := fleet.Snapshot(homeDir, snapshotDeps())
	if err != nil {
		t.Fatal(err)
	}
	m := newDashboardModel(homeDir, "/bin/munsu")
	m = send(m, m.read())
	if len(m.failures) != 0 {
		t.Fatalf("failures = %v, want none", m.failures)
	}
	if got := ids(m.rows); !reflect.DeepEqual(got, ids(want.Tasks)) {
		t.Fatalf("dashboard rows = %v, Snapshot rows = %v", got, ids(want.Tasks))
	}
}

// The first read takes the newest events; each later read asks only for
// events after the last ID shown and appends them.
func TestDashboardFeedAppendsAfterCursor(t *testing.T) {
	m := testDashModel()
	var calls []string
	m.readFleet = func() (*fleet.DisplaySnapshot, error) { return &fleet.DisplaySnapshot{}, nil }
	m.latest = func(int) ([]orchestrator.Record, int, error) {
		calls = append(calls, "latest")
		return []orchestrator.Record{ev(1, "t", "p", "k", "one"), ev(2, "t", "p", "k", "two")}, 0, nil
	}
	m.after = func(cursor uint64, _ int) ([]orchestrator.Record, int, error) {
		calls = append(calls, fmt.Sprintf("after %d", cursor))
		return []orchestrator.Record{ev(3, "t", "p", "k", "three")}, 0, nil
	}
	m = send(m, m.read())
	m = send(m, m.read())
	if want := []string{"latest", "after 2"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("event reads = %v, want %v", calls, want)
	}
	var got []string
	for _, e := range m.events {
		got = append(got, e.Payload)
	}
	if want := []string{"one", "two", "three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("feed = %v, want %v", got, want)
	}
}

func TestDashboardRefusesWithoutTerminal(t *testing.T) {
	old := dashboardTerminals
	defer func() { dashboardTerminals = old }()

	// Both stdin and stdout must be terminals; either one alone is refused.
	for _, c := range []struct{ stdin, stdout bool }{{false, false}, {true, false}, {false, true}} {
		dashboardTerminals = func() (bool, bool) { return c.stdin, c.stdout }
		root := NewRootCommand()
		root.SetArgs([]string{"dashboard", "--home", t.TempDir()})
		err := root.Execute()
		var ce *contractError
		if !errors.As(err, &ce) {
			t.Fatalf("stdin=%v stdout=%v: Execute() = %v, want a contract error", c.stdin, c.stdout, err)
		}
		if ce.value.Error.ErrorCode != "not_a_terminal" || !strings.Contains(ce.value.Error.Action, "munsu fleet view") {
			t.Fatalf("stdin=%v stdout=%v: refusal = %+v, want not_a_terminal pointing at munsu fleet view", c.stdin, c.stdout, ce.value.Error)
		}
	}
}

type execCall struct {
	exe  string
	args []string
}

// actionFixture is a model with a failed captain home, a primary row and a
// captain-home row, plus a recorder in place of exec.
func actionFixture(calls *[]execCall) dashboardModel {
	m := testDashModel()
	m.run = func(exe string, args []string) tea.Cmd {
		*calls = append(*calls, execCall{exe, args})
		return nil
	}
	return send(m, goodRead(dashNow, []fleet.TaskSnapshot{
		row("p-1", "working", "primary task", "primary", ""),
		row("c-1", "working", "captain task", "captain:alpha", "/h/captains/alpha"),
	}, []fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("unreadable")}}, nil))
}

func selectTask(t *testing.T, m dashboardModel, id string) dashboardModel {
	t.Helper()
	for i := 0; i < 10; i++ {
		if m.sel.ID == id {
			return m
		}
		m = press(m, "j")
	}
	t.Fatalf("task %s not selectable", id)
	return m
}

func TestDashboardActionArgv(t *testing.T) {
	words := []string{"human", "chat", "go ahead; rm -rf /"}
	tests := []struct {
		name   string
		task   string // empty: the failed captain home
		key    string
		fields []string
		want   []string
	}{
		{"block with dep", "p-1", "b", []string{"dep-1"}, []string{"--home", "/h", "task", "block", "p-1", "--by", "dep-1"}},
		{"block without dep", "p-1", "b", []string{""}, []string{"--home", "/h", "task", "block", "p-1"}},
		{"unblock", "p-1", "u", nil, []string{"--home", "/h", "task", "unblock", "p-1"}},
		{"done", "p-1", "d", nil, []string{"--home", "/h", "task", "done", "p-1"}},
		{"retry", "p-1", "t", nil, []string{"--home", "/h", "task", "retry", "p-1"}},
		{"reopen", "p-1", "o", nil, []string{"--home", "/h", "task", "reopen", "p-1"}},
		{"teardown", "p-1", "x", nil, []string{"--home", "/h", "teardown", "p-1"}},
		{"promote", "p-1", "p", nil, []string{"--home", "/h", "promote", "p-1"}},
		{"send keeps the line one element", "p-1", "s", []string{"hello  \"world\" $HOME"}, []string{"--home", "/h", "send", "p-1", "hello  \"world\" $HOME"}},
		{"hold", "p-1", "h", []string{"approach", "pick a UI"}, []string{"--home", "/h", "decision-hold", "hold", "approach", "--reason", "pick a UI", "--from", "p-1"}},
		{"resolve", "p-1", "e", append([]string{"approach", "use bubbletea", "dep-a dep-b"}, words...),
			[]string{"--home", "/h", "decision-hold", "resolve", "approach", "--answer", "use bubbletea", "--from", "p-1", "--grantor", "human", "--channel", "chat", "--quote", "go ahead; rm -rf /", "--unblock", "dep-a", "--unblock", "dep-b"}},
		{"complete keys", "p-1", "c", append([]string{"k1 k2", ""}, words...),
			[]string{"--home", "/h", "decision-hold", "complete", "p-1", "k1", "k2", "--grantor", "human", "--channel", "chat", "--quote", "go ahead; rm -rf /"}},
		{"complete none", "p-1", "c", append([]string{"", "y"}, words...),
			[]string{"--home", "/h", "decision-hold", "complete", "p-1", "--grantor", "human", "--channel", "chat", "--quote", "go ahead; rm -rf /", "--none"}},
		{"retire from a captain row", "c-1", "R", nil, []string{"--home", "/h", "captain", "retire", "/h/captains/alpha"}},
		{"retire from a failed home", "", "R", nil, []string{"--home", "/h", "captain", "retire", "/h/captains/beta"}},
		{"converge", "p-1", "C", nil, []string{"--home", "/h", "captain", "converge"}},
		{"record-verdict", "p-1", "v", []string{"review-9"}, []string{"--home", "/h", "delivery", "record-verdict", "--reviewer-task", "review-9"}},
		{"pr-merge", "p-1", "m", append([]string{"https://github.com/o/r/pull/1"}, words...),
			[]string{"--home", "/h", "delivery", "pr-merge", "p-1", "https://github.com/o/r/pull/1", "--grantor", "human", "--channel", "chat", "--quote", "go ahead; rm -rf /"}},
		// A captain-home row passes its own home explicitly.
		{"done in a captain home", "c-1", "d", nil, []string{"--home", "/h/captains/alpha", "task", "done", "c-1"}},
		{"send in a captain home", "c-1", "s", []string{"hi"}, []string{"--home", "/h/captains/alpha", "send", "c-1", "hi"}},
		{"pr-merge in a captain home", "c-1", "m", append([]string{"u"}, words...),
			[]string{"--home", "/h/captains/alpha", "delivery", "pr-merge", "c-1", "u", "--grantor", "human", "--channel", "chat", "--quote", "go ahead; rm -rf /"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []execCall
			m := actionFixture(&calls)
			if tt.task != "" { // the failed source is selected first
				m = selectTask(t, m, tt.task)
			}
			m = press(m, tt.key)
			for _, f := range tt.fields {
				m = typeText(m, f)
				m = press(m, "enter")
			}
			if m.mode != modeConfirm {
				t.Fatalf("mode = %v after the prompts, want confirm; notice %q", m.mode, m.notice)
			}
			if !strings.Contains(m.frame(), "Run: ") || len(calls) != 0 {
				t.Fatalf("argv must be shown before exec; calls=%v", calls)
			}
			m = press(m, "y")
			if len(calls) != 1 || calls[0].exe != "/bin/munsu" || !reflect.DeepEqual(calls[0].args, tt.want) {
				t.Fatalf("exec = %+v, want /bin/munsu %q", calls, tt.want)
			}
		})
	}
}

// Rows reorder on refresh; a confirmed action runs against the row captured
// when it was chosen.
func TestDashboardActionBindsCapturedRowAcrossRefresh(t *testing.T) {
	var calls []execCall
	m := selectTask(t, actionFixture(&calls), "c-1")
	m = press(m, "d")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	// A refresh puts new rows ahead of the captured one.
	m = send(m, goodRead(dashNow, []fleet.TaskSnapshot{
		row("p-1", "working", "primary task", "primary", ""),
		row("n-0", "working", "new task", "primary", ""),
		row("c-1", "working", "captain task", "captain:alpha", "/h/captains/alpha"),
	}, []fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("unreadable")}}, nil))
	m = press(m, "y")
	want := []string{"--home", "/h/captains/alpha", "task", "done", "c-1"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}
}

// The selection follows its row when a refresh reorders the rows.
func TestDashboardSelectionFollowsRowAcrossRefresh(t *testing.T) {
	var calls []execCall
	m := selectTask(t, actionFixture(&calls), "c-1")
	m = send(m, goodRead(dashNow, []fleet.TaskSnapshot{
		row("n-0", "working", "new task", "primary", ""),
		row("c-1", "working", "captain task", "captain:alpha", "/h/captains/alpha"),
		row("p-1", "working", "primary task", "primary", ""),
	}, nil, nil))
	press(m, "d", "y")
	want := []string{"--home", "/h/captains/alpha", "task", "done", "c-1"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}
}

// A words form never submits while grantor, channel or quote is empty.
func TestDashboardWordsFormRequiresWords(t *testing.T) {
	forms := []struct {
		key    string
		fields []string // values in prompt order, words last
	}{
		{"e", []string{"k", "a", "", "human", "chat", "quote"}},
		{"c", []string{"", "", "human", "chat", "quote"}},
		{"m", []string{"https://github.com/o/r/pull/1", "human", "chat", "quote"}},
	}
	for _, f := range forms {
		for empty := len(f.fields) - 3; empty < len(f.fields); empty++ {
			t.Run(fmt.Sprintf("%s empty field %d", f.key, empty), func(t *testing.T) {
				var calls []execCall
				m := selectTask(t, actionFixture(&calls), "p-1")
				m = press(m, f.key)
				for i, v := range f.fields {
					if i == empty {
						v = ""
					}
					m = typeText(m, v)
					m = press(m, "enter")
				}
				if m.mode != modeForm || len(calls) != 0 {
					t.Fatalf("mode = %v, calls = %v; want the form kept open and no exec", m.mode, calls)
				}
				if m.pending.field != empty {
					t.Fatalf("focus on field %d, want the empty field %d", m.pending.field, empty)
				}
				m = typeText(m, "x")
				for i := 0; i < len(f.fields) && m.mode == modeForm; i++ {
					m = press(m, "enter")
				}
				if m.mode != modeConfirm {
					t.Fatalf("mode = %v after filling the field, want confirm", m.mode)
				}
			})
		}
	}
}

// A stale row is never rendered in a phase color, whatever its phase.
func TestDashboardStaleRowIsNeverColored(t *testing.T) {
	m := testDashModel()
	m = send(m, goodRead(dashNow.Add(-45*time.Second), []fleet.TaskSnapshot{
		row("t-work", "working", "a", "primary", ""),
		row("t-done", "done", "b", "primary", ""),
		row("t-blocked", "blocked", "c", "primary", ""),
		row("t-queued", "queued", "d", "primary", ""),
	}, nil, nil))
	m = press(m, "j") // keep the selected row off the first row
	for _, c := range []lipgloss.Style{dashGreen, dashRed, dashYellow} {
		sgr := strings.SplitN(c.Render("x"), "x", 2)[0]
		for _, l := range strings.Split(m.frame(), "\n") {
			if strings.Contains(l, "t-") && strings.Contains(l, sgr) {
				t.Errorf("stale row rendered in a phase color %q: %q", sgr, l)
			}
		}
	}
}

// A failed read is stale even while the last good read is recent.
func TestDashboardFailedReadInsideWindowIsStale(t *testing.T) {
	m := testDashModel()
	m = send(m, goodRead(dashNow.Add(-2*time.Second), []fleet.TaskSnapshot{row("t-work", "working", "a", "primary", "")}, nil, nil))
	m = send(m, dashRead{at: dashNow, snapErr: errors.New("boom")})
	f := ansiSeq.ReplaceAllString(m.frame(), "")
	if m.state() != stateStale || !strings.Contains(f, "STALE") || strings.Contains(f, "REFRESHED") {
		t.Fatalf("state = %v; want a stale frame:\n%s", m.state(), f)
	}
}

// A read older than the stale window is stale even without an error.
func TestDashboardAgedReadIsStale(t *testing.T) {
	m := testDashModel()
	m = send(m, goodRead(dashNow.Add(-(dashboardStaleAfter+time.Second)), []fleet.TaskSnapshot{row("t-work", "working", "a", "primary", "")}, nil, nil))
	f := ansiSeq.ReplaceAllString(m.frame(), "")
	if m.state() != stateStale || !strings.Contains(f, "STALE") || strings.Contains(f, "REFRESHED") {
		t.Fatalf("state = %v; want a stale frame:\n%s", m.state(), f)
	}
}

// Moving the cursor past the first screen keeps the selected row in the frame.
func TestDashboardScrollKeepsSelectedRowVisible(t *testing.T) {
	var tasks []fleet.TaskSnapshot
	for i := 0; i < 30; i++ {
		tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", "x", "primary", ""))
	}
	m := send(testDashModel(), goodRead(dashNow, tasks, nil, nil))
	for i := 0; i < 25; i++ {
		m = press(m, "j")
	}
	found := false
	for _, l := range strings.Split(ansiSeq.ReplaceAllString(m.frame(), ""), "\n") {
		if strings.HasPrefix(l, "> ") && strings.Contains(l, "t-25") {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected row t-25 is not in the frame:\n%s", ansiSeq.ReplaceAllString(m.frame(), ""))
	}
}

// The action runs on the identity captured at selection even when that row
// vanishes before y; it never falls to whichever row the cursor landed on.
func TestDashboardCapturedRowVanishesBeforeConfirm(t *testing.T) {
	var calls []execCall
	m := selectTask(t, actionFixture(&calls), "c-1")
	m = press(m, "d")
	m = send(m, goodRead(dashNow, []fleet.TaskSnapshot{row("p-1", "working", "primary task", "primary", "")}, nil, nil))
	press(m, "y")
	want := []string{"--home", "/h/captains/alpha", "task", "done", "c-1"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}
}

// pr-merge confirm state: the longest argv the dashboard builds.
func prMergeConfirm(t *testing.T, calls *[]execCall, width, height int) (dashboardModel, []string) {
	t.Helper()
	m := selectTask(t, actionFixture(calls), "p-1")
	m.width, m.height = width, height
	m = press(m, "m")
	fields := []string{"https://github.com/some-org/some-repo/pull/12345", "the human", "the chat channel", "yes merge this one now, after reading the verdict"}
	for _, v := range fields {
		m = typeText(m, v)
		m = press(m, "enter")
	}
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	return m, m.pending.argv
}

// The confirm step shows every argv element, wrapped, never clipped.
func TestDashboardConfirmShowsFullArgv(t *testing.T) {
	var calls []execCall
	m, argv := prMergeConfirm(t, &calls, 60, 24)
	lines := strings.Split(ansiSeq.ReplaceAllString(m.frame(), ""), "\n")
	if len(lines) > m.height {
		t.Fatalf("frame has %d lines, terminal has %d", len(lines), m.height)
	}
	for _, l := range lines {
		if lipgloss.Width(l) > m.width {
			t.Errorf("line wider than the terminal: %q", l)
		}
	}
	want := "Run: " + argvLine(append([]string{m.exe}, argv...))
	if got := strings.Join(lines, ""); !strings.Contains(got, want) {
		t.Fatalf("frame does not show the whole argv\nwant %q\n--- frame ---\n%s", want, strings.Join(lines, "\n"))
	}
}

// A short terminal keeps the confirm block; the task body gives up rows.
func TestDashboardShortTerminalKeepsConfirmBlock(t *testing.T) {
	var calls []execCall
	m, argv := prMergeConfirm(t, &calls, 100, 9)
	lines := strings.Split(ansiSeq.ReplaceAllString(m.frame(), ""), "\n")
	if len(lines) > m.height {
		t.Fatalf("frame has %d lines, terminal has %d", len(lines), m.height)
	}
	want := "Run: " + argvLine(append([]string{m.exe}, argv...))
	if got := strings.Join(lines, ""); !strings.Contains(got, want) || !strings.Contains(got, "y run") {
		t.Fatalf("confirm block cut by the short terminal:\n%s", strings.Join(lines, "\n"))
	}
}

// When the confirm block cannot fit at all, the frame says so and y runs nothing.
func TestDashboardConfirmTooSmallRefusesY(t *testing.T) {
	var calls []execCall
	m, _ := prMergeConfirm(t, &calls, 60, 24)
	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 4})
	if f := ansiSeq.ReplaceAllString(m.frame(), ""); !strings.Contains(f, "Enlarge") {
		t.Fatalf("frame lacks the enlarge notice:\n%s", f)
	}
	m = press(m, "y")
	if len(calls) != 0 || m.mode != modeConfirm {
		t.Fatalf("calls = %v, mode = %v; want no exec and still confirming", calls, m.mode)
	}
	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 24})
	press(m, "y")
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want one exec after the terminal grew", calls)
	}
}

// assertNoRaw fails when the frame carries a hostile character unescaped.
// The dashboard's own styling never emits NUL, a bidi override or SGR 8.
func assertNoRaw(t *testing.T, name, frame string) {
	t.Helper()
	for _, raw := range []string{"\x00", "\u202e", "\x1b[8m", "\u0085", "\u009b", "\u2028", "\xff"} {
		if strings.Contains(frame, raw) {
			t.Errorf("%s: frame carries %q raw:\n%s", name, raw, frame)
		}
	}
}

func assertShown(t *testing.T, name, frame string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(frame, w) {
			t.Errorf("%s: frame lacks the escaped text %q:\n%s", name, w, frame)
		}
	}
}

// Text the dashboard did not write is shown escaped at every render site:
// never raw, never dropped.
func TestDashboardHostileTextIsEscaped(t *testing.T) {
	hostileRow := fleet.TaskSnapshot{
		ID: "i\x00\u202e", Kind: "k\x00", Source: "s\x00\u202e", CurrentState: "p\x00",
		CurrentDescription: "d\x00\u202e\x1b[8m",
	}
	t.Run("rows failures events", func(t *testing.T) {
		m := testDashModel()
		m.home = "/h\x00"
		m.width = 120
		r := goodRead(dashNow, []fleet.TaskSnapshot{hostileRow},
			[]fleet.SourceFailure{{Source: "fs\x00", Err: errors.New("fe\x00\u202e\x1b[8m")}},
			[]orchestrator.Record{{ID: 1, Timestamp: dashNow.UnixNano(), Type: "et\x00", Producer: "ep\x00", Key: "ek\x00", Payload: "pl\x00\u202e\x1b[8m"}})
		m = send(m, r)
		f := m.frame()
		assertNoRaw(t, "rows failures events", f)
		assertShown(t, "rows failures events", f, `/h\x00`, `i\x00\u202e`, `k\x00`, `s\x00\u202e`, `p\x00`, `d\x00\u202e\x1b[8m`,
			`fs\x00`, `fe\x00\u202e\x1b[8m`, `et\x00 ep\x00 ek\x00 pl\x00\u202e\x1b[8m`)
	})
	t.Run("event log error", func(t *testing.T) {
		r := goodRead(dashNow, nil, nil, nil)
		r.eventErr = errors.New("ee\x00\u202e\x1b[8m")
		f := send(testDashModel(), r).frame()
		assertNoRaw(t, "event log error", f)
		assertShown(t, "event log error", f, `ee\x00\u202e\x1b[8m`)
	})
	t.Run("C1 controls, line separator, invalid UTF-8", func(t *testing.T) {
		const in = "c\u0085\u009b\u2028\xff"
		const shown = `c\u0085\u009b\u2028\xff`
		r := goodRead(dashNow, []fleet.TaskSnapshot{{ID: "t-1", Kind: "ship", CurrentState: "working", Source: "primary", CurrentDescription: in}}, nil,
			[]orchestrator.Record{{ID: 1, Timestamp: dashNow.UnixNano(), Type: "t", Producer: "p", Key: "k", Payload: in}})
		f := send(testDashModel(), r).frame()
		assertNoRaw(t, "other classes", f)
		assertShown(t, "other classes", f, "ship    primary          "+shown, "t p k "+shown)

		var calls []execCall
		m := selectTask(t, actionFixture(&calls), "p-1")
		m = press(m, "s")
		m = send(m, tea.PasteMsg{Content: in})
		m = press(m, "enter")
		f = m.frame()
		assertNoRaw(t, "other classes argv", f)
		assertShown(t, "other classes argv", f, `"`+shown+`"`)
	})
	t.Run("status falls back to last status", func(t *testing.T) {
		hostileRow.CurrentDescription, hostileRow.LastStatus = "", "ls\x00"
		f := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{hostileRow}, nil, nil)).frame()
		assertNoRaw(t, "last status", f)
		assertShown(t, "last status", f, `ls\x00`)
	})
	t.Run("stale read error", func(t *testing.T) {
		m := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{row("t-1", "working", "x", "primary", "")}, nil, nil))
		m = send(m, dashRead{at: dashNow, snapErr: errors.New("re\x00\u202e\x1b[8m")})
		f := m.frame()
		assertNoRaw(t, "stale read error", f)
		assertShown(t, "stale read error", f, `re\x00\u202e\x1b[8m`)
	})
	t.Run("failed first read", func(t *testing.T) {
		m := send(testDashModel(), dashRead{at: dashNow, snapErr: errors.New("fr\x00\u202e\x1b[8m")})
		f := m.frame()
		assertNoRaw(t, "failed first read", f)
		assertShown(t, "failed first read", f, `FAILED fr\x00\u202e\x1b[8m`, `Fleet read failed: fr\x00\u202e\x1b[8m`)
	})
	t.Run("form value and confirm argv", func(t *testing.T) {
		var calls []execCall
		m := selectTask(t, actionFixture(&calls), "p-1")
		m.exe = "/bin/mu\x00"
		m = press(m, "s")
		m = send(m, tea.PasteMsg{Content: "hi\x1b[8mSECRET\x00\u202e"})
		f := m.frame()
		assertNoRaw(t, "form", f)
		assertShown(t, "form", f, `hi\x1b[8mSECRET\x00\u202e`)
		m = press(m, "enter")
		if m.mode != modeConfirm {
			t.Fatalf("mode = %v, want confirm", m.mode)
		}
		f = m.frame()
		assertNoRaw(t, "confirm", f)
		assertShown(t, "confirm", f, `"hi\x1b[8mSECRET\x00\u202e"`, `"/bin/mu\x00"`)
		if got := m.pending.argv[len(m.pending.argv)-1]; got != "hi\x1b[8mSECRET\x00\u202e" {
			t.Fatalf("argv element changed to %q; display escaping must not alter what runs", got)
		}
	})
	t.Run("result and notice", func(t *testing.T) {
		m := testDashModel()
		m.notice = "n\x00"
		m.result = &dashExecDone{argv: []string{"/bin/mu\x00", "a\u202e"}, err: errors.New("er\x00"), output: "o\x1b[8m\nline\u202e"}
		f := m.frame()
		assertNoRaw(t, "result", f)
		assertShown(t, "result", f, `n\x00`, `"/bin/mu\x00" "a\u202e"`, `er\x00`, `o\x1b[8m`, `line\u202e`)
	})
}

// The confirm step runs only when the header and the whole confirm block fit
// the terminal exactly; one row shorter it runs nothing, even though the
// footer alone would still fit.
func TestDashboardConfirmFitBoundary(t *testing.T) {
	var calls []execCall
	m, _ := prMergeConfirm(t, &calls, 60, 40)
	lines := strings.Split(ansiSeq.ReplaceAllString(m.frame(), ""), "\n")
	const header = 2
	if !strings.HasPrefix(lines[0], "munsu dashboard") || !strings.HasPrefix(lines[1], "unresolved") {
		t.Fatalf("header is not two lines:\n%s", strings.Join(lines, "\n"))
	}
	footer := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "Run: ") {
			footer = len(lines) - i
			break
		}
	}
	if footer == 0 {
		t.Fatalf("no confirm block in the frame:\n%s", strings.Join(lines, "\n"))
	}

	exact := send(m, tea.WindowSizeMsg{Width: 60, Height: header + footer})
	if n := len(strings.Split(exact.frame(), "\n")); n > exact.height {
		t.Fatalf("frame has %d lines at height %d", n, exact.height)
	}
	press(exact, "y")
	if len(calls) != 1 {
		t.Fatalf("calls = %d at the exact fit height %d, want 1", len(calls), header+footer)
	}
	short := send(m, tea.WindowSizeMsg{Width: 60, Height: header + footer - 1})
	press(short, "y")
	if len(calls) != 1 {
		t.Fatalf("calls = %d one row below the fit height, want no further exec", len(calls))
	}
}

// wrap never makes a line wider than w, at the token boundaries w and w+1,
// and loses no character.
func TestDashboardWrapBoundaries(t *testing.T) {
	const w = 8
	for _, in := range []string{
		strings.Repeat("x", w),
		strings.Repeat("x", w+1),
		"ab " + strings.Repeat("y", w),
		"ab " + strings.Repeat("y", w+1),
	} {
		lines := wrap(in, w)
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("wrap(%q, %d) has the %d-cell line %q", in, w, lipgloss.Width(l), l)
			}
		}
		if got := strings.Join(lines, ""); got != in {
			t.Errorf("wrap(%q, %d) lines join to %q", in, w, got)
		}
	}
}

// A fresh row whose pane state is unknown is never rendered green.
func TestDashboardUnknownPhaseIsNeverGreen(t *testing.T) {
	m := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-first", "working", "x", "primary", ""),
		{ID: "t-unk", Kind: "ship", PaneAliveUnknown: true, Source: "primary"},
	}, nil, nil))
	green := strings.SplitN(dashGreen.Render("x"), "x", 2)[0]
	found := false
	for _, l := range strings.Split(m.frame(), "\n") {
		if strings.Contains(l, "t-unk") {
			found = true
			if !strings.Contains(l, "unknown") || strings.Contains(l, green) {
				t.Errorf("unknown-phase row is green or lacks its phase: %q", l)
			}
		}
	}
	if !found {
		t.Fatal("unknown-phase row not rendered")
	}
}

// Every frame line is bounded by the terminal width, ends outside any escape
// sequence, and leaves no style open, at every width from 1 to 200: the
// selected row (reverse) is wider than the terminal and the next row is not
// selected. Every width is run because a cut can land in any escape sequence.
func TestDashboardFrameLinesAreBoundedAndClosed(t *testing.T) {
	long := strings.Repeat("a long status ", 20)
	base := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-sel", "working", long, "primary", ""),
		row("t-next", "working", "next", "primary", ""),
	}, nil, []orchestrator.Record{ev(1, "task.status", "t", "k", long)}))
	base.height = 12
	for w := 1; w <= 200; w++ {
		m := base
		m.width = w
		for n, l := range strings.Split(m.frame(), "\n") {
			if got := ansi.StringWidth(l); got > w {
				t.Fatalf("width %d line %d is %d cells: %q", w, n, got, l)
			}
			if strings.Contains(ansiSeq.ReplaceAllString(l, ""), "\x1b") {
				t.Fatalf("width %d line %d ends inside an escape sequence: %q", w, n, l)
			}
			open := false
			for _, sgr := range ansiSeq.FindAllString(l, -1) {
				open = sgr != "\x1b[m" && sgr != "\x1b[0m"
			}
			if open {
				t.Fatalf("width %d line %d leaves its style open: %q", w, n, l)
			}
		}
	}
}

// A retired task is finished: it is not counted as unresolved.
func TestDashboardRetiredIsNotUnresolved(t *testing.T) {
	m := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-ret", "retired", "x", "primary", ""),
		row("t-work", "working", "x", "primary", ""),
	}, nil, nil))
	if n := m.unresolved(); n != 1 {
		t.Fatalf("unresolved = %d, want 1 (the working row)", n)
	}
}

// captain recover takes the registry ID, which need not equal the directory
// name; a home no registered captain owns is not bindable.
func TestDashboardRecoverResolvesRegistryID(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := filepath.Join(parent, "captains", "dir-name")
	if err := os.MkdirAll(captainHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Register(parent, "registry-id", captainHome, "", ""); err != nil {
		t.Fatal(err)
	}
	strayHome := filepath.Join(parent, "captains", "stray")
	if err := os.MkdirAll(strayHome, 0o755); err != nil {
		t.Fatal(err)
	}

	var calls []execCall
	m := newDashboardModel(parent, "/bin/munsu")
	m.run = func(exe string, args []string) tea.Cmd {
		calls = append(calls, execCall{exe, args})
		return nil
	}
	m = send(m, goodRead(dashNow, []fleet.TaskSnapshot{
		row("c-1", "working", "in captain home", "captain:dir-name", captainHome),
		row("s-1", "working", "in stray home", "captain:stray", strayHome),
	}, nil, nil))

	m = press(m, "V", "y")
	want := []string{"--home", parent, "captain", "recover", "registry-id"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}

	m = send(m, dashExecDone{})
	m = press(m, "j", "V")
	if m.mode != modeBrowse || len(calls) != 1 || !strings.Contains(m.notice, "not bindable") {
		t.Fatalf("stray home: mode=%v calls=%d notice=%q; want not bindable and no exec", m.mode, len(calls), m.notice)
	}
}
