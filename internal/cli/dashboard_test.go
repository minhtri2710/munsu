package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
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

// dashUnbox is a stripped frame line of a stacked layout without its panel
// border: a top border's title, or a body line's text.
func dashUnbox(l string) string {
	l = strings.TrimRight(l, " ")
	if t, ok := strings.CutPrefix(l, "╭─ "); ok {
		return strings.TrimRight(t, "─╮ ")
	}
	if t, ok := strings.CutPrefix(l, "│ "); ok {
		return strings.TrimRight(strings.TrimSuffix(t, "│"), " ")
	}
	return l
}

func healthyModel(w, h int) dashboardModel {
	m := resized(testDashModel(), w, h)
	work := row("t-work", "working", "implementing", "primary", "")
	work.LaunchedAt, work.PhaseChangedAt = dashNow.Add(-3*time.Hour).UnixNano(), dashNow.Add(-12*time.Minute).UnixNano()
	work.ContextUsed, work.ContextWindow = 92_000, 256_000
	capRow := row("t-cap", "working", "in captain home", "captain:alpha", "/h/captains/alpha")
	capRow.ContextUsed = 9_241
	m = send(m, goodRead(dashNow.Add(-3*time.Second), []fleet.TaskSnapshot{
		work,
		row("t-human", "blocked", "needs a decision", "primary", ""),
		capRow,
		row("t-cap2", "queued", "waiting for a slot", "captain:alpha", "/h/captains/alpha"),
		row("t-done", "done", "merged", "primary", ""),
	}, nil, []orchestrator.Record{
		ev(1, "task.status", "t-work", "k1", "working: started"),
		ev(2, "task.status", "t-human", "k2", "blocked: needs a decision"),
	}))
	m.skipped = 2
	return m
}

// The golden frames pin the look at the default size, at 80x24 and wide
// enough for Detail beside Tasks.
func TestDashboardGoldenHealthy(t *testing.T) {
	assertGolden(t, "healthy", healthyModel(100, 24).frame())
	assertGolden(t, "healthy_80x24", healthyModel(80, 24).frame())
	assertGolden(t, "healthy_150x32", healthyModel(150, 32).frame())
}

// The screen the Human reported: one queued task in one group, and a log that
// still holds events of a task since removed, some from the day before.
func TestDashboardGoldenQueued(t *testing.T) {
	queued := func(w, h int) dashboardModel {
		t1 := row("t1", "queued", "smoke", "primary", "")
		t1.LastStatus = "queued: cli task add"
		at := func(d time.Duration) int64 { return dashNow.Add(d).UnixNano() }
		return send(resized(testDashModel(), w, h), goodRead(dashNow, []fleet.TaskSnapshot{t1}, nil, []orchestrator.Record{
			{ID: 1, Timestamp: at(-22 * time.Hour), Type: "task.status", Producer: "greet-go", Key: "greet-go", Payload: "working: committed 1c74e55"},
			{ID: 2, Timestamp: at(-13 * time.Hour), Type: "task.status", Producer: "t1", Key: "t1", Payload: "queued: cli task add"},
			{ID: 3, Timestamp: at(-12*time.Hour - time.Second), Type: "task.status", Producer: "greet-go", Key: "default", Payload: "resolved: soldier torn down"},
			{ID: 4, Timestamp: at(-time.Minute), Type: "watcher.cycle.failed", Producer: "munsu:watcher", Payload: "probe timed out"},
			{ID: 5, Timestamp: at(-time.Second), Type: "task.status", Producer: "t1", Key: "smoke", Payload: "queued: waiting for a slot"},
		}))
	}
	assertGolden(t, "queued_200x24", queued(200, 24).frame())
	assertGolden(t, "queued_80x24", queued(80, 24).frame())
}

// The feed leaves out an event only when munsu writes its type with a task ID
// as the producer, the producer is no captain, and a complete, fresh snapshot
// holds no task of that ID. Every other event shows.
func TestDashboardEventFilter(t *testing.T) {
	rows := []fleet.TaskSnapshot{row("t-1", "working", "x", "primary", "")}
	complete := send(testDashModel(), goodRead(dashNow, rows, nil, nil))
	partial := send(testDashModel(), goodRead(dashNow, rows, []fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("unreadable")}}, nil))
	stale := send(complete, dashRead{at: dashNow, snapErr: errors.New("hung")})
	removed := ev(1, "task.status", "greet-go", "k", "working: a")
	for _, tc := range []struct {
		name  string
		m     dashboardModel
		e     orchestrator.Record
		shown bool
	}{
		{"task.status of a task in the snapshot", complete, ev(1, "task.status", "t-1", "k", "working: a"), true},
		{"task.status of a removed task", complete, removed, false},
		{"popup.sent of a removed task", complete, ev(1, "popup.sent", "greet-go", "needs-decision", ""), false},
		{"popup.failed of a removed task", complete, ev(1, "popup.failed", "greet-go", "needs-decision", ""), false},
		{"a captain producer the snapshot does not hold", complete, ev(1, "popup.sent", "captain:alpha", "needs-decision", ""), true},
		{"a munsu producer", complete, ev(1, "watcher.cycle.failed", "munsu:watcher", "", "probe"), true},
		{"an operator producer of another type", complete, ev(1, "note", "general", "", "hello"), true},
		{"no producer", complete, ev(1, "note", "", "", "hello"), true},
		{"a removed task with a failed source", partial, removed, true},
		{"a removed task while the read is stale", stale, removed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.m
			m.events = []orchestrator.Record{tc.e}
			shown, hidden := m.shownEvents()
			if got := len(shown) == 1; got != tc.shown || hidden != len(m.events)-len(shown) {
				t.Fatalf("shown = %v hidden %d, want shown %v", got, hidden, tc.shown)
			}
		})
	}
	m := complete
	m.events = []orchestrator.Record{removed}
	if f := ansi.Strip(resized(m, 100, 24).frame()); !strings.Contains(f, "No events yet.") || !strings.Contains(f, "Events (1 of removed tasks hidden)") {
		t.Fatalf("a feed whose events are all left out does not read as empty with one hidden:\n%s", f)
	}
}

