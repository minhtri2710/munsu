package cli

import (
	"strings"
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
// own).
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
	key     string
	name    string
	binding dashBinding
	// needsCaptainID resolves the registry ID from the registry before the
	// action starts; with no registered captain at the row's home it is not
	// bindable.
	needsCaptainID bool
	fields         []dashField
	// args builds the argv after "--home <home>" from the captured target and
	// the field values (keyed by field name).
	args func(t dashTarget, v map[string]string) []string
}

var wordsFields = []dashField{
	{"grantor", "grantor", true},
	{"channel", "channel", true},
	{"quote", "quote (verbatim)", true},
}

func taskArgs(path ...string) func(dashTarget, map[string]string) []string {
	return func(t dashTarget, _ map[string]string) []string {
		return append(append([]string{}, path...), t.ID)
	}
}

func wordsArgs(v map[string]string) []string {
	return []string{"--grantor", v["grantor"], "--channel", v["channel"], "--quote", v["quote"]}
}

// dashActions is the approved action set (plan sets A and B). The wake actions
// are absent: no existing read exposes a claimed wake's lease and event IDs.
// Offered nowhere: --force, --teardown, --generation.
var dashActions = []dashAction{
	{key: "b", name: "task block", binding: bindTask,
		fields: []dashField{{"by", "blocked by (empty for none)", false}},
		args: func(t dashTarget, v map[string]string) []string {
			a := []string{"task", "block", t.ID}
			if v["by"] != "" {
				a = append(a, "--by", v["by"])
			}
			return a
		}},
	{key: "u", name: "task unblock", binding: bindTask, args: taskArgs("task", "unblock")},
	{key: "d", name: "task done", binding: bindTask, args: taskArgs("task", "done")},
	{key: "t", name: "task retry", binding: bindTask, args: taskArgs("task", "retry")},
	{key: "o", name: "task reopen", binding: bindTask, args: taskArgs("task", "reopen")},
	{key: "x", name: "teardown", binding: bindTask, args: taskArgs("teardown")},
	{key: "p", name: "promote", binding: bindTask, args: taskArgs("promote")},
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
		fields: append([]dashField{{"keys", "keys (space-separated, optional)", false}, {"none", "none (any text passes --none)", false}}, wordsFields...),
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
}

func findDashAction(key string) *dashAction {
	for i := range dashActions {
		if dashActions[i].key == key {
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
