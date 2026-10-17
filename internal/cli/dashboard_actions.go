package cli

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// dashTarget is the row identity captured when an action starts. Rows reorder
// on refresh, so every action binds to this, never to a row index. ID is empty
// for a failed-source row.
type dashTarget struct {
	ID     string
	Home   string
	Source string
	// CaptainID is the registry ID of the captain owning Home, resolved when a
	// captain action starts; it is not part of the row identity.
	CaptainID string
}

// dashField is one prompted value. A field marked required blocks submission
// while empty; the dashboard checks nothing else (each command refuses on its
// own). The words fields are required only where the plan says the dashboard
// passes the Human's words through: decision-hold resolve and delivery
// pr-merge.
type dashField struct {
	name     string
	label    string
	required bool
}

type dashBinding int

const (
	bindTask    dashBinding = iota // a task row: its ID and home
	bindCaptain                    // a row or failed source of a captain home
	bindPrimary                    // no row: the primary home
	bindAny                        // any row or failed source: its home
)

type dashAction struct {
	key string
	// keys is the action's declared binding, built once from key and name by
	// withBindings. onKey matches it and the footer renders it; the footer
	// copies it disabled for an action the selection cannot carry.
	keys    key.Binding
	name    string
	binding dashBinding
	// needsCaptainID resolves the registry ID from the registry before the
	// action starts; with no registered captain at the row's home it is not
	// bindable.
	needsCaptainID bool
	fields         []dashField
	// applies, when set, is whether the selected task row's state admits the
	// action, by the predicate its Task Authority operation refuses by. The
	// footer offers only actions that apply; begin does not consult it, so
	// the command's own refusal stays the only one.
	applies func(fleet.TaskSnapshot) bool
	// args builds the argv after "--home <home>" from the captured target and
	// the field values (keyed by field name).
	args func(t dashTarget, v map[string]string) []string
}

var wordsFields = []dashField{
	{"grantor", "grantor", true},
	{"channel", "channel", true},
	{"quote", "quote (verbatim)", true},
}

// optionalWordsFields are the words a command may take but does not always
// need; the command refuses on its own when it needs them.
var optionalWordsFields = []dashField{
	{"grantor", "grantor (optional)", false},
	{"channel", "channel (optional)", false},
	{"quote", "quote (optional, verbatim)", false},
}

// inPhase applies an action to a row whose canonical phase admits it.
func inPhase(admits func(taskauthority.Phase) bool) func(fleet.TaskSnapshot) bool {
	return func(ts fleet.TaskSnapshot) bool { return admits(taskauthority.Phase(ts.CurrentState)) }
}

func taskArgs(path ...string) func(dashTarget, map[string]string) []string {
	return func(t dashTarget, _ map[string]string) []string {
		return append(append([]string{}, path...), t.ID)
	}
}

// wordsArgs passes each words value that was given.
func wordsArgs(v map[string]string) []string {
	var a []string
	for _, name := range []string{"grantor", "channel", "quote"} {
		if v[name] != "" {
			a = append(a, "--"+name, v[name])
		}
	}
	return a
}

