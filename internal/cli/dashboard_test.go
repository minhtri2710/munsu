package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	return resized(m, 100, 24)
}

func resized(m dashboardModel, w, h int) dashboardModel {
	return send(m, tea.WindowSizeMsg{Width: w, Height: h})
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
		case "pgdown":
			m = send(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
		case "down":
			m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
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
	// The frame pads lines to a common width; the golden keeps no padding.
	lines := strings.Split(ansiSeq.ReplaceAllString(got, ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	got = strings.Join(lines, "\n") + "\n"
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
	for i := 0; i < 40; i++ {
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
		{"complete none runs with no words", "p-1", "c", []string{"", "y", "", "", ""},
			[]string{"--home", "/h", "decision-hold", "complete", "p-1", "--none"}},
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
	if got := m.list.SelectedItem().(dashItem); m.targetOf(got) != m.sel || got.row.ID != "c-1" {
		t.Fatalf("the highlighted row is %q, want the selected row c-1", got.row.ID)
	}
	press(m, "d", "y")
	want := []string{"--home", "/h/captains/alpha", "task", "done", "c-1"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}
}

// A form that carries the Human's words (resolve, pr-merge) never submits
// while grantor, channel or quote is empty. complete takes them optionally
// and is not in this table.
func TestDashboardWordsFormRequiresWords(t *testing.T) {
	forms := []struct {
		key    string
		fields []string // values in prompt order, words last
	}{
		{"e", []string{"k", "a", "", "human", "chat", "quote"}},
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
	// A form frame that does not fit is exactly the esc notice, cut to the
	// width, and the form is drawn otherwise.
	var calls []execCall
	form := press(selectTask(t, actionFixture(&calls), "p-1"), "m")
	var small, drawn bool
	for w := 1; w <= 120; w++ {
		m := resized(form, w, 12)
		f := strings.TrimRight(ansi.Strip(m.frame()), " ")
		isNotice := f == ansi.Truncate("Terminal too small: enlarge it, or esc to cancel.", w, "…")
		if m.fits() == isNotice {
			t.Fatalf("form %dx12: fits = %v, frame is the esc notice = %v:\n%s", w, m.fits(), isNotice, f)
		}
		small, drawn = small || isNotice, drawn || !isNotice
	}
	if !small || !drawn {
		t.Fatalf("the form sweep saw the notice = %v and the form = %v, want both", small, drawn)
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
func prMergeConfirm(t *testing.T, calls *[]execCall, width, height int) dashboardModel {
	t.Helper()
	m := resized(selectTask(t, actionFixture(calls), "p-1"), width, height)
	m = press(m, "m")
	fields := []string{"https://github.com/some-org/some-repo/pull/12345", "the human", "the chat channel", "yes merge this one now, after reading the verdict"}
	for _, v := range fields {
		m = typeText(m, v)
		m = press(m, "enter")
	}
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	return m
}

// sendConfirm is a send action in confirm state with the pasted line value.
func sendConfirm(t *testing.T, calls *[]execCall, value string, width, height int) dashboardModel {
	t.Helper()
	m := resized(selectTask(t, actionFixture(calls), "p-1"), width, height)
	m = press(m, "s")
	m = send(m, tea.PasteMsg{Content: value})
	m = press(m, "enter")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want confirm", m.mode)
	}
	return m
}

func dashNoSpace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// confirmRows are the confirm viewport rows as the stripped frame draws them:
// after the header, notes, list and feed lines, before the footer lines. They
// are nil when the frame is the notice.
func confirmRows(m dashboardModel) []string {
	if !m.fits() {
		return nil
	}
	lines := strings.Split(ansi.Strip(m.frame()), "\n")
	start := len(m.header()) + len(m.notes()) + len(m.body()) + len(m.feedSection())
	end := min(start+m.argv.Height(), len(lines)-len(m.footer()))
	if end < start {
		return nil
	}
	return lines[start:end]
}

// scrollDrawn scrolls the confirm viewport one line at a time to its bottom
// and returns the non-space runes the frames drew of it on the way.
func scrollDrawn(m dashboardModel) (dashboardModel, string) {
	var drawn strings.Builder
	for {
		rows := confirmRows(m)
		if m.argv.AtBottom() {
			drawn.WriteString(strings.Join(rows, ""))
			break
		}
		if len(rows) > 0 {
			drawn.WriteString(rows[0])
		}
		m = press(m, "down")
	}
	return m, dashNoSpace(drawn.String())
}

// The confirm viewport draws every rune of the command: when its gate passes,
// the viewport, paged from the top to the bottom, drew every non-space rune of
// it (a grapheme the wrap cannot place is never drawn, so the gate refuses).
// y runs exactly when that gate passes and the frame fits the terminal; a
// frame that does not fit is the "Terminal too small" notice.
func TestDashboardConfirmShowsFullArgv(t *testing.T) {
	wantOf := func(m dashboardModel) string {
		return dashNoSpace("Run: " + argvLine(append([]string{m.exe}, m.pending.argv...)))
	}
	// The frame claims "The whole command is shown." exactly when y's gate
	// (onScreen) passes.
	assertClaim := func(t *testing.T, name string, w, h int, m dashboardModel) {
		t.Helper()
		const claim = "The whole command is shown."
		got := strings.Contains(ansi.Strip(m.frame()), claim)
		if shown := m.onScreen(); got != shown {
			t.Fatalf("%s %dx%d: footer claim = %v, onScreen = %v:\n%s", name, w, h, got, shown, m.frame())
		}
	}
	run := func(t *testing.T, name string, build func(calls *[]execCall, w, h int) dashboardModel, widths, heights []int, mustRun func(w, h int) bool) {
		t.Helper()
		ran := false
		var calls []execCall
		start := build(&calls, 100, 40)
		for _, h := range heights {
			for _, w := range widths {
				calls = nil
				m := resized(start, w, h)
				m.argv.SetYOffset(0)
				m, drawn := scrollDrawn(m)
				assertClaim(t, name, w, h, m)
				fit := m.fits()
				if f := strings.TrimRight(ansi.Strip(m.frame()), " "); !fit && f != ansi.Truncate("Terminal too small: enlarge it, or esc to cancel.", w, "…") {
					t.Fatalf("%s %dx%d: the frame does not fit and is not the esc notice:\n%s", name, w, h, f)
				}
				press(m, "y")
				if len(calls) == 1 {
					ran = true
					if !fit {
						t.Fatalf("%s %dx%d: y ran in a frame that does not fit", name, w, h)
					}
					if want := wantOf(m); drawn != want {
						t.Fatalf("%s %dx%d: y ran but the viewport drew %q, not the whole command %q", name, w, h, drawn, want)
					}
				} else if fit && mustRun(w, h) {
					t.Fatalf("%s %dx%d: y did not run", name, w, h)
				}
			}
		}
		if !ran {
			t.Fatalf("%s: y never ran", name)
		}
	}
	seq := func(from, to int) []int {
		var out []int
		for i := from; i <= to; i++ {
			out = append(out, i)
		}
		return out
	}
	// Header 2, hints 2, one row: 5 lines is the least that draws anything.
	run(t, "pr-merge", func(calls *[]execCall, w, h int) dashboardModel { return prMergeConfirm(t, calls, w, h) },
		seq(1, 120), []int{5, 6, 9, 40}, func(int, int) bool { return true })
	run(t, "grapheme pairs", func(calls *[]execCall, w, h int) dashboardModel {
		return sendConfirm(t, calls, "a\u2764\ufe0f\u2764\ufe0f\u2764\ufe0fb \u754c\u754c\u754c ab\u754ccd", w, h)
	}, seq(1, 120), []int{5, 8, 40}, func(int, int) bool { return false })

	t.Run("a grapheme wider than the terminal runs nothing", func(t *testing.T) {
		var calls []execCall
		m := sendConfirm(t, &calls, "\u754c", 1, 40)
		m, _ = scrollDrawn(m)
		m = press(m, "y")
		if len(calls) != 0 || m.mode != modeConfirm {
			t.Fatalf("calls = %v, mode = %v; want no exec and still confirming", calls, m.mode)
		}
	})
	t.Run("scrolled down in a terminal with no argv row claims nothing", func(t *testing.T) {
		var calls []execCall
		m := resized(prMergeConfirm(t, &calls, 100, 40), 30, 4)
		for i := 0; i < 40; i++ {
			m = press(m, "down")
		}
		assertClaim(t, "pr-merge", 30, 4, m)
		if !strings.Contains(ansi.Strip(m.frame()), "too small") {
			t.Fatalf("no too-small notice:\n%s", m.frame())
		}
	})
	t.Run("a viewport with no row runs nothing", func(t *testing.T) {
		var calls []execCall
		m := prMergeConfirm(t, &calls, 100, 4)
		m = press(m, "y")
		if len(calls) != 0 || m.mode != modeConfirm {
			t.Fatalf("calls = %v, mode = %v; want no exec and still confirming", calls, m.mode)
		}
	})
}

// y waits for the whole command: at the top of a command taller than its
// viewport it runs nothing and the block says to scroll; once the viewport is
// at its bottom it runs; scrolled back up it waits again.
func TestDashboardConfirmYWaitsForWholeArgv(t *testing.T) {
	var calls []execCall
	m := prMergeConfirm(t, &calls, 50, 7)
	if m.argv.AtBottom() {
		t.Fatalf("fixture: the command fits the %d-row viewport", m.argv.Height())
	}
	m = press(m, "y")
	if len(calls) != 0 || m.mode != modeConfirm || !strings.Contains(m.frame(), "Scroll down") {
		t.Fatalf("calls = %v, mode = %v; want no exec, still confirming, and a scroll notice:\n%s", calls, m.mode, m.frame())
	}
	for i := 0; i < 50 && !m.argv.AtBottom(); i++ {
		m = press(m, "pgdown")
	}
	if !m.argv.AtBottom() || !strings.Contains(m.frame(), "The whole command is shown.") {
		t.Fatalf("not at the bottom after paging:\n%s", m.frame())
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	press(m, "y")
	if len(calls) != 0 {
		t.Fatalf("calls = %v after scrolling back up, want y to wait again", calls)
	}
	press(send(m, tea.KeyPressMsg{Code: tea.KeyPgDown}), "y")
	if len(calls) != 1 {
		t.Fatalf("calls = %d at the bottom, want 1", len(calls))
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
	frame = ansi.Strip(frame)
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

		// A captured row ID is outside text too: it reaches the confirm argv.
		m := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{{ID: "t" + in, Kind: "ship", CurrentState: "working", Source: "primary"}}, nil, nil))
		m.run = func(string, []string) tea.Cmd { return nil }
		m = press(m, "d")
		f = m.frame()
		assertNoRaw(t, "other classes argv", f)
		assertShown(t, "other classes argv", f, `"t`+shown+`"`)
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
		// textinput drops control runes (ESC, NUL) from a paste and keeps a
		// format rune such as the bidi override, which the field draws escaped.
		var calls []execCall
		m := selectTask(t, actionFixture(&calls), "p-1")
		m.exe = "/bin/mu\x00"
		m = press(m, "s")
		m = send(m, tea.PasteMsg{Content: "hi\x1b[8mSECRET\x00\u202e"})
		f := m.frame()
		assertNoRaw(t, "form", f)
		assertShown(t, "form", f, `hi[8mSECRET\u202e`)
		m = press(m, "enter")
		if m.mode != modeConfirm {
			t.Fatalf("mode = %v, want confirm", m.mode)
		}
		f = m.frame()
		assertNoRaw(t, "confirm", f)
		assertShown(t, "confirm", f, `"hi[8mSECRET\u202e"`, `"/bin/mu\x00"`)
		if got := m.pending.argv[len(m.pending.argv)-1]; got != "hi[8mSECRET\u202e" {
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
	trim := func(l string) string { return strings.TrimRight(ansi.Strip(l), " ") }
	wideLines := strings.Split(resized(base, 1000, 12).frame(), "\n")
	for w := 1; w <= 200; w++ {
		m := resized(base, w, 12)
		for n, l := range strings.Split(m.frame(), "\n") {
			if got := ansi.StringWidth(l); got > w {
				t.Fatalf("width %d line %d is %d cells: %q", w, n, got, l)
			}
			// A line the width cuts ends in a visible ellipsis.
			if m.fits() && ansi.StringWidth(trim(wideLines[n])) > w && !strings.HasSuffix(trim(l), "…") {
				t.Fatalf("width %d line %d is cut without a visible ellipsis: %q", w, n, l)
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

	// A task ID longer than its 22-cell field is cut inside the field, so the
	// next column starts at the same cell as for a short ID.
	cols := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-short", "working", "x", "primary", ""),
		row("t-"+strings.Repeat("long-id-", 5), "working", "x", "primary", ""),
	}, nil, nil))
	var starts []int
	var longLine string
	for _, l := range strings.Split(cols.frame(), "\n") {
		l = ansi.Strip(l)
		if i := strings.Index(l, "primary"); i >= 0 && strings.Contains(l, "t-") {
			starts = append(starts, ansi.StringWidth(l[:i]))
			if strings.Contains(l, "long-id") {
				longLine = l
			}
		}
	}
	if len(starts) != 2 || starts[0] != starts[1] {
		t.Fatalf("the source column starts at cells %v, want the same cell for a short and a long ID", starts)
	}
	if !strings.Contains(longLine[:strings.Index(longLine, "primary")], "…") {
		t.Fatalf("the cut ID field has no visible ellipsis: %q", longLine)
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

// exitStatus12 is the error of a real subprocess that exited with code 12: the
// test binary run again with dashExitEnv set, which init answers.
func exitStatus12(t *testing.T) error {
	t.Helper()
	c := exec.Command(os.Args[0])
	c.Env = append(os.Environ(), dashExitEnv+"=1")
	err := c.Run()
	if (dashExecDone{err: err}).exitCode() != 12 {
		t.Fatalf("helper process error = %v, want exit status 12", err)
	}
	return err
}

const dashExitEnv = "MUNSU_DASHBOARD_TEST_EXIT12"

func init() {
	if os.Getenv(dashExitEnv) != "" {
		os.Exit(12)
	}
}

// The shown counts come from the components, and the frame stays inside the
// terminal, in fresh, stale, zero-task, zero-failure and no-event states, at
// every height from tall down to where header and footer alone fill the
// terminal, with the cursor on every item. A "Showing N of M" title is true
// (N rows drawn), a list with no such title drew all its rows, the selected
// row is drawn, and the footer is never what gets cut.
func TestDashboardFrameCountsAndBounds(t *testing.T) {
	type state struct {
		name       string
		nf, nt, ne int
		stale      bool
		feedFailed bool // the events were read, then a later feed read failed
		home       string
		result     bool // a finished command with a long argv and a two-digit exit code
		skipped    int  // malformed lines the good event read skipped
		firstFail  bool // the first and only read failed: no good read before it
		// then moves the model into another mode or adds free text the frame
		// draws; such a row skips the browse-only checks of the fixed-width loop.
		then func(*testing.T, dashboardModel) dashboardModel
		// parts are the exact stripped protected texts the state draws, top
		// to bottom, by terminal height. They are authored, not read off the code.
		parts map[int][]string
	}
	// Free texts, each longer than the widest terminal the sweep uses, so a
	// frame that drew one as protected would show the notice at widths it fits.
	longText := func(what string) string {
		return what + " " + strings.Repeat("with a realistic amount of detail ", 5)
	}
	states := []state{
		{"fresh", 30, 3, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 33  Human-needed 0  failed sources 30", "Failed sources and tasks - Showing 19 of 33 (page 1 of 2)", "Events"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 33  Human-needed 0  failed sources 30", "Failed sources and tasks - Showing 4 of 33 (page 1 of 9)", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 33  Human-needed 0  failed sources 30", "Failed sources and tasks - Showing 0 of 33 (page 1 of 33)", "Events - Showing 0 of the last 3 read"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 33  Human-needed 0  failed sources 30"},
		}},
		{"fresh many tasks", 0, 30, 40, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 30  Human-needed 0  failed sources 0", "Tasks - Showing 19 of 30 (page 1 of 2)", "Events - Showing 5 of the last 40 read"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 30  Human-needed 0  failed sources 0", "Tasks - Showing 4 of 30 (page 1 of 8)", "Events - Showing 2 of the last 40 read"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 30  Human-needed 0  failed sources 0", "Tasks - Showing 0 of 30 (page 1 of 30)", "Events - Showing 0 of the last 40 read"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 30  Human-needed 0  failed sources 0"},
		}},
		{"stale", 30, 3, 3, true, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard STALE last good read 60s ago", "unresolved 33  Human-needed 0  failed sources 30", "Last read failed:", "Failed sources and tasks - Showing 18 of 33 (page 1 of 2)", "Events"},
			12: {"munsu dashboard STALE last good read 60s ago", "unresolved 33  Human-needed 0  failed sources 30", "Last read failed:", "Failed sources and tasks - Showing 3 of 33 (page 1 of 11)", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard STALE last good read 60s ago", "unresolved 33  Human-needed 0  failed sources 30", "Last read failed:", "Failed sources and tasks - Showing 0 of 33 (page 1 of 33)"},
			4:  {"munsu dashboard STALE last good read 60s ago", "unresolved 33  Human-needed 0  failed sources 30", "Last read failed:"},
		}},
		{"stale many tasks", 2, 30, 40, true, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard STALE last good read 60s ago", "unresolved 32  Human-needed 0  failed sources 2", "Last read failed:", "Failed sources and tasks - Showing 18 of 32 (page 1 of 2)", "Events - Showing 5 of the last 40 read"},
			12: {"munsu dashboard STALE last good read 60s ago", "unresolved 32  Human-needed 0  failed sources 2", "Last read failed:", "Failed sources and tasks - Showing 3 of 32 (page 1 of 11)", "Events - Showing 2 of the last 40 read"},
			6:  {"munsu dashboard STALE last good read 60s ago", "unresolved 32  Human-needed 0  failed sources 2", "Last read failed:", "Failed sources and tasks - Showing 0 of 32 (page 1 of 32)"},
			4:  {"munsu dashboard STALE last good read 60s ago", "unresolved 32  Human-needed 0  failed sources 2", "Last read failed:"},
		}},
		{"zero tasks", 5, 0, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 5  Human-needed 0  failed sources 5", "Failed sources and tasks", "Events"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 5  Human-needed 0  failed sources 5", "Failed sources and tasks - Showing 4 of 5 (page 1 of 2)", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 5  Human-needed 0  failed sources 5", "Failed sources and tasks - Showing 0 of 5 (page 1 of 5)", "Events - Showing 0 of the last 3 read"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 5  Human-needed 0  failed sources 5"},
		}},
		{"zero failures", 0, 6, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 6  Human-needed 0  failed sources 0", "Tasks", "Events"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 6  Human-needed 0  failed sources 0", "Tasks - Showing 4 of 6 (page 1 of 2)", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 6  Human-needed 0  failed sources 0", "Tasks - Showing 0 of 6 (page 1 of 6)", "Events - Showing 0 of the last 3 read"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 6  Human-needed 0  failed sources 0"},
		}},
		{"zero events", 3, 6, 0, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 9  Human-needed 0  failed sources 3", "Failed sources and tasks", "Events"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 9  Human-needed 0  failed sources 3", "Failed sources and tasks - Showing 4 of 9 (page 1 of 3)", "Events"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 9  Human-needed 0  failed sources 3", "Failed sources and tasks - Showing 0 of 9 (page 1 of 9)", "Events"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 9  Human-needed 0  failed sources 3"},
		}},
		{"feed unreadable after a read", 2, 6, 40, false, true, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks", "Events - Showing 5 of the last 40 read - unreadable:"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 4 of 8 (page 1 of 2)", "Events - Showing 2 of the last 40 read - unreadable:"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 0 of 8 (page 1 of 8)", "Events - Showing 0 of the last 40 read - unreadable:"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2"},
		}},
		{"long home", 2, 6, 40, false, false, strings.Repeat("h", 70), false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks", "Events - Showing 5 of the last 40 read"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 4 of 8 (page 1 of 2)", "Events - Showing 2 of the last 40 read"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 0 of 8 (page 1 of 8)", "Events - Showing 0 of the last 40 read"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2"},
		}},
		{"command result", 2, 6, 40, false, false, "", true, 0, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks", "Events - Showing 5 of the last 40 read", "exit 12:"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 3 of 8 (page 1 of 3)", "Events - Showing 2 of the last 40 read", "exit 12:"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 0 of 8 (page 1 of 8)", "exit 12:"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "exit 12:"},
		}},
		{"feed unreadable after a read, lines skipped", 2, 6, 40, false, true, "", false, 3, false, nil, map[int][]string{
			30: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks", "Events - Showing 5 of the last 40 read (3 malformed lines skipped) - unreadable:"},
			12: {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 4 of 8 (page 1 of 2)", "Events - Showing 2 of the last 40 read (3 malformed lines skipped) - unreadable:"},
			6:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2", "Failed sources and tasks - Showing 0 of 8 (page 1 of 8)", "Events - Showing 0 of the last 40 read (3 malformed lines skipped) - unreadable:"},
			4:  {"munsu dashboard PARTIAL refreshed 0s ago", "unresolved 8  Human-needed 0  failed sources 2"},
		}},
		{"first read failed", 0, 0, 0, false, false, "", false, 0, true, nil, map[int][]string{
			30: {"munsu dashboard FAILED", "Fleet read failed:", "Events"},
			12: {"munsu dashboard FAILED", "Fleet read failed:", "Events"},
			6:  {"munsu dashboard FAILED", "Fleet read failed:", "Events"},
			4:  {"munsu dashboard FAILED"},
		}},
		{"empty", 0, 0, 0, false, false, "", false, 0, false, nil, map[int][]string{
			30: {"munsu dashboard EMPTY refreshed 0s ago", "unresolved 0  Human-needed 0  failed sources 0", "No tasks and no failed sources.", "Events"},
			12: {"munsu dashboard EMPTY refreshed 0s ago", "unresolved 0  Human-needed 0  failed sources 0", "No tasks and no failed sources.", "Events"},
			6:  {"munsu dashboard EMPTY refreshed 0s ago", "unresolved 0  Human-needed 0  failed sources 0", "No tasks and no failed sources.", "Events"},
			4:  {"munsu dashboard EMPTY refreshed 0s ago", "unresolved 0  Human-needed 0  failed sources 0"},
		}},
		{"loading", 0, 0, 0, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			return testDashModel()
		}, map[int][]string{
			30: {"munsu dashboard LOADING", "Loading fleet...", "Events"},
			12: {"munsu dashboard LOADING", "Loading fleet...", "Events"},
			6:  {"munsu dashboard LOADING", "Loading fleet...", "Events"},
			4:  {"munsu dashboard LOADING"},
		}},
		{"form mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			return press(selectTask(t, m, "t-00"), "m")
		}, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of the last 3 read"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
		}},
		{"confirm mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m = press(selectTask(t, m, "t-00"), "s")
			return press(send(m, tea.PasteMsg{Content: longText("send this line to the task")}), "enter")
		}, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 0 of 3 (page 1 of 3)"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
		}},
		{"running mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m.mode = modeRunning
			return m
		}, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events - Showing 2 of the last 3 read"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of the last 3 read"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 0 of 3 (page 1 of 3)"},
		}},
		{"browse result with output, an error and a notice", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m.result = &dashExecDone{
				argv:   []string{"task", "done", longText("t-00")},
				err:    errors.New(longText("fork/exec /bin/munsu: no such file or directory")),
				output: longText("first line of output") + "\n" + longText("second line of output"),
			}
			m.notice = longText("selected task changed before the action ran")
			return m
		}, map[int][]string{
			30: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events", "exit -1:"},
			12: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of the last 3 read", "exit -1:"},
			6:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "exit -1:"},
			4:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "exit -1:"},
		}},
	}
	// Longer than the widest terminal the sweep uses, so a clip cuts it.
	feedErr := "open /Users/someone/.munsu/projects/example/state/events.log: permission denied, " + strings.Repeat("and more ", 6)
	titleRe := regexp.MustCompile(`^(Tasks|Failed sources and tasks)( - Showing (\d+) of (\d+) \(page \d+ of \d+\))?$`)
	eventsRe := regexp.MustCompile(`^Events( - Showing (\d+) of the last (\d+) read)?`)
	itemRe := regexp.MustCompile(`^[> ] (x captain:c\d\d|[! ] t-\d\d)`)
	eventRe := regexp.MustCompile(`^\d\d:\d\d:\d\d `)
	// The protected parts of a state whose layout changes with the width, by
	// state, height and width.
	narrow := map[string]map[int]map[int][]string{
		"confirm mode": {
			30: {
				100: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events"},
				60:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks", "Events"},
			},
			12: {
				100: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 1 of the last 3 read"},
				60:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of the last 3 read"},
			},
			6: {
				100: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
				60:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
			},
			4: {
				100: {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
				60:  {"munsu dashboard REFRESHED 0s ago", "unresolved 3  Human-needed 0  failed sources 0"},
			},
		},
	}
	for _, st := range states {
		var tasks []fleet.TaskSnapshot
		for i := 0; i < st.nt; i++ {
			tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", longText(fmt.Sprintf("implementing step %d", i)), "primary", ""))
		}
		var failures []fleet.SourceFailure
		for i := 0; i < st.nf; i++ {
			failures = append(failures, fleet.SourceFailure{Source: fmt.Sprintf("captain:c%02d", i), Err: errors.New(longText(fmt.Sprintf("captain %d: open state/tasks.json: permission denied", i)))})
		}
		var events []orchestrator.Record
		for i := 1; i <= st.ne; i++ {
			events = append(events, ev(uint64(i), "task.status", "t", "k", longText(fmt.Sprintf("step %d", i))))
		}
		base := testDashModel()
		if st.home != "" {
			base.home = st.home
		}
		if st.firstFail {
			base = send(base, dashRead{at: dashNow, snapErr: errors.New("scanning captain home /Users/someone/.munsu/projects/example/state/captains/c01: " + feedErr)})
		} else {
			good := goodRead(dashNow, tasks, failures, events)
			good.skipped = st.skipped
			base = send(base, good)
		}
		if st.result {
			base.result = &dashExecDone{argv: []string{"task", "done", strings.Repeat("a long argument ", 10)}, err: exitStatus12(t)}
		}
		if st.feedFailed {
			base = send(base, dashRead{at: dashNow, snap: &fleet.DisplaySnapshot{Tasks: tasks, Failures: failures}, eventErr: errors.New(feedErr)})
		}
		if st.stale {
			base = send(base, dashRead{at: dashNow.Add(time.Minute), snapErr: errors.New(longText("scanning captain home: read tasks: i/o error"))})
			base.now = func() time.Time { return dashNow.Add(time.Minute) }
		}
		total := st.nf + st.nt
		notes := 0
		if st.stale || st.result {
			notes = 1
		}
		if st.then != nil {
			base = st.then(t, base)
		}
		for _, w := range []int{100, 60} {
			if st.then != nil {
				continue
			}
			if st.skipped > 0 && w == 60 {
				// This row's feed title (error plus skipped note) is about 80
				// columns protected, so 60 is the notice; the sweep below
				// covers that width.
				continue
			}
			at := resized(base, w, 24)
			for cur := 0; cur < total; cur++ {
				if cur > 0 {
					at = press(at, "j")
				}
				for h := 30; h >= 1; h-- {
					m := resized(at, w, h)
					lines := strings.Split(ansi.Strip(m.frame()), "\n")
					where := fmt.Sprintf("%s %dx%d cursor %d", st.name, w, h, cur)
					if len(lines) > h {
						t.Fatalf("%s: %d lines, terminal has %d", where, len(lines), h)
					}
					for _, l := range lines {
						if ansi.StringWidth(l) > w {
							t.Fatalf("%s: line wider than the terminal: %q", where, l)
						}
					}
					avail := h - 2 - notes - 2
					if avail < 0 {
						continue
					}
					if !strings.HasPrefix(lines[len(lines)-1], "h hold") || !strings.HasPrefix(lines[len(lines)-2], "j/k move") {
						t.Fatalf("%s: the footer is cut:\n%s", where, strings.Join(lines, "\n"))
					}
					var items, evs, titleN, titleM, feedN, feedM = 0, 0, -1, -1, -1, -1
					var listTitle, feedTitle, selected bool
					for _, l := range lines {
						l = strings.TrimRight(l, " ")
						switch {
						case itemRe.MatchString(l):
							items++
							selected = selected || strings.HasPrefix(l, "> ")
						case eventRe.MatchString(l):
							evs++
						case titleRe.MatchString(l):
							listTitle = true
							if want := map[bool]string{true: "Failed sources and tasks", false: "Tasks"}[st.nf > 0]; titleRe.FindStringSubmatch(l)[1] != want {
								t.Fatalf("%s: list title %q, want %q:\n%s", where, l, want, strings.Join(lines, "\n"))
							}
							if g := titleRe.FindStringSubmatch(l); g[3] != "" {
								titleN, _ = strconv.Atoi(g[3])
								titleM, _ = strconv.Atoi(g[4])
							}
						case strings.HasPrefix(l, "Events"):
							feedTitle = true
							if st.feedFailed && !strings.Contains(l, " - unreadable: o") {
								t.Fatalf("%s: feed title %q does not say the feed is unreadable:\n%s", where, l, strings.Join(lines, "\n"))
							}
							if st.skipped > 0 && !strings.Contains(l, "(3 malformed lines skipped)") {
								t.Fatalf("%s: feed title %q lacks the malformed lines note:\n%s", where, l, strings.Join(lines, "\n"))
							}
							if g := eventsRe.FindStringSubmatch(l); g[2] != "" {
								feedN, _ = strconv.Atoi(g[2])
								feedM, _ = strconv.Atoi(g[3])
							}
						}
					}
					if total > 0 && avail >= 2 && !listTitle || avail >= 2 && !feedTitle {
						t.Fatalf("%s: a section title is missing:\n%s", where, strings.Join(lines, "\n"))
					}
					if listTitle {
						if titleN >= 0 && (items != titleN || titleM != total) || titleN < 0 && items != total {
							t.Fatalf("%s: %d items drawn, title says %d of %d, total %d:\n%s", where, items, titleN, titleM, total, strings.Join(lines, "\n"))
						}
						if items > 0 && !selected {
							t.Fatalf("%s: the selected row is not drawn:\n%s", where, strings.Join(lines, "\n"))
						}
					}
					if feedTitle && st.ne > 0 {
						if feedN >= 0 && (evs != feedN || feedM != st.ne) || feedN < 0 && evs != st.ne {
							t.Fatalf("%s: %d events drawn, title says %d of the last %d, read %d:\n%s", where, evs, feedN, feedM, st.ne, strings.Join(lines, "\n"))
						}
					}
				}
			}
		}
		// The protected parts layout() returns for the state equal the authored
		// table exactly, so a free text made protected fails whatever its length.
		// Then at every width from 1 to 120: the frame is the "Terminal too
		// small" notice exactly when a table entry does not fit, else every entry
		// is drawn whole.
		notice := "Terminal too small: enlarge it."
		if base.mode == modeForm || base.mode == modeConfirm {
			notice = "Terminal too small: enlarge it, or esc to cancel."
		}
		keepsAt := func(w, h int) ([]string, map[string]bool) {
			var got []string
			follows := map[string]bool{} // free text follows the part
			for _, l := range resized(base, w, h).layout() {
				if l.keep.text != "" {
					p := ansi.Strip(l.keep.text)
					got = append(got, p)
					follows[p] = strings.TrimRight(ansi.Strip(l.rest), " ") != ""
				}
			}
			return got, follows
		}
		for _, h := range []int{30, 12, 6, 4} {
			got, follows := keepsAt(1000, h)
			prot := st.parts[h]
			if !slices.Equal(got, prot) {
				t.Fatalf("%s %d: protected parts\n got  %q\n want %q", st.name, h, got, prot)
			}
			// The confirm row's wrapped command takes more rows in a narrower
			// terminal, so its list and feed counts change with the width: the
			// table is authored at two more widths, and the sweep reads the
			// parts at each width.
			for w, want := range narrow[st.name][h] {
				if got, _ := keepsAt(w, h); !slices.Equal(got, want) {
					t.Fatalf("%s %dx%d: protected parts\n got  %q\n want %q", st.name, w, h, got, want)
				}
			}
			for w := 1; w <= 120; w++ {
				f := ansi.Strip(resized(base, w, h).frame())
				where := fmt.Sprintf("%s %dx%d", st.name, w, h)
				lines := strings.Split(f, "\n")
				if len(lines) > h {
					t.Fatalf("%s: %d lines, terminal has %d", where, len(lines), h)
				}
				tooSmall := false
				prot, follows := prot, follows
				if narrow[st.name] != nil {
					prot, follows = keepsAt(w, h)
				}
				for _, p := range prot {
					tooSmall = tooSmall || ansi.StringWidth(p) > w || ansi.StringWidth(p) == w && follows[p]
				}
				drawn := strings.TrimRight(f, " ") == ansi.Truncate(notice, w, "…")
				if drawn != tooSmall {
					t.Fatalf("%s: notice drawn = %v, a protected part is wider than the terminal = %v (%q):\n%s", where, drawn, tooSmall, prot, f)
				}
				for _, l := range lines {
					if ansi.StringWidth(l) > w {
						t.Fatalf("%s: line wider than the terminal: %q", where, l)
					}
				}
				for _, p := range prot {
					if !drawn && !strings.Contains(f, p) {
						t.Fatalf("%s: protected part %q is not drawn whole:\n%s", where, p, f)
					}
				}
			}
		}
	}
}

