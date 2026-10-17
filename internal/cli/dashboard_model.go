package cli

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

const (
	// dashboardTick is the refresh interval. A tick starts a read only when
	// none is in flight, so a slow read never stacks a second one.
	dashboardTick = 2 * time.Second
	// dashboardStaleAfter marks rows stale when no read has succeeded for this
	// long, for example because a read is hung.
	dashboardStaleAfter = 3 * dashboardTick
	// dashboardFeedMax bounds the events the feed shows.
	dashboardFeedMax = 200
	// dashboardFeedCap bounds the events the model keeps and a read returns:
	// the first read reads back past hidden events up to it to fill the feed.
	dashboardFeedCap = 2000
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
	holds    map[dashHoldKey][]dashHold
	holdsErr error
	latest   bool // events came from LatestEvents (replace) rather than EventsAfter (append)
	events   []orchestrator.Record
	skipped  int
	eventErr error
	// registry is the captain registry, nil when unreadable; captainIDs maps
	// each captain home of the snapshot and the registry to the registry ID
	// captainIDIn resolves for it.
	registry   []fleet.Info
	captainIDs map[string]string
	// The header chips: the watcher beat and the unacked wakes (queued plus
	// claimed and not yet resolved).
	beat       home.WatcherBeatStatus
	unacked    int
	unackedErr error
}

type dashTickMsg struct{}

// dashExecDone reports a finished action subprocess.
type dashExecDone struct {
	argv   []string
	err    error
	output string
}

// dashItem is one selectable line: a failed source or a task row. It is a
// list.Item; the list never filters, so it has no filter value.
type dashItem struct {
	failure *fleet.SourceFailure
	row     *fleet.TaskSnapshot
}

func (dashItem) FilterValue() string { return "" }

// dashPending is the action in progress. Its target and argv are captured
// before any refresh can reorder the rows.
type dashPending struct {
	action *dashAction
	target dashTarget
	home   string
	inputs []textinput.Model
	field  int
	argv   []string
}

type dashboardModel struct {
	home, exe     string
	now           func() time.Time
	width, height int

	readFleet func() (*fleet.DisplaySnapshot, error)
	readHolds func(primary string, snap *fleet.DisplaySnapshot) (map[dashHoldKey][]dashHold, error)
	latest    func(n int) ([]orchestrator.Record, int, error)
	after     func(cursor uint64, n int) ([]orchestrator.Record, int, error)
	// run starts the action subprocess. Tests replace it; nothing else execs.
	run func(exe string, args []string) tea.Cmd
	// ask runs a decision form. Tests replace it.
	ask func(*huh.Form) tea.Cmd
	// captains lists the registered captains of the primary home.
	captains func() ([]fleet.Info, error)
	// watcher and wakes feed the header chips.
	watcher func(now time.Time) home.WatcherBeatStatus
	wakes   func(now time.Time) (int, error)

	reading    bool
	readDone   bool
	lastGood   time.Time
	readErr    error
	rows       []fleet.TaskSnapshot
	failures   []fleet.SourceFailure
	holds      map[dashHoldKey][]dashHold
	holdsErr   error
	beat       home.WatcherBeatStatus
	unacked    int
	unackedErr error
	registry   []fleet.Info
	captainIDs map[string]string

	events     []orchestrator.Record
	cursor     uint64
	feedLoaded bool
	skipped    int
	eventErr   error

	// list owns the cursor, scrolling and paging of the failed sources and
	// task rows. sel is the identity of the selected item, kept in step with
	// the list so an action and a refresh bind the row, never an index.
	list list.Model
	sel  dashTarget
	// feed shows the event lines, always scrolled to the newest. argv shows
	// the confirm command as loadArgv wrapped it.
	feed viewport.Model
	argv viewport.Model
	// Section heights set by resize: the lines of the list section (title
	// included), of NEEDS YOU, of the detail pane and of the feed section
	// (title included).
	listH, needsH, detailH, feedH int
	// look is the layout resize picked; dark is the terminal's background,
	// which picks the theme.
	look dashLook
	dark bool

	mode     dashMode
	pending  *dashPending
	deciding *dashDecision
	notice   string // one-line message for the footer
	result   *dashExecDone
	help     bool // the footer lists every key, not only those that apply
}