// An event of another day is dated; a key equal to its producer is printed once.
func TestDashboardEventRows(t *testing.T) {
	th := testDashModel().theme()
	for _, tc := range []struct {
		name string
		e    orchestrator.Record
		line string
	}{
		{"today", orchestrator.Record{Timestamp: dashNow.Truncate(24 * time.Hour).UnixNano(), Type: "t", Producer: "t-1", Key: "k", Payload: "p"}, "00:00:00 t t-1 k p"},
		{"the day before", orchestrator.Record{Timestamp: dashNow.Truncate(24 * time.Hour).Add(-time.Second).UnixNano(), Type: "t", Producer: "t-1", Key: "k", Payload: "p"}, "Sep 30 23:59:59 t t-1 k p"},
		{"key equal to producer", ev(1, "task.status", "t-1", "t-1", "working: a"), "12:00:01 task.status t-1 working: a"},
		{"no producer", ev(1, "note", "", "", "hello"), "12:00:01 note hello"},
	} {
		if got := ansi.Strip(eventLine(tc.e, dashNow, th)); got != tc.line {
			t.Errorf("%s: line = %q, want %q", tc.name, got, tc.line)
		}
	}
}

// The first read reads the log back, doubling, until the feed has
// dashboardFeedMax events to show, the log ends, or dashboardFeedCap is read,
// so events of removed tasks never push out live ones. A snapshot that may not
// hold every task hides nothing, so one read is enough, and the next read
// reads back again until one is taken against a complete snapshot; after that
// reads only append (the third read in the last two rows reads no log back).
func TestDashboardFeedReadsBackPastHidden(t *testing.T) {
	logOf := func(live, orphans int) []orchestrator.Record {
		var log []orchestrator.Record
		for i := range live + orphans {
			p := "t-1"
			if i >= live {
				p = "gone"
			}
			log = append(log, ev(uint64(i+1), "task.status", p, "k", "working: a"))
		}
		return log
	}
	failed := []fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("unreadable")}}
	rows := []fleet.TaskSnapshot{row("t-1", "working", "x", "primary", "")}
	complete := func() (*fleet.DisplaySnapshot, error) { return &fleet.DisplaySnapshot{Tasks: rows}, nil }
	for _, tc := range []struct {
		name string
		log  []orchestrator.Record
		// snaps are the fleet reads in order, one per dashboard read.
		snaps         []func() (*fleet.DisplaySnapshot, error)
		reads         []int
		shown, hidden int
		newestShownID uint64
	}{
		{"live events behind orphans", logOf(250, 300), nil, []int{200, 400, 800}, 200, 300, 250},
		{"live events first", logOf(300, 0), nil, []int{200}, 200, 0, 300},
		{"a short log", logOf(10, 300), nil, []int{200, 400}, 10, 300, 10},
		{"orphans to the cap", logOf(0, 3000), nil, []int{200, 400, 800, 1600, 2000}, 0, 2000, 0},
		{"a failed source", logOf(250, 300), []func() (*fleet.DisplaySnapshot, error){
			func() (*fleet.DisplaySnapshot, error) {
				return &fleet.DisplaySnapshot{Tasks: rows, Failures: failed}, nil
			},
		}, []int{200}, 200, 0, 550},
		{"a failed first read, then a complete one", logOf(250, 300), []func() (*fleet.DisplaySnapshot, error){
			func() (*fleet.DisplaySnapshot, error) { return nil, errors.New("hung") }, complete, complete,
		}, []int{200, 200, 400, 800}, 200, 300, 250},
		{"a failed source first, then a complete read", logOf(250, 300), []func() (*fleet.DisplaySnapshot, error){
			func() (*fleet.DisplaySnapshot, error) {
				return &fleet.DisplaySnapshot{Tasks: rows, Failures: failed}, nil
			}, complete, complete,
		}, []int{200, 200, 400, 800}, 200, 300, 250},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testDashModel()
			m.readHolds = func(string, *fleet.DisplaySnapshot) (map[dashHoldKey][]dashHold, error) { return nil, nil }
			m.captains = func() ([]fleet.Info, error) { return nil, nil }
			var reads []int
			m.latest = func(n int) ([]orchestrator.Record, int, error) {
				reads = append(reads, n)
				return tc.log[max(0, len(tc.log)-n):], 0, nil
			}
			m.after = func(uint64, int) ([]orchestrator.Record, int, error) { return nil, 0, nil }
			snaps := tc.snaps
			if snaps == nil {
				snaps = []func() (*fleet.DisplaySnapshot, error){complete}
			}
			for _, s := range snaps {
				m.readFleet = s
				m = send(m, m.read())
			}
			shown, hidden := m.shownEvents()
			if !slices.Equal(reads, tc.reads) || len(shown) != tc.shown || hidden != tc.hidden {
				t.Fatalf("reads %v shown %d hidden %d, want reads %v shown %d hidden %d", reads, len(shown), hidden, tc.reads, tc.shown, tc.hidden)
			}
			if len(shown) > 0 && shown[len(shown)-1].ID != tc.newestShownID {
				t.Fatalf("newest shown event %d, want %d", shown[len(shown)-1].ID, tc.newestShownID)
			}
		})
	}
}

// NAME and GROUP are as wide as their longest cell over every row, within
// their least and most widths; GROUP is not drawn while every row shares a
// group.
func TestDashboardColumnsFitContent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rows         []fleet.TaskSnapshot
		group, width int
	}{
		{"one group, short names", []fleet.TaskSnapshot{row("t1", "queued", "x", "primary", "")}, 0, dashColMin[1]},
		{"two groups", []fleet.TaskSnapshot{row("t1", "queued", "x", "primary", ""), row("t2", "queued", "x", "captain:alpha", "/h/c/a")}, len("general"), dashColMin[1]},
		{"a name between the bounds", []fleet.TaskSnapshot{row("t1", "queued", "x", "primary", ""), row("twelve-chars", "queued", "x", "primary", "")}, 0, 12},
		{"a name past the most", []fleet.TaskSnapshot{row("t-"+strings.Repeat("x", 40), "queued", "x", "primary", "")}, 0, dashColW[1]},
		{"a group past the most", []fleet.TaskSnapshot{row("t1", "queued", "x", "primary", ""), row("t2", "queued", "x", "captain:"+strings.Repeat("g", 20), "/h/c/g")}, dashColW[0], dashColMin[1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := send(resized(testDashModel(), 200, 24), goodRead(dashNow, tc.rows, nil, nil)).cols()
			if c[0] != tc.group || c[1] != tc.width {
				t.Fatalf("GROUP %d NAME %d, want %d and %d", c[0], c[1], tc.group, tc.width)
			}
		})
	}
}

