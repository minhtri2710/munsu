package fleet

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// HomeSummary is a bounded structured view of one Captain home.
// Schema aligns with the munsu captain-home-summary pattern:
// active_children, holds, decisions_open, queued, landed, counts, omitted, valid/reason.
// Registered home state is authoritative; parent status is separate evidence.
type HomeSummary struct {
	Schema         string
	Generated      string
	Home           string
	Valid          bool
	Reason         string
	State          string // no_active_work | active_child_work | captain_decision | externally_held | unknown
	ActiveChildren []ChildBrief
	DecisionsOpen  []DecisionBrief
	Holds          []HoldBrief
	Queued         []QueuedBrief
	Landed         []LandedBrief
	Endpoints      []EndpointBrief
	Counts         HomeCounts
	Omitted        []OmittedSurface
}

// ChildBrief is one active endpoint under a Captain home.
type ChildBrief struct {
	ID     string
	Status string
	Kind   string
	Doing  string
}

// DecisionBrief is one open decision under a Captain home.
type DecisionBrief struct {
	ID      string
	Key     string
	Verb    string
	Summary string
	Reason  string
	Source  string // status
}

// HoldBrief is one external hold (blocked Task Authority record or
// parked/paused/blocked child).
type HoldBrief struct {
	ID        string
	Title     string
	BlockedBy string
	Reason    string
	Source    string // task-authority | child-state
}

// QueuedBrief is one queued Task Authority record.
type QueuedBrief struct {
	ID    string
	Title string
	Repo  string
	Kind  string
}

// LandedBrief is one recently completed Task Authority record.
type LandedBrief struct {
	ID    string
	Title string
	PRURL string
}

// queuedView is the Task Authority view of one queued task.
type queuedView struct {
	ID, Title, Repo, Kind string
}

// landedView is the Task Authority view of one landed task.
type landedView struct {
	ID, Title string
}

// EndpointBrief is one task meta endpoint under the home.
type EndpointBrief struct {
	ID     string
	State  string
	Kind   string
	Source string
}

// HomeCounts aggregates Captain-home workload surfaces (full totals; lists may be capped).
type HomeCounts struct {
	ActiveChildren int
	DecisionsOpen  int
	Holds          int
	Queued         int
	Landed         int
	Endpoints      int
	// Legacy workload counters retained for callers that still read them.
	InFlight int
	Blocked  int
	Done     int
}

// OmittedSurface records a truncated list surface.
type OmittedSurface struct {
	Surface string
	Count   int
}

const (
	maxActiveChildren = 20
	maxDecisionsOpen  = 20
	maxQueued         = 20
	maxHolds          = 20
	maxLanded         = 10
	maxEndpoints      = 20
)

var prURLRe = regexp.MustCompile(`https?://[^\s]+`)

