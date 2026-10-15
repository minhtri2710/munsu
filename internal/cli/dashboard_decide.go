package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// dashHold is one open decision hold `decision-hold hold` recorded for a task.
type dashHold struct{ key, reason string }

// dashHoldKey is the task a hold belongs to: its home and ID.
type dashHoldKey struct{ home, id string }

// dashHolds reads, from each home with task rows, the open decision holds of
// those rows: the holds `decision-hold resolve` and `complete` release.
func dashHolds(primary string, snap *fleet.DisplaySnapshot) (map[dashHoldKey][]dashHold, error) {
	byHome := map[string][]string{}
	for _, ts := range snap.Tasks {
		h := cmp.Or(ts.Home, primary)
		byHome[h] = append(byHome[h], ts.ID)
	}
	out := map[dashHoldKey][]dashHold{}
	var errs []error
	for dir, ids := range byHome {
		h, err := home.Open(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("home %s: %w", dir, err))
			continue
		}
		auth, err := taskauthority.NewCanonical(h)
		if err != nil {
			errs = append(errs, fmt.Errorf("home %s: %w", dir, err))
			continue
		}
		holds, err := auth.ListHolds()
		if err != nil {
			errs = append(errs, fmt.Errorf("home %s: %w", dir, err))
			continue
		}
		sort.Slice(holds, func(i, j int) bool { return holds[i].ID < holds[j].ID })
		for _, id := range ids {
			for _, hold := range holds {
				key, ok := strings.CutPrefix(hold.ID, decisionHoldID(id, ""))
				if ok && hold.ReleasedAt == 0 && slices.Contains(hold.Scope.TaskIDs, id) {
					k := dashHoldKey{dir, id}
					out[k] = append(out[k], dashHold{key: key, reason: hold.Reason})
				}
			}
		}
	}
	return out, errors.Join(errs...)
}

// holdsOf is the open decision holds of a row.
func (m dashboardModel) holdsOf(ts fleet.TaskSnapshot) []dashHold {
	return m.holds[dashHoldKey{cmp.Or(ts.Home, m.home), ts.ID}]
}

// dashDecision holds the huh form's values while the form runs.
type dashDecision struct {
	target                                       dashTarget
	key, action, answer, grantor, channel, quote string
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

// decide asks which of the selected row's holds, which option, the answer
// and the words.
func (m dashboardModel) decide() (dashboardModel, tea.Cmd) {
	it, _ := m.list.SelectedItem().(dashItem)
	var holds []dashHold
	if it.row != nil {
		holds = m.holdsOf(*it.row)
	}
	if len(holds) == 0 {
		m.notice = "decide: select a held decision"
		return m, nil
	}
	d := &dashDecision{target: m.sel, key: holds[0].key}
	keys := make([]huh.Option[string], len(holds))
	for i, h := range holds {
		keys[i] = huh.NewOption(h.key+": "+h.reason, h.key)
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Decision held by "+it.row.ID).Options(keys...).Value(&d.key),
			huh.NewSelect[string]().Title("Option").Options(dashDecideOptions...).Value(&d.action),
			huh.NewInput().Title("answer (resolve only)").Value(&d.answer),
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
	m.pending = &dashPending{action: a, target: d.target, home: d.target.Home}
	m.confirm(map[string]string{"key": d.key, "keys": d.key, "answer": d.answer, "grantor": d.grantor, "channel": d.channel, "quote": d.quote})
}