// The terminal's background reply picks the theme: the header bar is drawn in
// the dark palette until a light background answers.
func TestDashboardThemeFollowsBackground(t *testing.T) {
	bar := func(dark bool) string { return strings.SplitN(dashThemeFor(dark).bar.Render("x"), "x", 2)[0] }
	m := testDashModel()
	if f := m.frame(); !strings.Contains(f, bar(true)) || strings.Contains(f, bar(false)) {
		t.Fatalf("before a reply the bar is not dark:\n%q", f)
	}
	m = send(m, tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	if f := m.frame(); !strings.Contains(f, bar(false)) || strings.Contains(f, bar(true)) {
		t.Fatalf("after a light reply the bar is not light:\n%q", f)
	}
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

func TestDashboardItemsOrder(t *testing.T) {
	m := testDashModel()
	m.rows = []fleet.TaskSnapshot{
		row("cap-b", "working", "", "captain:beta", ""),
		row("ordinary", "working", "ordinary task", "primary", ""),
		row("cap-a", "working", "", "captain:alpha", ""),
		row("human", "blocked", "needs a decision", "primary", ""),
	}
	m.failures = []fleet.SourceFailure{{Source: "captain:gamma", Err: errors.New("unreadable")}}

	items := m.items()
	var got []string
	for _, it := range items {
		if it := it.(dashItem); it.failure != nil {
			got = append(got, it.failure.Source)
		} else {
			got = append(got, it.row.ID)
		}
	}
	want := []string{"captain:gamma", "human", "ordinary", "cap-a", "cap-b"}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v (failures, then general with Human-needed first, then captains by id)", got, want)
	}
}

func TestDashboardHeaderChips(t *testing.T) {
	for _, tc := range []struct {
		name string
		read func(*dashRead)
		want string
	}{
		{"live watcher", func(r *dashRead) {
			r.beat = home.WatcherBeatStatus{Exists: true, Age: 12 * time.Second}
			r.unacked = 2
		}, "watcher 12s · captains 1 · working 1 · unacked 2"},
		{"stale watcher, unreadable wakes", func(r *dashRead) {
			r.beat = home.WatcherBeatStatus{Exists: true, Stale: true, Age: 400 * time.Second}
			r.unackedErr = errors.New("boom")
		}, "watcher stale 6m40s · captains 1 · working 1 · unacked ?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := goodRead(dashNow, []fleet.TaskSnapshot{
				row("t-work", "working", "", "primary", ""),
				row("t-cap", "queued", "", "captain:alpha", ""),
			}, nil, nil)
			tc.read(&r)
			m := send(testDashModel(), r)
			if got := ansiSeq.ReplaceAllString(m.header()[0].String(), ""); !strings.Contains(got, tc.want) {
				t.Fatalf("title line = %q, want %q", got, tc.want)
			}
		})
	}
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
		events = append(events, ev(uint64(i), "task.status", "t-00", "k", fmt.Sprintf("working: step %d", i)))
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
	initHomeWithTask(t, homeDir, "alpha", "ship")
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
	old := terminals
	defer func() { terminals = old }()

	// Both stdin and stdout must be terminals; either one alone is refused.
	for _, c := range []struct{ stdin, stdout bool }{{false, false}, {true, false}, {false, true}} {
		terminals = func() (bool, bool) { return c.stdin, c.stdout }
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
	for _, c := range []lipgloss.Style{styleOK, styleFail, styleWarn} {
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
// the lines right above the footer lines. They are nil when the frame is the
// notice.
func confirmRows(m dashboardModel) []string {
	if !m.fits() {
		return nil
	}
	m.resize()
	lines := strings.Split(ansi.Strip(m.frame()), "\n")
	end := len(lines) - len(m.footer())
	start := end - m.argv.Height()
	if start < 0 {
		return nil
	}
	return lines[start:end]
}

// scrollDrawn scrolls the confirm viewport one line at a time to its bottom,
// or until a key no longer moves it, and returns the non-space runes the
// frames drew of it on the way.
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
		at := m.argv.YOffset()
		m = press(m, "down")
		if m.argv.YOffset() == at {
			break
		}
	}
	return m, dashNoSpace(drawn.String())
}

// assertWholeBeforeY checks a confirm frame whose terminal was just resized:
// the footer claims the whole command only when the drawn rows are the whole
// command, y runs exactly then, and otherwise y runs once the command has been
// scrolled through from its first row.
func assertWholeBeforeY(t *testing.T, where string, m dashboardModel, calls *[]execCall) {
	t.Helper()
	*calls = nil
	want := dashNoSpace("Run: " + argvLine(append([]string{m.exe}, m.pending.argv...)))
	drawn := dashNoSpace(strings.Join(confirmRows(m), ""))
	claim := strings.Contains(ansi.Strip(m.frame()), "The whole command is shown.")
	if claim && drawn != want {
		t.Fatalf("%s: the frame claims the whole command but drew %q, command %q:\n%s", where, drawn, want, m.frame())
	}
	press(m, "y")
	if ran := len(*calls) == 1; ran != claim {
		t.Fatalf("%s: calls = %d, claim = %v; y must run exactly when the whole command is claimed:\n%s", where, len(*calls), claim, m.frame())
	}
	if claim {
		return
	}
	*calls = nil
	m, drawn = scrollDrawn(m)
	if drawn != want {
		t.Fatalf("%s: scrolled from the top it drew %q, command %q", where, drawn, want)
	}
	press(m, "y")
	if len(*calls) != 1 {
		t.Fatalf("%s: calls = %d after the whole command was drawn, want 1", where, len(*calls))
	}
}