// dashActions is the approved action set (plan sets A and B). The wake actions
// are absent: no existing read exposes a claimed wake's lease and event IDs.
// Offered nowhere: --force, --generation.
var dashActions = withBindings([]dashAction{
	{key: "b", name: "task block", binding: bindTask, applies: inPhase(taskauthority.Phase.Blockable),
		fields: []dashField{{"by", "blocked by (empty for none)", false}},
		args: func(t dashTarget, v map[string]string) []string {
			a := []string{"task", "block", t.ID}
			if v["by"] != "" {
				a = append(a, "--by", v["by"])
			}
			return a
		}},
	{key: "u", name: "task unblock", binding: bindTask, applies: inPhase(taskauthority.Phase.Unblockable), args: taskArgs("task", "unblock")},
	{key: "d", name: "task done", binding: bindTask, applies: inPhase(func(p taskauthority.Phase) bool { return !p.Terminal() }), args: taskArgs("task", "done")},
	{key: "t", name: "task retry", binding: bindTask, applies: inPhase(taskauthority.Phase.Terminal), args: taskArgs("task", "retry")},
	{key: "o", name: "task reopen", binding: bindTask, applies: inPhase(taskauthority.Phase.Terminal), args: taskArgs("task", "reopen")},
	{key: "x", name: "teardown", binding: bindTask, args: taskArgs("teardown")},
	{key: "p", name: "promote", binding: bindTask, applies: func(ts fleet.TaskSnapshot) bool {
		return taskauthority.Promotable(ts.Kind, taskauthority.Phase(ts.CurrentState))
	}, args: taskArgs("promote")},
	{key: "s", name: "send", binding: bindTask,
		fields: []dashField{{"line", "line", false}},
		args: func(t dashTarget, v map[string]string) []string {
			return []string{"send", t.ID, v["line"]}
		}},

	{key: "h", name: "decision-hold hold", binding: bindTask,
		fields: []dashField{{"key", "decision key", false}, {"reason", "reason", false}},
		args: func(t dashTarget, v map[string]string) []string {
			return []string{"decision-hold", "hold", v["key"], "--reason", v["reason"], "--from", t.ID}
		}},
	{key: "e", name: "decision-hold resolve", binding: bindTask,
		fields: append([]dashField{{"key", "decision key", false}, {"answer", "answer", false}, {"unblock", "unblock (space-separated, optional)", false}}, wordsFields...),
		args: func(t dashTarget, v map[string]string) []string {
			a := []string{"decision-hold", "resolve", v["key"], "--answer", v["answer"], "--from", t.ID}
			a = append(a, wordsArgs(v)...)
			for _, dep := range strings.Fields(v["unblock"]) {
				a = append(a, "--unblock", dep)
			}
			return a
		}},
	{key: "c", name: "decision-hold complete", binding: bindTask,
		fields: append([]dashField{{"keys", "keys (space-separated, optional)", false}, {"none", "none (any text passes --none)", false}}, optionalWordsFields...),
		args: func(t dashTarget, v map[string]string) []string {
			a := append([]string{"decision-hold", "complete", t.ID}, strings.Fields(v["keys"])...)
			a = append(a, wordsArgs(v)...)
			if v["none"] != "" {
				a = append(a, "--none")
			}
			return a
		}},

	{key: "R", name: "captain retire", binding: bindCaptain,
		args: func(t dashTarget, _ map[string]string) []string { return []string{"captain", "retire", t.Home} }},
	{key: "V", name: "captain recover", binding: bindCaptain, needsCaptainID: true,
		args: func(t dashTarget, _ map[string]string) []string {
			return []string{"captain", "recover", t.CaptainID}
		}},
	{key: "C", name: "captain converge", binding: bindPrimary,
		args: func(dashTarget, map[string]string) []string { return []string{"captain", "converge"} }},

	{key: "v", name: "delivery record-verdict", binding: bindAny,
		fields: []dashField{{"reviewer", "reviewer task", false}},
		args: func(_ dashTarget, v map[string]string) []string {
			return []string{"delivery", "record-verdict", "--reviewer-task", v["reviewer"]}
		}},
	{key: "m", name: "delivery pr-merge", binding: bindTask,
		fields: append([]dashField{{"pr", "PR URL", false}}, wordsFields...),
		args: func(t dashTarget, v map[string]string) []string {
			return append([]string{"delivery", "pr-merge", t.ID, v["pr"]}, wordsArgs(v)...)
		}},
})

// The dashboard's other bindings. The list's own key map does the moving;
// dashMove is only its footer entry.
var (
	dashQuit       = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	dashMove       = key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("j/k", "move"))
	dashDecide     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "decide"))
	dashHelp       = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
	dashFormNext   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next/submit"))
	dashFormCancel = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	dashRun        = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "run"))
	dashRunCancel  = key.NewBinding(key.WithKeys("esc", "n"), key.WithHelp("esc", "cancel"))
)

// dashDecideOptions are a held decision's options: each value names the
// action that carries the choice.
var dashDecideOptions = []huh.Option[string]{
	huh.NewOption("resolve: record an answer", "decision-hold resolve"),
	huh.NewOption("complete: note it without an answer", "decision-hold complete"),
}

// actionNamed is the action of the table with the given name.
func actionNamed(name string) *dashAction {
	for i := range dashActions {
		if dashActions[i].name == name {
			return &dashActions[i]
		}
	}
	return nil
}

// withBindings declares each action's key binding; its help text is the last
// word of the action name.
func withBindings(as []dashAction) []dashAction {
	for i := range as {
		w := strings.Fields(as[i].name)
		as[i].keys = key.NewBinding(key.WithKeys(as[i].key), key.WithHelp(as[i].key, w[len(w)-1]))
	}
	return as
}

// actionFor is the action whose declared binding matches the key press.
func actionFor(k tea.KeyPressMsg) *dashAction {
	for i := range dashActions {
		if key.Matches(k, dashActions[i].keys) {
			return &dashActions[i]
		}
	}
	return nil
}

// bind resolves the home an action runs against and whether the captured
// target can carry the action at all. Captain registry commands run against
// the primary home; every other action runs against the row's own home, so a
// captain-home row never lands on the primary.
func (a *dashAction) bind(t dashTarget, hasTarget bool, primary string) (home string, problem string) {
	switch a.binding {
	case bindTask:
		if !hasTarget || t.ID == "" {
			return "", "select a task row"
		}
		return t.Home, ""
	case bindCaptain:
		if !hasTarget || !strings.HasPrefix(t.Source, "captain:") {
			return "", "select a row from a captain home"
		}
		return primary, ""
	case bindAny:
		if hasTarget {
			return t.Home, ""
		}
		return primary, ""
	default:
		return primary, ""
	}
}

// buildArgv is the exact argument vector, after the executable, of the
// command the action runs.
func (a *dashAction) buildArgv(home string, t dashTarget, v map[string]string) []string {
	return append([]string{"--home", home}, a.args(t, v)...)
}