// dashListKeys is the list's whole key map. Every other binding is absent, so
// no action key (b u d t o x s p h e c R V C v m), q or esc reaches the list:
// the default map binds h, u, d, b, v and esc.
func dashListKeys() list.KeyMap {
	return list.KeyMap{
		CursorUp:   key.NewBinding(key.WithKeys("up", "k")),
		CursorDown: key.NewBinding(key.WithKeys("down", "j")),
		PrevPage:   key.NewBinding(key.WithKeys("left", "pgup")),
		NextPage:   key.NewBinding(key.WithKeys("right", "pgdown")),
		GoToStart:  key.NewBinding(key.WithKeys("home")),
		GoToEnd:    key.NewBinding(key.WithKeys("end")),
	}
}

func newDashboardModel(home, exe string) dashboardModel {
	l := list.New(nil, dashRows{}, 80, 24)
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.KeyMap = dashListKeys()

	feed := viewport.New()
	feed.MouseWheelEnabled = false

	argv := viewport.New()
	argv.MouseWheelEnabled = false
	argv.KeyMap.Left.Unbind()
	argv.KeyMap.Right.Unbind()

	return dashboardModel{
		home:    home,
		exe:     exe,
		now:     time.Now,
		width:   80,
		height:  24,
		list:    l,
		feed:    feed,
		argv:    argv,
		reading: true,
		dark:    true,
		readFleet: func() (*fleet.DisplaySnapshot, error) {
			return fleet.SnapshotDisplay(home, snapshotDeps())
		},
		readHolds: dashHolds,
		latest:    func(n int) ([]orchestrator.Record, int, error) { return orchestrator.LatestEvents(home, n) },
		after: func(c uint64, n int) ([]orchestrator.Record, int, error) {
			return orchestrator.EventsAfter(home, c, n)
		},
		run:      dashboardExec,
		ask:      dashAsk,
		captains: func() ([]fleet.Info, error) { return fleet.ListCaptains(home) },
		watcher:  dashBeat(home),
		wakes:    dashUnacked(home),
	}
}

func dashBeat(h string) func(time.Time) home.WatcherBeatStatus {
	return func(now time.Time) home.WatcherBeatStatus { return home.ReadWatcherBeatStatus(h, now) }
}

