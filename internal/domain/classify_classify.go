// Package classify implements status classification logic for soldier status files.
// It is a pure-logic package (stdlib only) that replaces fm-classify-lib.sh in Go.
// Functions are side-effect-free reads of status files or pure string predicates.
package domain

import (
	"regexp"
	"slices"
	"strings"
)

// Default verb constants, matching fm-classify-lib.sh defaults.
const (
	PausedVerbDefault  = "paused"
	ResolveVerbDefault = "resolved"
)

// ValidStatusStates lists the recognized status verbs.
var ValidStatusStates = []string{
	"working", "review-ready", "amending", "needs-decision", "blocked", "paused",
	"awaiting_approval", "resolved", "done", "failed", "delivered",
}

// IsValidStatusState reports whether state is a recognized status verb.
func IsValidStatusState(state string) bool {
	return slices.Contains(ValidStatusStates, state)
}

// MaterialVerbs are the status verbs that warrant waking a parent supervisor.
var MaterialVerbs = []string{"done", "failed", "needs-decision", "blocked"}

// IsMaterialVerb reports whether verb warrants waking a parent supervisor.
func IsMaterialVerb(verb string) bool {
	return slices.Contains(MaterialVerbs, verb)
}

// humanNeededVerbs are the status verbs that put the Human's attention on a
// task (G350): a Human gate opens or a product fork (needs-decision), a
// blocker routed upward (blocked), and a failure such as red CI or a
// turn-killing runtime error (failed). Completion, readiness and merge lines
// are audit projections, not attention.
var humanNeededVerbs = []string{"needs-decision", "blocked", "failed"}

// humanNeededRe matches a human-needed verb that leads a status line or
// follows other text in it, such as a wake payload that prefixes the task ID.
// Compiled once at package init.
var humanNeededRe = regexp.MustCompile(`(?i)(?:^|\s)(?:needs-decision|blocked|failed):`)

// AbsorbResult indicates why an idle soldier might be safely absorbed instead of surfaced.
type AbsorbResult int

const (
	// None means the soldier cannot be safely absorbed and must surface.
	None AbsorbResult = iota
	// Working means the soldier is provably still working.
	Working
	// Paused means the soldier declared a deliberate external-wait pause.
	Paused
)

// Decision represents a keyed open general decision.
type Decision struct {
	Key     string
	Verb    string // "needs-decision" or "blocked"
	Summary string
}

// Activity represents a keyed open work phase (working/paused) in the status event log.
// It is fold evidence about whether a parent/child event was explicitly superseded;
// it is never authoritative current soldier state (prefer soldierstate / structured home).
type Activity struct {
	Key     string
	Verb    string // "working" or paused verb
	Summary string
}

// StatusMatch represents a status file whose last line is general-relevant.
type StatusMatch struct {
	Path     string
	TaskID   string
	LastLine string
}

// --- Internal helpers matching fm-classify-lib.sh functions ---

// LineVerb extracts the leading verb word from a status line.
// Strips optional [key=<slug>] marker before the colon.
func LineVerb(line string) string {
	before, _, _ := strings.Cut(line, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	return strings.TrimSpace(before)
}

// lineNote extracts text after the first colon, trimmed.
func lineNote(line string) string {
	_, note, found := strings.Cut(line, ":")
	if found {
		return strings.TrimSpace(note)
	}
	return strings.TrimSpace(line)
}

// decisionKey extracts the optional [key=<slug>] from a status line, or "default".
func decisionKey(line string) string {
	before, _, _ := strings.Cut(line, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		rest := before[idx+5:] // skip "[key="
		if end := strings.Index(rest, "]"); end >= 0 {
			key := rest[:end]
			if key != "" && isValidKey(key) {
				return key
			}
		}
	}
	return "default"
}

// isValidKey reports whether s is a valid decision key.
func isValidKey(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return len(s) > 0
}

// removeByKey removes the first decision with the given key from the slice.
func removeByKey(decisions []Decision, key string) []Decision {
	for i, d := range decisions {
		if d.Key == key {
			return append(decisions[:i], decisions[i+1:]...)
		}
	}
	return decisions
}

// --- Public API ---

// GeneralRelevant returns true if a status line needs the Human: it carries
// needs-decision:, blocked: or failed: (G350). done:, "PR ready", "checks
// green", "ready in branch" and "merged" lines are audit-only and do not match.
// Paused lines are NOT general-relevant.
// Verb-aware: nonterminal progress verbs (working, resolved) NEVER
// match from free-text prose alone. A "working:" line cannot escalate merely
// because its prose contains "failed:" or "blocked:".
func GeneralRelevant(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Paused lines are never general-relevant.
	if IsPaused(trimmed) {
		return false
	}

	// Verb-aware: nonterminal progress verbs are never general-relevant
	// from free-text prose.
	verb := LineVerb(trimmed)
	switch verb {
	case "working", "resolved":
		return false
	}

	if slices.Contains(humanNeededVerbs, verb) {
		return true
	}
	return humanNeededRe.MatchString(trimmed)
}

// IsPaused returns true if a status line's leading verb is the pause verb.
// Matches the munsu status_is_paused pattern.
func IsPaused(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return LineVerb(trimmed) == PausedVerbDefault
}

// OpenDecisions reads a status file and returns all still-open keyed decisions.
// Keys must be explicitly closed by "resolved:" lines referencing the same key. A bare "resolved:" closes the "default" key.
// Returns nil for missing/unreadable files or when no decisions are open.
// Matches the munsu status_open_decisions pattern.
func FoldOpenDecisions(lines []string) []Decision {
	var decisions []Decision

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		verb := LineVerb(line)
		note := lineNote(line)
		key := decisionKey(line)

		switch verb {
		case "needs-decision", "blocked":
			// Open or replace this key's decision.
			decisions = removeByKey(decisions, key)
			decisions = append(decisions, Decision{Key: key, Verb: verb, Summary: note})

		case ResolveVerbDefault:
			// Close this key's decision.
			decisions = removeByKey(decisions, key)
		}
	}

	return decisions
}

// OpenActivities folds a status file into still-open keyed work phases.
// working or paused opens/replaces a phase for its key; done, failed,
// needs-decision, blocked or resolved with the same key closes it.
// Bare legacy events use key "default". Matches the munsu status_open_activities pattern.
// Not authoritative current state — use soldierstate / home summary for that.
func FoldOpenActivities(lines []string) []Activity {
	var activities []Activity
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		verb := LineVerb(line)
		note := lineNote(line)
		key := decisionKey(line)

		switch verb {
		case "working", PausedVerbDefault:
			activities = removeActivityByKey(activities, key)
			activities = append(activities, Activity{Key: key, Verb: verb, Summary: note})
		case "done", "failed", "needs-decision", "blocked", ResolveVerbDefault:
			activities = removeActivityByKey(activities, key)
		}
	}
	return activities
}

// removeActivityByKey removes the first activity with the given key.
func removeActivityByKey(activities []Activity, key string) []Activity {
	for i, a := range activities {
		if a.Key == key {
			return append(activities[:i], activities[i+1:]...)
		}
	}
	return activities
}

func ClassifyAbsorb(lastLine string) AbsorbResult {
	switch LineVerb(strings.TrimSpace(lastLine)) {
	case PausedVerbDefault:
		return Paused
	case "working":
		return Working
	}
	return None
}