// The confirm viewport draws every rune of the command: when its gate passes,
// the viewport, paged from the top to the bottom, drew every non-space rune of
// it. y runs exactly when that gate passes and the frame fits the terminal; a
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
	// Header 1, NEEDS YOU 1, hints 2, one row: 5 lines is the least that draws anything.
	run(t, "pr-merge", func(calls *[]execCall, w, h int) dashboardModel { return prMergeConfirm(t, calls, w, h) },
		seq(1, 120), []int{5, 6, 7, 10, 40}, func(int, int) bool { return true })
	run(t, "grapheme pairs", func(calls *[]execCall, w, h int) dashboardModel {
		return sendConfirm(t, calls, "a\u2764\ufe0f\u2764\ufe0f\u2764\ufe0fb \u754c\u754c\u754c ab\u754ccd", w, h)
	}, seq(1, 120), []int{6, 9, 40}, func(int, int) bool { return false })

	t.Run("a grapheme wider than the terminal runs nothing", func(t *testing.T) {
		var calls []execCall
		m := sendConfirm(t, &calls, "\u754c", 1, 40)
		m, _ = scrollDrawn(m)
		m = press(m, "y")
		if len(calls) != 0 || m.mode != modeConfirm {
			t.Fatalf("calls = %v, mode = %v; want no exec and still confirming", calls, m.mode)
		}
	})
	// With no row for the argv, scroll keys do nothing: at 30 the frame is the
	// notice, at 100 it fits. Neither claims the command or runs it, and once
	// the terminal has rows the command is still shown from its first row.
	t.Run("scrolled down in a terminal with no argv row claims nothing and runs nothing", func(t *testing.T) {
		for _, w := range []int{30, 100} {
			var calls []execCall
			m := resized(prMergeConfirm(t, &calls, 100, 40), w, 3)
			for i := 0; i < 400; i++ {
				m = press(m, "down")
			}
			if m.argv.Height() != 0 {
				t.Fatalf("%dx3: argv height %d, want a zero-row viewport", w, m.argv.Height())
			}
			assertClaim(t, "pr-merge", w, 3, m)
			if m.fits() != (w == 100) {
				t.Fatalf("%dx3: fits = %v:\n%s", w, m.fits(), m.frame())
			}
			m = press(m, "y")
			if len(calls) != 0 || m.mode != modeConfirm {
				t.Fatalf("%dx3: calls = %v, mode = %v; want no exec and still confirming", w, calls, m.mode)
			}
			assertWholeBeforeY(t, fmt.Sprintf("%dx3 grown to 100x5", w), resized(m, 100, 5), &calls)
		}
	})
	t.Run("a viewport with no row runs nothing", func(t *testing.T) {
		var calls []execCall
		m := prMergeConfirm(t, &calls, 100, 3)
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

	// A width change rewraps the command (the model wraps it itself: a double-width
	// rune must not make a row wider than the viewport), so the viewport goes back
	// to its first row; a height change keeps the offset, clamped to the new bottom. Either
	// way the frame claims the whole command only once its rows were drawn.
	t.Run("a resize after scrolling still draws the whole command before y runs", func(t *testing.T) {
		var tokens, wide []string
		for i := 0; i < 120; i++ {
			tokens = append(tokens, fmt.Sprintf("T%03d", i))
			wide = append(wide, fmt.Sprintf("\U0001F600T%03d", i))
		}
		for _, tc := range []struct {
			name   string
			build  func(calls *[]execCall) dashboardModel
			scroll func(dashboardModel) dashboardModel
			w, h   int
		}{
			{"pr-merge wider", func(c *[]execCall) dashboardModel { return prMergeConfirm(t, c, 50, 6) },
				func(m dashboardModel) dashboardModel { return press(m, "down", "down") }, 120, 6},
			{"120-token send wider", func(c *[]execCall) dashboardModel { return sendConfirm(t, c, strings.Join(tokens, " "), 50, 6) },
				func(m dashboardModel) dashboardModel { return press(m, "down", "down", "down", "down", "down", "down") }, 80, 6},
			{"pr-merge narrowed", func(c *[]execCall) dashboardModel { return prMergeConfirm(t, c, 120, 6) },
				func(m dashboardModel) dashboardModel { return m }, 50, 6},
			{"120 double-width tokens", func(c *[]execCall) dashboardModel { return sendConfirm(t, c, strings.Join(wide, " "), 50, 20) },
				func(m dashboardModel) dashboardModel { return m }, 50, 20},
			{"pr-merge taller, same width", func(c *[]execCall) dashboardModel { return prMergeConfirm(t, c, 50, 6) },
				func(m dashboardModel) dashboardModel {
					for i := 0; i < 50 && !m.argv.AtBottom(); i++ {
						m = press(m, "pgdown")
					}
					return m
				}, 50, 9},
		} {
			var calls []execCall
			m := tc.scroll(tc.build(&calls))
			assertWholeBeforeY(t, tc.name, resized(m, tc.w, tc.h), &calls)
		}
	})
	// confirm() starts at the top: a second, shorter command is not entered
	// already at its bottom because the first one was scrolled.
	t.Run("a second confirm after a scrolled one waits for its whole command", func(t *testing.T) {
		var calls []execCall
		m := prMergeConfirm(t, &calls, 50, 6)
		for i := 0; i < 50 && !m.argv.AtBottom(); i++ {
			m = press(m, "pgdown")
		}
		m = press(m, "esc")
		m = press(m, "s")
		m = send(m, tea.PasteMsg{Content: strings.Repeat("abcdefghi ", 12)})
		m = press(m, "enter")
		if m.mode != modeConfirm || m.argv.Height() >= m.argv.TotalLineCount() {
			t.Fatalf("fixture: mode = %v, viewport %d rows of %d lines; want a confirm taller than its viewport", m.mode, m.argv.Height(), m.argv.TotalLineCount())
		}
		press(m, "y")
		if len(calls) != 0 || strings.Contains(m.frame(), "The whole command is shown.") {
			t.Fatalf("calls = %v; want y to wait until the second command is shown:\n%s", calls, m.frame())
		}
		m, drawn := scrollDrawn(m)
		if want := dashNoSpace("Run: " + argvLine(append([]string{m.exe}, m.pending.argv...))); drawn != want {
			t.Fatalf("drew %q, want %q", drawn, want)
		}
		press(m, "y")
		if len(calls) != 1 {
			t.Fatalf("calls = %d after the whole command was shown, want 1", len(calls))
		}
	})
	// Scroll keys do nothing while the notice hides the command: widening again
	// shows it from its first row.
	t.Run("scrolling under the too-small notice shows the command from its first row", func(t *testing.T) {
		var calls []execCall
		m := resized(prMergeConfirm(t, &calls, 50, 6), 20, 6)
		if m.fits() {
			t.Fatalf("fixture: 20x6 fits:\n%s", m.frame())
		}
		m = press(m, "down", "down", "down")
		assertWholeBeforeY(t, "50x6 after scrolling under the notice", resized(m, 50, 6), &calls)
	})
	t.Run("a second y does not run the command again", func(t *testing.T) {
		var calls []execCall
		m := press(prMergeConfirm(t, &calls, 100, 40), "y")
		if len(calls) != 1 {
			t.Fatalf("calls = %d after y, want 1", len(calls))
		}
		press(m, "y")
		if len(calls) != 1 {
			t.Fatalf("calls = %d after a second y, want 1", len(calls))
		}
	})
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
		ID: "i\x00\u202e", Kind: "k\x00", Source: "captain:s\x00\u202e", CurrentState: "p\x00",
		CurrentDescription: "d\x00\u202e\x1b[8m",
	}
	t.Run("rows failures events", func(t *testing.T) {
		m := testDashModel()
		m.home = "/h\x00"
		m.width = 120
		r := goodRead(dashNow, []fleet.TaskSnapshot{hostileRow, row("t-2", "working", "x", "primary", "")},
			[]fleet.SourceFailure{{Source: "fs\x00", Err: errors.New("fe\x00\u202e\x1b[8m")}},
			[]orchestrator.Record{{ID: 1, Timestamp: dashNow.UnixNano(), Type: "et\x00", Producer: hostileRow.ID, Key: "ek\x00", Payload: "pl\x00\u202e\x1b[8m"}})
		m = send(m, r)
		f := m.frame()
		assertNoRaw(t, "rows failures events", f)
		assertShown(t, "rows failures events", f, `/h\x00`, `i\x00\u202e`, `k\x00`, `s\x00\u202e`, `p\x00`, `d\x00\u202e\x1b[8m`,
			`fs\x00`, `fe\x00\u202e\x1b[8m`, `et\x00 i\x00\u202e ek\x00 pl\x00\u202e\x1b[8m`)
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
			[]orchestrator.Record{{ID: 1, Timestamp: dashNow.UnixNano(), Type: "t", Producer: "t-1", Key: "k", Payload: in}})
		f := send(resized(testDashModel(), 133, 24), r).frame()
		assertNoRaw(t, "other classes", f)
		assertShown(t, "other classes", f, "-              "+shown, "t t-1 k "+shown)

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
		assertShown(t, "failed first read", f, `FAILED   fr\x00\u202e\x1b[8m`, `Fleet read failed: fr\x00\u202e\x1b[8m`)
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

// AGE and SINCE are the time to now in its largest whole unit, and "-" for a
// time the snapshot does not know.
func TestDashboardAge(t *testing.T) {
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{59*time.Second + 999*time.Millisecond, "59s"},
		{time.Minute, "1m"},
		{12*time.Minute + 59*time.Second, "12m"},
		{time.Hour, "1h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{50 * time.Hour, "2d"},
	} {
		if got := dashAge(dashNow, dashNow.Add(-tc.ago).UnixNano()); got != tc.want {
			t.Errorf("dashAge(%v ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := dashAge(dashNow, 0); got != "-" {
		t.Errorf("dashAge(unknown) = %q, want -", got)
	}
}

// CTX is the share of a known window, else pi's compact token count, and "-"
// when the snapshot read no usage; every value fits the 4-cell column.
func TestDashboardCtx(t *testing.T) {
	for _, tc := range []struct {
		used, window int
		want         string
	}{
		{0, 0, "-"},
		{0, 256_000, "-"},
		{9_241, 256_000, "4%"},
		{1_279, 256_000, "0%"},
		{1_280, 256_000, "1%"},
		{256_000, 256_000, "100%"},
		{999, 0, "999"},
		{1_000, 0, "1.0k"},
		{9_241, 0, "9.2k"},
		{9_949, 0, "9.9k"},
		{9_950, 0, "10k"},
		{999_499, 0, "999k"},
		{999_500, 0, "1.0M"},
		{1_349_999, 0, "1.3M"},
		{9_949_999, 0, "9.9M"},
		{9_950_000, 0, "10M"},
		{99_499_999, 0, "99M"},
	} {
		if got := dashCtx(tc.used, tc.window); got != tc.want {
			t.Errorf("dashCtx(%d, %d) = %q, want %q", tc.used, tc.window, got, tc.want)
		}
	}
}

// The AGE and SINCE, CTX, and HARNESS and MODEL columns are drawn from the
// list width the drawn GROUP, NAME, STATE and ROLE leave room for, so a hidden
// GROUP or short names draw them on a narrower terminal. Each gate is pinned
// at its first width and the width before, for three GROUP and NAME widths.
func TestDashboardColumnGates(t *testing.T) {
	rowsOf := func(group, name string) []fleet.TaskSnapshot {
		rows := []fleet.TaskSnapshot{row(name, "working", "x", "primary", "")}
		if group != "" {
			rows = append(rows, row("t-2", "working", "x", "captain:"+group, "/h/c/g"))
		}
		return rows
	}
	short, long := "t-1", strings.Repeat("n", 20)
	for _, tc := range []struct {
		name           string
		rows           []fleet.TaskSnapshot
		age, ctx, seat int // the least list width that draws each
	}{
		{"one group, short names", rowsOf("", short), 66, 71, 104},
		{"general and alpha, short names", rowsOf("alpha", short), 74, 79, 112},
		{"a long group, short names", rowsOf(strings.Repeat("g", 12), short), 79, 84, 117},
		{"a long group, long names", rowsOf(strings.Repeat("g", 12), long), 91, 96, 129},
	} {
		for _, g := range []struct {
			col   int
			least int
		}{{3, tc.age}, {4, tc.age}, {5, tc.ctx}, {7, tc.seat}, {8, tc.seat}} {
			for _, w := range []int{g.least - 1, g.least} {
				m := send(resized(testDashModel(), w+4, 40), goodRead(dashNow, tc.rows, nil, nil))
				if m.listW() != w {
					t.Fatalf("%s: terminal %d gives list width %d, want %d", tc.name, w+4, m.listW(), w)
				}
				if got := m.cols()[g.col] > 0; got != (w >= g.least) {
					t.Errorf("%s: list width %d draws column %d %v, want %v", tc.name, w, g.col, got, w >= g.least)
				}
			}
		}
	}
}

// A row whose seat health the snapshot could not determine reads "unknown"
// and is never green; a probed seat and a phase with no seat keep their phase.
func TestDashboardUnknownPhaseIsNeverGreen(t *testing.T) {
	unknown := func(ts fleet.TaskSnapshot) fleet.TaskSnapshot { ts.PaneAliveUnknown = true; return ts }
	alive := row("t-alive", "working", "x", "primary", "")
	alive.Window, alive.PaneAlive = "@1", true
	m := send(resized(testDashModel(), 100, 40), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-first", "working", "x", "primary", ""),
		{ID: "t-unk", Kind: "ship", PaneAliveUnknown: true, Source: "primary"},
		unknown(row("t-wunk", "working", "x", "primary", "")),
		unknown(row("t-bunk", "blocked", "x", "primary", "")),
		alive,
		unknown(row("t-dunk", "done", "x", "primary", "")),
	}, nil, nil))
	green := strings.SplitN(styleOK.Render("x"), "x", 2)[0]
	frame := m.frame()
	for _, tc := range []struct {
		id, state string
		green     bool
	}{
		{"t-unk", "? unknown", false},
		{"t-wunk", "? working unknown", false},
		{"t-bunk", "? blocked unknown", false},
		{"t-alive", "● working ", true},
		{"t-dunk", "✓ done ", true},
	} {
		var l string
		for _, fl := range strings.Split(frame, "\n") {
			if strings.Contains(fl, tc.id+" ") && strings.Contains(fl, "ship") {
				l = fl
				break
			}
		}
		if plain := ansiSeq.ReplaceAllString(l, ""); !strings.Contains(plain, tc.state) {
			t.Errorf("%s: state %q not in row %q", tc.id, tc.state, plain)
		}
		if strings.Contains(l, green) != tc.green {
			t.Errorf("%s: green = %v, want %v: %q", tc.id, !tc.green, tc.green, l)
		}
	}
}

// Every frame line is bounded by the terminal width, ends outside any escape
// sequence, and leaves no style open, at every width from 1 to 200 and in
// every look: the selected row (highlighted) is wider than the terminal and
// the next row is not selected. Every width is run because a cut can land in
// any escape sequence.
func TestDashboardFrameLinesAreBoundedAndClosed(t *testing.T) {
	long := strings.Repeat("a long status ", 20)
	base := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-sel", "working", long, "primary", ""),
		row("t-next", "working", "next", "primary", ""),
	}, nil, []orchestrator.Record{ev(1, "task.status", "t-sel", "k", long)}))
	selRow := regexp.MustCompile(`t-sel {4}`)
	looks := map[dashLook]bool{}
	for _, h := range []int{6, 12, 24} {
		for w := 1; w <= 200; w++ {
			m := resized(base, w, h)
			layout := m.layout()
			fits := m.fits()
			if fits {
				looks[m.look] = true
			}
			for n, l := range strings.Split(m.frame(), "\n") {
				where := fmt.Sprintf("%dx%d line %d", w, h, n)
				if got := ansi.StringWidth(l); got > w {
					t.Fatalf("%s is %d cells: %q", where, got, l)
				}
				// A line cut to its room shows a visible ellipsis; the selected
				// row and the event are longer than any room.
				s := ansi.Strip(l)
				cut := fits && ansi.StringWidth(layout[n].String()) > layout[n].room
				if (cut || selRow.MatchString(s) || strings.Contains(s, "12:00:01")) && !strings.Contains(s, "…") {
					t.Fatalf("%s is cut without a visible ellipsis: %q", where, s)
				}
				if strings.Contains(ansiSeq.ReplaceAllString(l, ""), "\x1b") {
					t.Fatalf("%s ends inside an escape sequence: %q", where, l)
				}
				open := false
				for _, sgr := range ansiSeq.FindAllString(l, -1) {
					open = sgr != "\x1b[m" && sgr != "\x1b[0m"
				}
				if open {
					t.Fatalf("%s leaves its style open: %q", where, l)
				}
			}
		}
	}
	if !looks[lookFlat] || !looks[lookBoxed] || !looks[lookSide] {
		t.Fatalf("the sweep drew the looks %v, want flat, boxed and side", looks)
	}

	// A task ID longer than its 20-cell field is cut inside the field, so the
	// next column starts at the same cell as for a short ID.
	cols := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("t-short", "working", "x", "primary", ""),
		row("t-"+strings.Repeat("long-id-", 5), "working", "x", "primary", ""),
	}, nil, nil))
	var starts []int
	var longLine string
	for _, l := range strings.Split(cols.frame(), "\n") {
		l = dashUnbox(ansi.Strip(l))
		if i := strings.Index(l, "working"); i >= 0 && strings.Contains(l, "t-") && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "▌")) {
			starts = append(starts, ansi.StringWidth(l[:i]))
			if strings.Contains(l, "long-id") {
				longLine = l
			}
		}
	}
	if len(starts) != 2 || starts[0] != starts[1] {
		t.Fatalf("the state column starts at cells %v, want the same cell for a short and a long ID", starts)
	}
	if !strings.Contains(longLine[:strings.Index(longLine, "working")], "…") {
		t.Fatalf("the cut ID field has no visible ellipsis: %q", longLine)
	}
}

