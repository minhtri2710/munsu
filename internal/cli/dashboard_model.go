package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

const (
	// dashboardTick is the refresh interval. A tick starts a read only when
	// none is in flight, so a slow read never stacks a second one.
	dashboardTick = 2 * time.Second
	// dashboardStaleAfter marks rows stale when no read has succeeded for this
	// long, for example because a read is hung.
	dashboardStaleAfter = 3 * dashboardTick
	// dashboardFeedMax bounds the events the model keeps and each read returns.
	dashboardFeedMax = 200
	// dashboardOutputLines bounds the subprocess output tail shown after a run.
	dashboardOutputLines = 4
)

type dashMode int

const (
	modeBrowse dashMode = iota
	modeForm
	modeConfirm
	modeRunning
)

// dashRead is one completed read: the fleet display read and the event feed
// read, each with its own error.
type dashRead struct {
	at       time.Time
	snap     *fleet.DisplaySnapshot
	snapErr  error
	latest   bool // events came from LatestEvents (replace) rather than EventsAfter (append)
	events   []orchestrator.Record
	skipped  int
	eventErr error
}

type dashTickMsg struct{}

// dashExecDone reports a finished action subprocess.
type dashExecDone struct {
	argv   []string
	err    error
	output string
}

// dashItem is one selectable line: a failed source or a task row.
type dashItem struct {
	failure *fleet.SourceFailure
	row     *fleet.TaskSnapshot
}

// dashPending is the action in progress. Its target and argv are captured
// before any refresh can reorder the rows.
type dashPending struct {
	action *dashAction
	target dashTarget
	home   string
	values []string
	field  int
	argv   []string
}

type dashboardModel struct {
	home, exe     string
	now           func() time.Time
	width, height int

	readFleet func() (*fleet.DisplaySnapshot, error)
	latest    func(n int) ([]orchestrator.Record, int, error)
	after     func(cursor uint64, n int) ([]orchestrator.Record, int, error)
	// run starts the action subprocess. Tests replace it; nothing else execs.
	run func(exe string, args []string) tea.Cmd
	// captains lists the registered captains of the primary home.
	captains func() ([]fleet.Info, error)

	reading  bool
	readDone bool
	lastGood time.Time
	readErr  error
	rows     []fleet.TaskSnapshot
	failures []fleet.SourceFailure

	events     []orchestrator.Record
	cursor     uint64
	feedLoaded bool
	skipped    int
	eventErr   error

	cur int
	sel dashTarget

	mode    dashMode
	pending *dashPending
	notice  string // one-line message for the footer
	result  *dashExecDone
}

func newDashboardModel(home, exe string) dashboardModel {
	return dashboardModel{
		home:    home,
		exe:     exe,
		now:     time.Now,
		width:   80,
		height:  24,
		reading: true,
		readFleet: func() (*fleet.DisplaySnapshot, error) {
			return fleet.SnapshotDisplay(home, snapshotDeps())
		},
		latest: func(n int) ([]orchestrator.Record, int, error) { return orchestrator.LatestEvents(home, n) },
		after: func(c uint64, n int) ([]orchestrator.Record, int, error) {
			return orchestrator.EventsAfter(home, c, n)
		},
		run:      dashboardExec,
		captains: func() ([]fleet.Info, error) { return fleet.ListCaptains(home) },
	}
}

// dashboardExec runs the same binary under bubbletea's ExecProcess, which
// suspends the UI for the duration. Output is captured so the exit code and
// the output tail stay visible after the UI returns.
func dashboardExec(exe string, args []string) tea.Cmd {
	c := exec.Command(exe, args...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	argv := append([]string{exe}, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return dashExecDone{argv: argv, err: err, output: out.String()}
	})
}

// read performs one read. It touches no model state, so it is safe to run as a
// command while the model keeps updating.
func (m dashboardModel) read() dashRead {
	r := dashRead{at: m.now(), latest: !m.feedLoaded}
	r.snap, r.snapErr = m.readFleet()
	if r.latest {
		r.events, r.skipped, r.eventErr = m.latest(dashboardFeedMax)
	} else {
		r.events, r.skipped, r.eventErr = m.after(m.cursor, dashboardFeedMax)
	}
	return r
}

func (m dashboardModel) readCmd() tea.Cmd {
	return func() tea.Msg { return m.read() }
}

