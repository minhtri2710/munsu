// Package classify implements status classification logic for soldier status files.
// It is a pure-logic package (stdlib only) that replaces fm-classify-lib.sh in Go.
// Functions are side-effect-free reads of status files or pure string predicates.
package domain

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Default verb constants, matching fm-classify-lib.sh defaults.
const (
	PausedVerbDefault  = "paused"
	ResolveVerbDefault = "resolved"
)

// captainReDefault matches general-relevant patterns in a status line.
// Compiled once at package init.
var captainReDefault = regexp.MustCompile(`(?i)(?:^|\s)(?:done|needs-decision|blocked|failed):|PR ready|checks green|ready in branch|merged`)

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

// lineVerb extracts the leading verb word from a status line.
// Strips optional [key=<slug>] marker before the colon.
func lineVerb(line string) string {
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

// readLastLine reads the last non-empty line from a file.
// Returns empty string if the file cannot be read or has no content.
func readLastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) != "" {
			lastLine = text
		}
	}
	return lastLine
}

// --- Public API ---

// GeneralRelevant returns true if a status line contains a general-relevant verb
// (done:, failed:, needs-decision:, blocked:, "PR ready", "checks green",
// "ready in branch", "merged"). Paused lines are NOT general-relevant.
// Verb-aware: nonterminal progress verbs (working, resolved, captain-held) NEVER
// match from free-text prose alone. A "working:" line cannot escalate merely
// because its prose contains "PR ready", "checks green", "merged", etc. Only
// the authoritative terminal verbs and bare legacy lines (no leading verb)
// match free-text tokens.
// Matches the munsu status_is_captain_relevant pattern with Firstmate ea3ac2e
// verb-awareness parity.
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
	// from free-text prose. Only the authoritative terminal verbs and
	// bare non-verb legacy lines should match free-text tokens.
	verb := lineVerb(trimmed)
	switch verb {
	case "working", "resolved", "captain-held":
		return false
	}

	// Check exact verb match for core general-relevant verbs.
	switch verb {
	case "done", "needs-decision", "blocked", "failed":
		return true
	}
	// Check the regex pattern for composite patterns.
	return captainReDefault.MatchString(trimmed)
}

// IsPaused returns true if a status line's leading verb is the pause verb.
// Matches the munsu status_is_paused pattern.
func IsPaused(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return lineVerb(trimmed) == PausedVerbDefault
}

// OpenDecisions reads a status file and returns all still-open keyed decisions.
// Keys must be explicitly closed by "resolved:" or "captain-held:" lines
// referencing the same key. A bare "resolved:" closes the "default" key.
// Returns nil for missing/unreadable files or when no decisions are open.
// Matches the munsu status_open_decisions pattern.
func OpenDecisions(path string) []Decision {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var decisions []Decision

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		verb := lineVerb(line)
		note := lineNote(line)
		key := decisionKey(line)

		switch verb {
		case "needs-decision", "blocked":
			// Open or replace this key's decision.
			decisions = removeByKey(decisions, key)
			decisions = append(decisions, Decision{Key: key, Verb: verb, Summary: note})

		case ResolveVerbDefault, "captain-held":
			// Close this key's decision.
			decisions = removeByKey(decisions, key)
		}
	}

	return decisions
}

// OpenActivities folds a status file into still-open keyed work phases.
// working or paused opens/replaces a phase for its key; done, failed,
// needs-decision, blocked, resolved, or captain-held with the same key closes it.
// Bare legacy events use key "default". Matches the munsu status_open_activities pattern.
// Not authoritative current state — use soldierstate / home summary for that.
func OpenActivities(path string) []Activity {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var activities []Activity
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		verb := lineVerb(line)
		note := lineNote(line)
		key := decisionKey(line)

		switch verb {
		case "working", PausedVerbDefault:
			activities = removeActivityByKey(activities, key)
			activities = append(activities, Activity{Key: key, Verb: verb, Summary: note})
		case "done", "failed", "needs-decision", "blocked", ResolveVerbDefault, "captain-held":
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

// AbsorbClass classifies why an idle task might be safely absorbed.
// Reads the status file from stateDir/<id>.status and classifies based on
// the last non-empty status line:
//   - Working if the last line verb is "working"
//   - Paused if the last line verb is the pause verb
//   - None otherwise
//
// This is a pure-logic subset of munsu's absorb_class logic, which also
// consults no-mistakes run-step and pane liveness. The watcher integrates
// classify alongside soldierstate.Read for the full picture.
func AbsorbClass(id string, stateDir string) AbsorbResult {
	statusPath := filepath.Join(stateDir, id+".status")
	lastLine := readLastLine(statusPath)
	if lastLine == "" {
		return None
	}

	verb := lineVerb(lastLine)
	switch verb {
	case PausedVerbDefault:
		return Paused
	case "working":
		return Working
	}
	return None
}

// ScanGeneralRelevant scans stateDir/*.status for general-relevant last lines.
// Returns a StatusMatch for each file whose last line is general-relevant.
// Matches the munsu scan_captain_relevant_statuses pattern.
func ScanGeneralRelevant(stateDir string) []StatusMatch {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}

	var matches []StatusMatch
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".status") {
			continue
		}

		statusPath := filepath.Join(stateDir, name)
		lastLine := readLastLine(statusPath)
		if lastLine == "" || !GeneralRelevant(lastLine) {
			continue
		}

		taskID := strings.TrimSuffix(name, ".status")
		matches = append(matches, StatusMatch{
			Path:     statusPath,
			TaskID:   taskID,
			LastLine: lastLine,
		})
	}

	return matches
}