// dashUnacked counts the wakes the General has not resolved: queued ones and
// claimed ones still held in a lease.
func dashUnacked(h string) func(time.Time) (int, error) {
	return func(now time.Time) (int, error) {
		queued, err := home.PeekWakes(h)
		if err != nil {
			return 0, err
		}
		claimed, err := orchestrator.ListClaimedWakes(h, now)
		return len(queued) + len(claimed), err
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
	if r.snapErr == nil {
		r.holds, r.holdsErr = m.readHolds(m.home, r.snap)
	}
	r.beat = m.watcher(r.at)
	r.unacked, r.unackedErr = m.wakes(r.at)
	r.registry, r.captainIDs = m.readCaptains(r.snap)
	if !r.latest {
		r.events, r.skipped, r.eventErr = m.after(m.cursor, dashboardFeedCap)
		return r
	}
	var snap *fleet.DisplaySnapshot
	if r.snapErr == nil {
		snap = r.snap
	}
	for n := dashboardFeedMax; ; n = min(2*n, dashboardFeedCap) {
		r.events, r.skipped, r.eventErr = m.latest(n)
		if shown, _ := dashShown(r.events, snap); r.eventErr != nil || len(r.events) < n || n == dashboardFeedCap || len(shown) >= dashboardFeedMax {
			return r
		}
	}
}

// readCaptains reads the captain registry and resolves, the way begin does,
// the registry ID of every captain home the snapshot and the registry name.
func (m dashboardModel) readCaptains(snap *fleet.DisplaySnapshot) ([]fleet.Info, map[string]string) {
	infos, err := m.captains()
	if err != nil {
		return nil, nil
	}
	homes := map[string]bool{}
	for _, c := range infos {
		homes[c.Home] = true
	}
	if snap != nil {
		for _, ts := range snap.Tasks {
			if strings.HasPrefix(ts.Source, "captain:") {
				homes[ts.Home] = true
			}
		}
		for _, f := range snap.Failures {
			if strings.HasPrefix(f.Source, "captain:") {
				homes[f.Home] = true
			}
		}
	}
	ids := map[string]string{}
	for h := range homes {
		if id, err := captainIDIn(infos, h); err == nil {
			ids[h] = id
		}
	}
	return infos, ids
}

func (m dashboardModel) readCmd() tea.Cmd {
	return func() tea.Msg { return m.read() }
}

func (m dashboardModel) tickCmd() tea.Cmd {
	return tea.Tick(dashboardTick, func(time.Time) tea.Msg { return dashTickMsg{} })
}

// Init issues the first read; newDashboardModel starts with a read in flight.
func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(m.readCmd(), m.tickCmd(), tea.RequestBackgroundColor)
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
	m, cmd := m.update(msg)
	m.resize()
	return m, cmd
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		rewrap := msg.Width != m.width
		m.width, m.height = msg.Width, msg.Height
		if rewrap {
			if m.mode == modeConfirm {
				m.loadArgv()
			}
			m.argv.SetYOffset(0) // a new width rewraps the command: show it from its first row
		}
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case dashTickMsg:
		return m, tea.Batch(m.tickCmd(), m.startRead())
	case dashRead:
		m.reading = false
		m.applyRead(msg)
	case dashAsked:
		m.decided(msg)
		return m, nil
	case dashExecDone:
		m.mode, m.pending = modeBrowse, nil
		m.result = &msg
		return m, m.startRead()
	case tea.PasteMsg:
		if m.mode == modeForm {
			p := m.pending
			p.inputs[p.field], _ = p.inputs[p.field].Update(msg)
		}
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *dashboardModel) applyRead(r dashRead) {
	m.readDone = true
	m.beat, m.unacked, m.unackedErr = r.beat, r.unacked, r.unackedErr
	m.registry, m.captainIDs = r.registry, r.captainIDs
	if r.snapErr != nil {
		m.readErr = r.snapErr
	} else {
		m.readErr = nil
		m.lastGood = r.at
		m.rows = r.snap.Tasks
		m.failures = r.snap.Failures
		m.holds, m.holdsErr = r.holds, r.holdsErr
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
	if over := len(m.events) - dashboardFeedCap; over > 0 {
		m.events = m.events[over:]
	}
	if n := len(r.events); n > 0 {
		m.cursor = r.events[n-1].ID
	}
}

// items lists failed sources first, then the task rows grouped by home: the
// primary home ("general") first, then each captain in id order. Within a
// group Human-needed rows come first, otherwise snapshot order holds.
func (m dashboardModel) items() []list.Item {
	items := make([]list.Item, 0, len(m.failures)+len(m.rows))
	for i := range m.failures {
		items = append(items, dashItem{failure: &m.failures[i]})
	}
	rows := make([]dashItem, 0, len(m.rows))
	for i := range m.rows {
		rows = append(rows, dashItem{row: &m.rows[i]})
	}
	rank := func(it dashItem) (int, string) {
		if g := dashGroup(it); g != "general" {
			return 1, g
		}
		return 0, ""
	}
	slices.SortStableFunc(rows, func(a, b dashItem) int {
		ra, ga := rank(a)
		rb, gb := rank(b)
		if c := cmp.Compare(ra, rb); c != 0 {
			return c
		}
		if c := strings.Compare(ga, gb); c != 0 {
			return c
		}
		return cmp.Compare(dashHumanRank(a), dashHumanRank(b))
	})
	for _, it := range rows {
		items = append(items, it)
	}
	return items
}

func dashHumanRank(it dashItem) int {
	if fleet.HumanNeeded(*it.row) {
		return 0
	}
	return 1
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

// syncSel records the identity of the list's selected item.
func (m *dashboardModel) syncSel() {
	if it, ok := m.list.SelectedItem().(dashItem); ok {
		m.sel = m.targetOf(it)
	} else {
		m.sel = dashTarget{}
	}
}

// reselect keeps the selection on the same row after a refresh reorders them;
// a vanished row falls back to the nearest index.
func (m *dashboardModel) reselect() {
	items := m.items()
	prev := m.list.Index()
	m.list.SetItems(items)
	for i, it := range items {
		if m.targetOf(it.(dashItem)) == m.sel {
			m.list.Select(i)
			return
		}
	}
	m.list.Select(max(0, min(prev, len(items)-1)))
	m.syncSel()
}

func (m dashboardModel) onKey(k tea.KeyPressMsg) (dashboardModel, tea.Cmd) {
	name := k.String()
	if name == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeRunning:
		return m, nil
	case modeConfirm:
		return m.onConfirmKey(k)
	case modeForm:
		return m.onFormKey(k)
	}
	m.notice = ""
	if key.Matches(k, dashQuit) {
		return m, tea.Quit
	}
	if key.Matches(k, dashHelp) {
		m.help = !m.help
		return m, nil
	}
	if a := actionFor(k); a != nil {
		return m.begin(a), nil
	}
	if key.Matches(k, dashDecide) {
		return m.decide()
	}
	m.list, _ = m.list.Update(k)
	m.syncSel()
	return m, nil
}

// begin captures the selected row and starts the action's prompts, or goes
// straight to confirmation when it has none.
func (m dashboardModel) begin(a *dashAction) dashboardModel {
	hasTarget := len(m.list.Items()) > 0
	t := m.sel
	if a.binding == bindCaptain {
		if infos, err := m.captains(); err == nil {
			t = captainTarget(t, infos)
		}
	}
	home, problem := a.bind(t, hasTarget, m.home)
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
	m.pending = &dashPending{action: a, target: t, home: home, inputs: make([]textinput.Model, len(a.fields))}
	for i := range m.pending.inputs {
		// ponytail: textinput's ctrl+v returns a clipboard-read Cmd; onFormKey
		// drops every textinput Cmd, so only bracketed paste (tea.PasteMsg) fills a field.
		in := textinput.New()
		m.pending.inputs[i] = in
	}
	m.pending.focus(0)
	if len(a.fields) == 0 {
		m.confirm(nil)
	} else {
		m.mode = modeForm
	}
	return m
}

// focus moves the form's cursor to field i.
func (p *dashPending) focus(i int) {
	if p.field < len(p.inputs) {
		p.inputs[p.field].Blur()
	}
	if i < len(p.inputs) {
		p.inputs[i].Focus()
	}
	p.field = i
}

// captainIDOf resolves the registry ID of the captain whose canonical home is
// the given row home from the registry as it is now.
func (m dashboardModel) captainIDOf(rowHome string) (string, error) {
	infos, err := m.captains()
	if err != nil {
		return "", err
	}
	return captainIDIn(infos, rowHome)
}

// captainTarget is the target a captain action takes for a primary-home
// captain metadata row (ID "captain:<id>"): the home and source of the
// registered captain <id>. Any other target, or an unregistered captain, is
// returned as it is.
func captainTarget(t dashTarget, infos []fleet.Info) dashTarget {
	id, ok := strings.CutPrefix(t.ID, "captain:")
	if !ok {
		return t
	}
	for _, c := range infos {
		if c.ID == id {
			return dashTarget{ID: t.ID, Home: c.Home, Source: "captain:" + id}
		}
	}
	return t
}

// captainIDIn resolves, from infos, the registry ID of the captain whose
// canonical home is rowHome, comparing the way fleet.Register stores it.
func captainIDIn(infos []fleet.Info, rowHome string) (string, error) {
	canon, err := home.CanonicalCaptainHome(rowHome)
	if err != nil {
		return "", fmt.Errorf("home %s: %w", rowHome, err)
	}
	for _, c := range infos {
		if c.Home == canon {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no registered captain has home %s", canon)
}

// confirm builds the argv from the field values and loads it, as the exact
// command line, into the confirm viewport at its top.
func (m *dashboardModel) confirm(v map[string]string) {
	p := m.pending
	p.argv = p.action.buildArgv(p.home, p.target, v)
	m.loadArgv()
	m.argv.SetYOffset(0)
	m.mode = modeConfirm
}

// loadArgv puts the exact command line into the confirm viewport, wrapped at
// the terminal width here: the viewport's own soft wrap can cut a double-width
// rune into a row one cell too wide.
func (m *dashboardModel) loadArgv() {
	line := "Run: " + argvLine(append([]string{m.exe}, m.pending.argv...))
	m.argv.SetContentLines(strings.Split(ansi.Hardwrap(line, m.width, true), "\n"))
}

func (m dashboardModel) onFormKey(k tea.KeyPressMsg) (dashboardModel, tea.Cmd) {
	p := m.pending
	switch {
	case key.Matches(k, dashFormCancel):
		m.mode, m.pending = modeBrowse, nil
	case key.Matches(k, dashFormNext):
		if p.field < len(p.action.fields)-1 {
			p.focus(p.field + 1)
			return m, nil
		}
		for i, f := range p.action.fields {
			if f.required && p.inputs[i].Value() == "" {
				p.focus(i)
				m.notice = f.name + " is required"
				return m, nil
			}
		}
		m.notice = ""
		v := make(map[string]string, len(p.inputs))
		for i, f := range p.action.fields {
			v[f.name] = p.inputs[i].Value()
		}
		m.confirm(v)
	default:
		p.inputs[p.field], _ = p.inputs[p.field].Update(k)
	}
	return m, nil
}

func (m dashboardModel) onConfirmKey(k tea.KeyPressMsg) (dashboardModel, tea.Cmd) {
	switch {
	case key.Matches(k, dashRun):
		if !m.onScreen() {
			return m, nil
		}
		m.mode, m.result = modeRunning, nil
		return m, m.run(m.exe, m.pending.argv)
	case key.Matches(k, dashRunCancel):
		m.mode, m.pending = modeBrowse, nil
	default:
		if m.argvDrawn() {
			m.argv, _ = m.argv.Update(k)
		}
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