func (m dashboardModel) tickCmd() tea.Cmd {
	return tea.Tick(dashboardTick, func(time.Time) tea.Msg { return dashTickMsg{} })
}

// Init issues the first read; newDashboardModel starts with a read in flight.
func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(m.readCmd(), m.tickCmd())
}

// startRead begins a read unless one is already in flight.
func (m *dashboardModel) startRead() tea.Cmd {
	if m.reading {
		return nil
	}
	m.reading = true
	return m.readCmd()
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case dashTickMsg:
		return m, tea.Batch(m.tickCmd(), m.startRead())
	case dashRead:
		m.reading = false
		m.applyRead(msg)
	case dashExecDone:
		m.mode, m.pending = modeBrowse, nil
		m.result = &msg
		return m, m.startRead()
	case tea.PasteMsg:
		if m.mode == modeForm {
			m.pending.values[m.pending.field] += msg.Content
		}
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *dashboardModel) applyRead(r dashRead) {
	m.readDone = true
	if r.snapErr != nil {
		m.readErr = r.snapErr
	} else {
		m.readErr = nil
		m.lastGood = r.at
		m.rows = humanFirst(r.snap.Tasks)
		m.failures = r.snap.Failures
		m.reselect()
	}
	if r.eventErr != nil {
		m.eventErr = r.eventErr
		return
	}
	m.eventErr, m.skipped, m.feedLoaded = nil, r.skipped, true
	if r.latest {
		m.events = r.events
	} else {
		m.events = append(m.events, r.events...)
	}
	if over := len(m.events) - dashboardFeedMax; over > 0 {
		m.events = m.events[over:]
	}
	if n := len(r.events); n > 0 {
		m.cursor = r.events[n-1].ID
	}
}

// humanFirst moves Human-needed rows to the front, keeping SnapshotDisplay's
// order inside each group.
func humanFirst(tasks []fleet.TaskSnapshot) []fleet.TaskSnapshot {
	out := make([]fleet.TaskSnapshot, 0, len(tasks))
	for _, ts := range tasks {
		if fleet.HumanNeeded(ts) {
			out = append(out, ts)
		}
	}
	for _, ts := range tasks {
		if !fleet.HumanNeeded(ts) {
			out = append(out, ts)
		}
	}
	return out
}

func (m dashboardModel) items() []dashItem {
	items := make([]dashItem, 0, len(m.failures)+len(m.rows))
	for i := range m.failures {
		items = append(items, dashItem{failure: &m.failures[i]})
	}
	for i := range m.rows {
		items = append(items, dashItem{row: &m.rows[i]})
	}
	return items
}

func (m dashboardModel) targetOf(it dashItem) dashTarget {
	if it.failure != nil {
		return dashTarget{Home: it.failure.Home, Source: it.failure.Source}
	}
	home := it.row.Home
	if home == "" {
		home = m.home
	}
	return dashTarget{ID: it.row.ID, Home: home, Source: it.row.Source}
}

// reselect keeps the selection on the same row after a refresh reorders them;
// a vanished row falls back to the nearest index.
func (m *dashboardModel) reselect() {
	items := m.items()
	if len(items) == 0 {
		m.cur, m.sel = 0, dashTarget{}
		return
	}
	for i, it := range items {
		if m.targetOf(it) == m.sel {
			m.cur = i
			return
		}
	}
	m.cur = min(m.cur, len(items)-1)
	m.sel = m.targetOf(items[m.cur])
}

func (m dashboardModel) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeRunning:
		return m, nil
	case modeConfirm:
		return m.onConfirmKey(key)
	case modeForm:
		return m.onFormKey(key, k.Key().Text)
	}
	m.notice = ""
	switch key {
	case "q":
		return m, tea.Quit
	case "down", "j":
		m.move(1)
	case "up", "k":
		m.move(-1)
	default:
		if a := findDashAction(key); a != nil {
			return m.begin(a), nil
		}
	}
	return m, nil
}

func (m *dashboardModel) move(d int) {
	items := m.items()
	if len(items) == 0 {
		return
	}
	m.cur = max(0, min(m.cur+d, len(items)-1))
	m.sel = m.targetOf(items[m.cur])
}