// captain recover takes the registry ID, which need not equal the directory
// name; a home no registered captain owns is not bindable. A primary-home
// captain row takes its registered captain's home and ID. The footer offers
// exactly what begin binds, from the same registry.
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
	failedHome := filepath.Join(parent, "captains", "failed")
	if err := os.MkdirAll(failedHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Register(parent, "failed-id", failedHome, "", ""); err != nil {
		t.Fatal(err)
	}

	var calls []execCall
	m := newDashboardModel(parent, "/bin/munsu")
	m.run = func(exe string, args []string) tea.Cmd {
		calls = append(calls, execCall{exe, args})
		return nil
	}
	m.readFleet = func() (*fleet.DisplaySnapshot, error) {
		return &fleet.DisplaySnapshot{Tasks: []fleet.TaskSnapshot{
			{ID: "captain:registry-id", Kind: "captain", Home: parent, Source: "primary"},
			{ID: "captain:ghost", Kind: "captain", Home: parent, Source: "primary"},
			row("c-1", "working", "in captain home", "captain:dir-name", captainHome),
			row("s-1", "working", "in stray home", "captain:stray", strayHome),
		}, Failures: []fleet.SourceFailure{{Source: "captain:failed", Home: failedHome, Err: errors.New("unreadable")}}}, nil
	}
	m = send(m, m.read())
	canonHome, err := home.CanonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	offers := func(m dashboardModel, k string) bool {
		lit, _ := litKeys(resized(m, 1000, 24).helpLines())
		return slices.ContainsFunc(lit, func(e string) bool { return strings.HasPrefix(e, k+" ") })
	}

	for _, tc := range []struct {
		id      string
		keys    []string
		want    []string
		notice  string
		offered bool
	}{
		{"captain:registry-id", []string{"V", "y"}, []string{"--home", parent, "captain", "recover", "registry-id"}, "", true},
		{"captain:registry-id", []string{"R", "y"}, []string{"--home", parent, "captain", "retire", canonHome}, "", true},
		{"captain:ghost", []string{"R"}, nil, "captain retire: select a row from a captain home", false},
		{"c-1", []string{"V", "y"}, []string{"--home", parent, "captain", "recover", "registry-id"}, "", true},
		{"", []string{"V", "y"}, []string{"--home", parent, "captain", "recover", "failed-id"}, "", true},
		{"s-1", []string{"V"}, nil, "not bindable", false},
	} {
		calls = nil
		sel := selectTask(t, m, tc.id)
		if got := offers(sel, tc.keys[0]); got != tc.offered {
			t.Errorf("%s %s: footer offers %v, want %v", tc.id, tc.keys[0], got, tc.offered)
		}
		got := press(sel, tc.keys...)
		if tc.want != nil && (len(calls) != 1 || !reflect.DeepEqual(calls[0].args, tc.want)) {
			t.Errorf("%s %s: exec = %+v, want %q", tc.id, tc.keys[0], calls, tc.want)
		}
		if tc.want == nil && (got.mode != modeBrowse || len(calls) != 0 || !strings.Contains(got.notice, tc.notice)) {
			t.Errorf("%s %s: mode=%v calls=%d notice=%q; want %q and no exec", tc.id, tc.keys[0], got.mode, len(calls), got.notice, tc.notice)
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

// litKeys splits footer lines at the help separator into their lit and dimmed entries.
func litKeys(lines []string) (lit, dim []string) {
	faint := strings.SplitN(styleMuted.Render("x"), "x", 2)[0]
	for _, l := range lines {
		for _, e := range strings.Split(l, dashHelpSep) {
			if strings.HasPrefix(e, faint) {
				dim = append(dim, ansi.Strip(e))
			} else {
				lit = append(lit, ansi.Strip(e))
			}
		}
	}
	return lit, dim
}

// The key footer offers, on one line, the movement entry, decide on a held
// row, the actions offered for the selected row, then help and quit. ? lists
// every entry, dimming those that do not apply. A key the footer does not
// offer still reaches begin. q quits by its declaration.
func TestDashboardKeyFooter(t *testing.T) {
	const (
		all     = "j/k move • enter decide • b block • u unblock • d done • t retry • o reopen • x teardown • p promote • s send • h hold • e resolve • c complete • R retire • V recover • C converge • v record-verdict • m pr-merge • ? less • q quit"
		tail    = "  ? help  q quit"
		anyKeys = "C converge  v record-verdict"
		notes   = "h hold  c complete  "
		live    = "b block  d done  x teardown  " + notes
		ended   = "t retry  o reopen  x teardown  " + notes
		merge   = "  m pr-merge"
	)
	var calls []execCall
	fixture := actionFixture(&calls)
	inState := func(state, kind string) dashboardModel {
		r := row("p-1", state, "primary task", "primary", "")
		r.Kind = kind
		return send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{r}, nil, nil))
	}
	bound := func(kind, worktree, window string) dashboardModel {
		r := row("p-1", "working", "primary task", "primary", "")
		r.Kind, r.Worktree, r.Window = kind, worktree, window
		return send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{r}, nil, nil))
	}
	captains := send(testDashModel(), goodRead(dashNow, []fleet.TaskSnapshot{
		row("p-1", "working", "primary task", "primary", ""),
	}, []fleet.SourceFailure{{Source: "captains", Home: "/h", Err: errors.New("unlistable")}}, nil))
	held := goodRead(dashNow, []fleet.TaskSnapshot{row("p-1", "working", "primary task", "primary", "")}, nil, nil)
	held.holds = map[dashHoldKey][]dashHold{{"/h", "p-1"}: {{"approach", "pick one"}}}
	registered := func(r dashRead) dashRead {
		r.registry = []fleet.Info{{ID: "alpha", Home: "/h/captains/alpha"}, {ID: "beta", Home: "/h/captains/beta"}}
		r.captainIDs = map[string]string{"/h/captains/alpha": "alpha", "/h/captains/beta": "beta"}
		return r
	}
	failedHome := registered(goodRead(dashNow, []fleet.TaskSnapshot{
		row("p-1", "working", "primary task", "primary", ""),
	}, []fleet.SourceFailure{{Source: "captain:beta", Home: "/h/captains/beta", Err: errors.New("unreadable")}}, nil))
	meta := func(id string) []fleet.TaskSnapshot {
		return []fleet.TaskSnapshot{{ID: id, Kind: "captain", Home: "/h", Source: "primary", Window: "mu-captain"}}
	}
	tests := []struct {
		name string
		m    dashboardModel
		want string
	}{
		{"empty fleet", testDashModel(), anyKeys + tail},
		{"queued task", inState("queued", "ship"), live + anyKeys + tail},
		{"working task, unbound", inState("working", "ship"), live + anyKeys + tail},
		{"working task, endpoint bound only", bound("ship", "", "mu-p-1"), "b block  d done  x teardown  s send  " + notes + anyKeys + tail},
		{"working task, worktree bound only", bound("ship", "/wt/p-1", ""), live + anyKeys + tail},
		{"working task, bound", bound("ship", "/wt/p-1", "mu-p-1"), "b block  d done  x teardown  s send  " + notes + anyKeys + merge + tail},
		{"working review task, bound", bound("review", "/wt/p-1", "mu-p-1"), "b block  d done  x teardown  s send  " + notes + anyKeys + tail},
		{"blocked task", inState("blocked", "ship"), "u unblock  d done  x teardown  " + notes + anyKeys + tail},
		{"done ship task", inState("done", "ship"), ended + anyKeys + tail},
		{"resolved ship task", inState("resolved", "ship"), ended + anyKeys + tail},
		{"retired ship task", inState("retired", "ship"), ended + anyKeys + tail},
		{"done scout task", inState("done", "scout"), "t retry  o reopen  x teardown  p promote  " + notes + anyKeys + tail},
		{"resolved scout task", inState("resolved", "scout"), "t retry  o reopen  x teardown  p promote  " + notes + anyKeys + tail},
		{"working scout task", inState("working", "scout"), live + anyKeys + tail},
		{"task with no phase", inState("", "ship"), anyKeys + tail},
		{"held decision row", send(testDashModel(), held), "enter decide  b block  d done  x teardown  h hold  e resolve  c complete  " + anyKeys + tail},
		{"failed captain-home row, unregistered", fixture, "j/k move  R retire  " + anyKeys + tail},
		{"failed captain-home row, registered", send(testDashModel(), failedHome), "j/k move  R retire  V recover  " + anyKeys + tail},
		{"captains failure row", captains, "j/k move  " + anyKeys + tail},
		{"captain task row", selectTask(t, fixture, "c-1"), "j/k move  " + live + "R retire  " + anyKeys + tail},
		{"primary captain row, registered", send(testDashModel(), registered(goodRead(dashNow, meta("captain:alpha"), nil, nil))), "R retire  V recover  " + anyKeys + tail},
		{"primary captain row, unregistered", send(testDashModel(), registered(goodRead(dashNow, meta("captain:ghost"), nil, nil))), anyKeys + tail},
		{"task named like a captain", send(testDashModel(), registered(goodRead(dashNow, []fleet.TaskSnapshot{row("alpha", "working", "x", "primary", "")}, nil, nil))), live + anyKeys + tail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := resized(tt.m, 1000, 24)
			got := m.helpLines()
			lit, dim := litKeys(got)
			if len(got) != 1 || len(dim) != 0 || strings.Join(lit, "  ") != tt.want {
				t.Fatalf("footer = %q (dimmed %q)\n want    %q", strings.Join(lit, "  "), dim, tt.want)
			}
			if !strings.Contains(ansi.Strip(m.frame()), strings.Join(lit, " • ")) {
				t.Fatalf("frame does not draw the footer:\n%s", m.frame())
			}
			m = press(m, "?")
			got = m.helpLines()
			if len(got) != 1 || ansi.Strip(got[0]) != all {
				t.Fatalf("help footer = %q\n want        %q", got, all)
			}
			if lit, dim := litKeys(got); strings.Join(lit, "  ") != strings.Replace(tt.want, "? help", "? less", 1) {
				t.Fatalf("help lit keys = %q (dimmed %q), want the footer's", strings.Join(lit, "  "), dim)
			}
			if lit, _ := litKeys(press(m, "?").helpLines()); strings.Join(lit, "  ") != tt.want {
				t.Fatalf("a second ? footer = %q, want %q", strings.Join(lit, "  "), tt.want)
			}
		})
	}

	t.Run("help draws exactly the unavailable keys dimmed", func(t *testing.T) {
		lit := func(k, desc string) string {
			return dashKey.Render(k) + " " + testDashModel().theme().subtle.Render(desc)
		}
		for _, tc := range []struct {
			name     string
			m        dashboardModel
			on, dims []string
		}{
			{"empty fleet", testDashModel(), []string{lit("C", "converge"), lit("q", "quit")}, []string{"b block", "R retire", "enter decide", "j/k move"}},
			{"primary task row", selectTask(t, fixture, "p-1"), []string{lit("b", "block"), lit("j/k", "move")}, []string{"m pr-merge", "u unblock", "R retire", "V recover", "enter decide"}},
		} {
			f := press(resized(tc.m, 300, 24), "?").View().Content
			for _, e := range tc.on {
				if !strings.Contains(f, e) {
					t.Errorf("%s: lit %q is not drawn lit:\n%q", tc.name, ansi.Strip(e), f)
				}
			}
			for _, e := range tc.dims {
				k, desc, _ := strings.Cut(e, " ")
				if !strings.Contains(f, styleMuted.Render(e)) || strings.Contains(f, lit(k, desc)) {
					t.Errorf("%s: %q is not drawn dimmed:\n%q", tc.name, e, f)
				}
			}
		}
	})

	t.Run("form and confirm hints are bindings", func(t *testing.T) {
		m := press(resized(selectTask(t, fixture, "p-1"), 100, 40), "s")
		if lit, _ := litKeys([]string{m.footer()[len(m.footer())-1].String()}); strings.Join(lit, "  ") != "enter next/submit  esc cancel" {
			t.Fatalf("form hints = %q", lit)
		}
		m = resized(press(send(m, tea.PasteMsg{Content: strings.Repeat("long line ", 60)}), "enter"), 60, 9)
		if m.argv.AtBottom() {
			t.Fatalf("fixture: the command fits its %d-row viewport", m.argv.Height())
		}
		lit, dim := litKeys([]string{m.footer()[len(m.footer())-1].String()})
		if strings.Join(lit, "  ") != "esc cancel" || strings.Join(dim, "  ") != "y run" {
			t.Fatalf("confirm hints before the whole command: lit %q dimmed %q", lit, dim)
		}
		m, _ = scrollDrawn(m)
		if lit, _ := litKeys([]string{m.footer()[len(m.footer())-1].String()}); strings.Join(lit, "  ") != "y run  esc cancel" {
			t.Fatalf("confirm hints at the bottom = %q", lit)
		}
	})

	t.Run("keys the footer does not offer still reach begin", func(t *testing.T) {
		for key, want := range map[string]string{"b": "task block: select a task row", "R": "captain retire: select a row from a captain home"} {
			m := press(resized(testDashModel(), 1000, 24), key)
			if m.mode != modeBrowse || m.notice != want {
				t.Errorf("%s on an empty fleet: mode %v, notice %q; want browse and %q", key, m.mode, m.notice, want)
			}
		}
		// The command, not the footer, refuses an action the row's phase does
		// not admit: u on a working task still asks to run task unblock.
		m := press(resized(selectTask(t, fixture, "p-1"), 100, 40), "u")
		if m.mode != modeConfirm || !slices.Equal(m.pending.argv, []string{"--home", "/h", "task", "unblock", "p-1"}) {
			t.Fatalf("u on a working task: mode %v, pending %+v; want the confirm of task unblock", m.mode, m.pending)
		}
	})

	t.Run("q quits", func(t *testing.T) {
		_, cmd := testDashModel().Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		if cmd == nil {
			t.Fatal("q returned no command")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("q command = %T, want tea.QuitMsg", cmd())
		}
	})

	// The footer keeps to one line, dropping its last actions while help and
	// quit do not fit; help fills lines by width without splitting an entry.
	t.Run("lines fill by width", func(t *testing.T) {
		const w = 60
		m := selectTask(t, fixture, "p-1")
		wide, _ := litKeys(resized(m, 1000, 24).helpLines())
		narrow := resized(m, w, 24).helpLines()
		lit, _ := litKeys(narrow)
		if len(narrow) != 1 || ansi.StringWidth(ansi.Strip(narrow[0])) > w || len(lit) >= len(wide) ||
			!slices.Equal(lit[:len(lit)-2], wide[:len(lit)-2]) || !slices.Equal(lit[len(lit)-2:], []string{"? help", "q quit"}) {
			t.Fatalf("footer at %d columns = %q, want one line of the first entries of %q, then help and quit", w, lit, wide)
		}
		if next := append(lit[:len(lit)-2:len(lit)-2], wide[len(lit)-2], "? help", "q quit"); ansi.StringWidth(strings.Join(next, " • ")) <= w {
			t.Fatalf("footer at %d columns leaves room for %q: %q", w, wide[len(lit)-2], lit)
		}
		m = press(m, "?")
		one := ansi.Strip(resized(m, 1000, 24).helpLines()[0])
		lines := resized(m, w, 24).helpLines()
		if len(lines) < 3 {
			t.Fatalf("%d help lines at %d columns, want several: %q", len(lines), w, lines)
		}
		var joined []string
		for i, l := range lines {
			l = ansi.Strip(l)
			joined = append(joined, strings.TrimRight(l, " "))
			if ansi.StringWidth(l) > w {
				t.Errorf("line %d is %d wide, terminal has %d: %q", i, ansi.StringWidth(l), w, l)
			}
			if i+1 < len(lines) {
				next := strings.SplitN(ansi.Strip(lines[i+1]), " • ", 2)[0]
				if ansi.StringWidth(l+" • "+next) <= w {
					t.Errorf("line %d leaves room for %q: %q", i, next, l)
				}
			}
		}
		if got := strings.Join(joined, " • "); got != one {
			t.Fatalf("lines split an entry or drop one:\n got  %q\n want %q", got, one)
		}
	})
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