// No list binding reacts to an action key, q or esc: a key the dashboard
// leaves to the list must not move the cursor, change the page or quit.
func TestDashboardListIgnoresActionKeys(t *testing.T) {
	var tasks []fleet.TaskSnapshot
	for i := 0; i < 30; i++ {
		tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", "x", "primary", ""))
	}
	m := send(testDashModel(), goodRead(dashNow, tasks, nil, nil))
	m = press(m, "pgdown", "j")
	if m.list.Paginator.Page == 0 || m.list.Paginator.Page == m.list.Paginator.TotalPages-1 {
		t.Fatalf("fixture: page %d of %d is not a middle page", m.list.Paginator.Page+1, m.list.Paginator.TotalPages)
	}
	keys := []string{"q", "esc", "ctrl+c"}
	for _, a := range dashActions {
		keys = append(keys, a.key)
	}
	for _, k := range keys {
		msg := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		switch k {
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "ctrl+c":
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		}
		l, cmd := m.list.Update(msg)
		if cmd != nil || l.Index() != m.list.Index() || l.Paginator.Page != m.list.Paginator.Page {
			t.Errorf("key %q reached the list: cmd %v, index %d -> %d, page %d -> %d", k, cmd != nil, m.list.Index(), l.Index(), m.list.Paginator.Page, l.Paginator.Page)
		}
	}
}

// The selection follows its row when a refresh moves it to another page.
func TestDashboardSelectionFollowsRowAcrossPages(t *testing.T) {
	var tasks []fleet.TaskSnapshot
	for i := 0; i < 30; i++ {
		tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", "x", "primary", ""))
	}
	var calls []execCall
	m := testDashModel()
	m.run = func(exe string, args []string) tea.Cmd {
		calls = append(calls, execCall{exe, args})
		return nil
	}
	m = send(m, goodRead(dashNow, tasks, nil, nil))
	m = selectTask(t, m, "t-25")
	// Twelve new rows ahead of it push t-25 two pages on.
	var more []fleet.TaskSnapshot
	for i := 0; i < 12; i++ {
		more = append(more, row(fmt.Sprintf("n-%02d", i), "working", "x", "primary", ""))
	}
	m = send(m, goodRead(dashNow, append(more, tasks...), nil, nil))
	press(m, "d", "y")
	want := []string{"--home", "/h", "task", "done", "t-25"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("exec = %+v, want %q", calls, want)
	}
}