// begin captures the selected row and starts the action's prompts, or goes
// straight to confirmation when it has none.
func (m dashboardModel) begin(a *dashAction) dashboardModel {
	items := m.items()
	var t dashTarget
	if len(items) > 0 {
		t = m.targetOf(items[m.cur])
	}
	home, problem := a.bind(t, len(items) > 0, m.home)
	if problem != "" {
		m.notice = a.name + ": " + problem
		return m
	}
	if a.needsCaptainID {
		id, err := m.captainIDOf(t.Home)
		if err != nil {
			m.notice = a.name + ": not bindable: " + err.Error()
			return m
		}
		t.CaptainID = id
	}
	m.pending = &dashPending{action: a, target: t, home: home, values: make([]string, len(a.fields))}
	if len(a.fields) == 0 {
		m.confirm()
	} else {
		m.mode = modeForm
	}
	return m
}

// captainIDOf resolves the registry ID of the captain whose canonical home is
// the given row home, comparing the way fleet.Register stores it.
func (m dashboardModel) captainIDOf(rowHome string) (string, error) {
	canon, err := home.CanonicalCaptainHome(rowHome)
	if err != nil {
		return "", fmt.Errorf("home %s: %w", rowHome, err)
	}
	infos, err := m.captains()
	if err != nil {
		return "", err
	}
	for _, c := range infos {
		if c.Home == canon {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no registered captain has home %s", canon)
}

func (m *dashboardModel) confirm() {
	p := m.pending
	v := make(map[string]string, len(p.values))
	for i, f := range p.action.fields {
		v[f.name] = p.values[i]
	}
	p.argv = p.action.buildArgv(p.home, p.target, v)
	m.mode = modeConfirm
}

func (m dashboardModel) onFormKey(key, text string) (tea.Model, tea.Cmd) {
	p := m.pending
	switch key {
	case "esc":
		m.mode, m.pending = modeBrowse, nil
	case "backspace":
		if v := []rune(p.values[p.field]); len(v) > 0 {
			p.values[p.field] = string(v[:len(v)-1])
		}
	case "enter":
		if p.field < len(p.action.fields)-1 {
			p.field++
			return m, nil
		}
		for i, f := range p.action.fields {
			if f.required && p.values[i] == "" {
				p.field = i
				m.notice = f.name + " is required"
				return m, nil
			}
		}
		m.notice = ""
		m.confirm()
	default:
		p.values[p.field] += text
	}
	return m, nil
}

func (m dashboardModel) onConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y":
		if !m.confirmFits() {
			return m, nil
		}
		m.mode, m.result = modeRunning, nil
		return m, m.run(m.exe, m.pending.argv)
	case "esc", "n":
		m.mode, m.pending = modeBrowse, nil
	}
	return m, nil
}

// exitCode is the subprocess exit code; -1 when it never ran to an exit.
func (d dashExecDone) exitCode() int {
	if d.err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(d.err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// outputTail is the last few non-empty lines of the subprocess output.
func (d dashExecDone) outputTail() []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(d.output), "\n") {
		if l = strings.TrimRight(l, " \r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines[max(0, len(lines)-dashboardOutputLines):]
}

// unresolved counts the rows that are not finished, plus one per failed
// source whose rows are unknown. done, resolved and retired rows are finished;
// every other row, including one with no canonical phase, is not.
func (m dashboardModel) unresolved() int {
	n := len(m.failures)
	for _, ts := range m.rows {
		switch taskauthority.Phase(ts.CurrentState) {
		case taskauthority.PhaseDone, taskauthority.PhaseResolved, taskauthority.PhaseRetired:
		default:
			n++
		}
	}
	return n
}

func (m dashboardModel) humanNeeded() int {
	n := 0
	for _, ts := range m.rows {
		if fleet.HumanNeeded(ts) {
			n++
		}
	}
	return n
}

type dashState int

const (
	stateLoading dashState = iota
	stateFailed
	stateStale
	stateEmpty
	statePartial // fresh, but a source or the event log could not be read
	stateRefreshed
)

func (m dashboardModel) state() dashState {
	switch {
	case !m.readDone:
		return stateLoading
	case m.lastGood.IsZero():
		return stateFailed
	case m.readErr != nil || m.now().Sub(m.lastGood) > dashboardStaleAfter:
		return stateStale
	case len(m.rows) == 0 && len(m.failures) == 0:
		return stateEmpty
	case len(m.failures) > 0 || m.eventErr != nil:
		return statePartial
	}
	return stateRefreshed
}