// SummarizeCaptainHome builds a bounded summary for a registered Captain home.
func SummarizeCaptainHome(homeDir string) HomeSummary {
	now := time.Now().UTC().Format(time.RFC3339)
	sum := HomeSummary{
		Schema:    "munsu-captain-home-summary.v1",
		Generated: now,
		Home:      homeDir,
		Valid:     true,
		State:     "no_active_work",
	}
	if homeDir == "" {
		sum.Valid = false
		sum.Reason = "no recorded captain home"
		sum.State = "unknown"
		return sum
	}

	// Canonical Task Authority records are the state source (Task 7.8): the
	// authoritative phase and kind win over the .status projection for the
	// child's current state, and a legacy v1 home fails closed in the summary
	// validity instead of silently projecting.
	canonical, canonicalErr := canonicalAggregates(homeDir)
	if canonicalErr != nil {
		sum.Valid = false
		sum.Reason = "task-authority state requires migration or repair: " + canonicalErr.Error()
		sum.State = "unknown"
		return sum
	}

	inFlightByID := map[string]bool{}
	var queuedAll []queuedView
	var landedAll []landedView
	for id, agg := range canonical {
		switch agg.Phase {
		case taskauthority.PhaseQueued:
			sum.Counts.Queued++
			queuedAll = append(queuedAll, queuedView{
				ID:    id,
				Title: agg.Definition.Description,
				Repo:  agg.Definition.Project,
				Kind:  agg.Definition.Kind,
			})
		case taskauthority.PhaseWorking:
			sum.Counts.InFlight++
			inFlightByID[id] = true
		case taskauthority.PhaseBlocked:
			sum.Counts.Blocked++
		case taskauthority.PhaseDone:
			sum.Counts.Done++
			if agg.Definition.Kind != "captain" {
				landedAll = append(landedAll, landedView{ID: id, Title: agg.Definition.Description})
			}
		}
	}
	sum.Counts.Landed = len(landedAll)

	entries, err := home.ListMeta(homeDir)
	if err != nil {
		entries = nil
	}
	sum.Counts.Endpoints = len(entries)
	metaByID := map[string]home.MetaEntry{}
	for _, e := range entries {
		metaByID[e.ID] = e
	}

	type childState struct {
		id, verb, statusVerb, status, kind, detail string
	}
	var children []childState
	var unknownChildren []string
	for _, e := range entries {
		status := strings.TrimSpace(e.LastStatus)
		statusVerb, detail := splitStatus(status)
		verb := statusVerb
		kind := e.Kind
		if agg, ok := canonical[e.ID]; ok {
			// The authoritative phase is state truth; the status line is
			// display detail and can never override a newer authoritative
			// lifecycle transition (criterion 3).
			verb = string(agg.Phase)
			kind = agg.Definition.Kind
			if detail == "" {
				detail = agg.PhaseDetail
			}
			if status == "" {
				status = string(agg.Phase)
			}
		}
		if status == "" {
			unknownChildren = append(unknownChildren, e.ID)
			verb = "unknown"
		}
		children = append(children, childState{
			id: e.ID, verb: verb, statusVerb: statusVerb, status: status, kind: kind, detail: detail,
		})
	}

	var activeAll []ChildBrief
	for _, c := range children {
		switch c.verb {
		case "working", "parked", "paused", "blocked", "needs-decision":
			activeAll = append(activeAll, ChildBrief{
				ID:     trunc(c.id, 120),
				Status: trunc(c.status, 160),
				Kind:   trunc(c.kind, 40),
				Doing:  trunc(c.detail, 120),
			})
		}
	}
	sum.Counts.ActiveChildren = len(activeAll)
	sum.ActiveChildren = capSlice(activeAll, maxActiveChildren)

	var decisionsAll []DecisionBrief
	seenDecision := map[string]bool{}
	for _, c := range children {
		path, err := home.StatusFilePath(homeDir, c.id)
		if err != nil {
			continue
		}
		for _, d := range home.OpenDecisions(path) {
			key := c.id + "\x00" + d.Key + "\x00" + d.Verb
			if seenDecision[key] {
				continue
			}
			seenDecision[key] = true
			decisionsAll = append(decisionsAll, DecisionBrief{
				ID:      trunc(c.id, 120),
				Key:     trunc(d.Key, 120),
				Verb:    trunc(d.Verb, 40),
				Summary: trunc(d.Summary, 160),
				Source:  "status",
			})
		}
		if c.statusVerb == "needs-decision" {
			key := c.id + "\x00default\x00needs-decision"
			if !seenDecision[key] {
				seenDecision[key] = true
				decisionsAll = append(decisionsAll, DecisionBrief{
					ID:      trunc(c.id, 120),
					Key:     "default",
					Verb:    "needs-decision",
					Summary: trunc(c.detail, 160),
					Source:  "status",
				})
			}
		}
	}
	sum.Counts.DecisionsOpen = len(decisionsAll)
	sum.DecisionsOpen = capSlice(decisionsAll, maxDecisionsOpen)

	var holdsAll []HoldBrief
	for id, agg := range canonical {
		if agg.Phase != taskauthority.PhaseBlocked {
			continue
		}
		holdsAll = append(holdsAll, HoldBrief{
			ID:     trunc(id, 120),
			Title:  trunc(agg.Definition.Description, 90),
			Reason: "blocked",
			Source: "task-authority",
		})
	}
	for _, c := range children {
		switch c.verb {
		case "parked", "paused", "blocked":
			title := c.id
			if inFlightByID[c.id] {
				if agg, ok := canonical[c.id]; ok {
					title = agg.Definition.Description
				}
			}
			holdsAll = append(holdsAll, HoldBrief{
				ID:     trunc(c.id, 120),
				Title:  trunc(title, 90),
				Reason: trunc(firstNonEmpty(c.detail, c.verb), 120),
				Source: "child-state",
			})
		}
	}
	sum.Counts.Holds = len(holdsAll)
	sum.Holds = capSlice(holdsAll, maxHolds)

	for i, item := range queuedAll {
		if i >= maxQueued {
			break
		}
		sum.Queued = append(sum.Queued, QueuedBrief{
			ID:    trunc(item.ID, 120),
			Title: trunc(item.Title, 120),
			Repo:  trunc(item.Repo, 120),
			Kind:  trunc(item.Kind, 40),
		})
	}

	// Reverse landed so later file order surfaces first (approx recent).
	for i, j := 0, len(landedAll)-1; i < j; i, j = i+1, j-1 {
		landedAll[i], landedAll[j] = landedAll[j], landedAll[i]
	}
	for i, item := range landedAll {
		if i >= maxLanded {
			break
		}
		pr := ""
		if lines, err := home.ReadStatus(homeDir, item.ID); err == nil {
			for k := len(lines) - 1; k >= 0; k-- {
				if u := extractPRURL(lines[k]); u != "" {
					pr = u
					break
				}
			}
		}
		sum.Landed = append(sum.Landed, LandedBrief{
			ID:    trunc(item.ID, 120),
			Title: trunc(item.Title, 120),
			PRURL: trunc(pr, 500),
		})
	}

	for i, c := range children {
		if i >= maxEndpoints {
			break
		}
		sum.Endpoints = append(sum.Endpoints, EndpointBrief{
			ID:     trunc(c.id, 120),
			State:  trunc(firstNonEmpty(c.verb, "unknown"), 40),
			Kind:   trunc(c.kind, 40),
			Source: "status",
		})
	}

	if len(activeAll) > maxActiveChildren {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "active_children", Count: len(activeAll) - maxActiveChildren})
	}
	if len(decisionsAll) > maxDecisionsOpen {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "decisions_open", Count: len(decisionsAll) - maxDecisionsOpen})
	}
	if len(queuedAll) > maxQueued {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "queued", Count: len(queuedAll) - maxQueued})
	}
	if len(holdsAll) > maxHolds {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "holds", Count: len(holdsAll) - maxHolds})
	}
	if len(entries) > maxEndpoints {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "endpoints", Count: len(entries) - maxEndpoints})
	}
	if len(landedAll) > maxLanded {
		sum.Omitted = append(sum.Omitted, OmittedSurface{Surface: "landed", Count: len(landedAll) - maxLanded})
	}

	var orphanInFlight []string
	for id := range inFlightByID {
		if _, ok := metaByID[id]; !ok {
			orphanInFlight = append(orphanInFlight, id)
		}
	}
	var unownedCurrent []string
	for _, c := range children {
		switch c.verb {
		case "working", "parked", "paused", "blocked":
			if !inFlightByID[c.id] {
				unownedCurrent = append(unownedCurrent, c.id+"="+c.verb)
			}
		}
	}

	switch {
	case len(unknownChildren) > 0:
		sum.Valid = false
		sum.Reason = "child current state unavailable"
	case len(orphanInFlight) > 0:
		sum.Valid = false
		sum.Reason = "in-flight task has no child metadata"
	case len(unownedCurrent) > 0 && sum.Counts.InFlight > 0:
		sum.Valid = false
		sum.Reason = "live child state has no in-flight task: " + strings.Join(unownedCurrent, ", ")
	}

	captainDecision := false
	for _, d := range decisionsAll {
		if d.Verb == "needs-decision" || d.Verb == "captain-hold" {
			captainDecision = true
			break
		}
	}
	switch {
	case !sum.Valid:
		sum.State = "unknown"
	case captainDecision:
		sum.State = "captain_decision"
	case sum.Counts.ActiveChildren > 0:
		sum.State = "active_child_work"
	case sum.Counts.Holds > 0:
		sum.State = "externally_held"
	default:
		sum.State = "no_active_work"
	}
	return sum
}

// LastParentStatus returns the last line of the parent status projection
// for logical captain:<id>; home path encoding is owned by internal/home.
func LastParentStatus(parentHome, captainID string) string {
	lines, err := home.ReadStatus(parentHome, "captain:"+captainID)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func splitStatus(status string) (verb, detail string) {
	status = strings.TrimSpace(status)
	if status == "" {
		return "", ""
	}
	before, after, found := strings.Cut(status, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	verb = strings.TrimSpace(before)
	if found {
		return verb, strings.TrimSpace(after)
	}
	return verb, ""
}

func trunc(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func extractPRURL(line string) string {
	return prURLRe.FindString(line)
}

func capSlice[T any](all []T, n int) []T {
	if len(all) <= n {
		return all
	}
	return all[:n]
}
