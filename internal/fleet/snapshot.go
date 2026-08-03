package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// FleetSnapshot represents the full fleet state.
type FleetSnapshot struct {
	Schema string         `json:"schema"`
	Time   string         `json:"time"`
	Tasks  []TaskSnapshot `json:"tasks"`
}

// TaskSnapshot represents one task's state.
type TaskSnapshot struct {
	ID               string `json:"id"`
	Project          string `json:"project"`
	Harness          string `json:"harness"`
	Model            string `json:"model"`
	Kind             string `json:"kind"`
	Mode             string `json:"mode"`
	Yolo             string `json:"yolo"`
	Window           string `json:"window"`
	Worktree         string `json:"worktree"`
	PaneAlive        bool   `json:"pane_alive"`
	PaneAliveUnknown bool   `json:"pane_alive_unknown,omitempty"`
	LastStatus       string `json:"last_status,omitempty"`

	// Home is the munsu home that owns this task meta (primary or captain).
	Home string `json:"home,omitempty"`
	// Source labels where the task was discovered: "primary" or "captain:<id>".
	Source string `json:"source,omitempty"`

	// Resolved current-state projection (populated when a resolver is wired).
	CurrentState        string            `json:"current_state,omitempty"`
	CurrentDescription  string            `json:"current_description,omitempty"`
	NoMistakesRunStep   string            `json:"no_mistakes_run_step,omitempty"`
	StatusLogSuperseded bool              `json:"status_log_superseded"`
	OpenActivities      []domain.Activity `json:"open_activities,omitempty"`
}

// CurrentStateInfo carries the resolved current-state projection for a task.
type CurrentStateInfo struct {
	State               string            `json:"state"`
	Description         string            `json:"description"`
	NoMistakesRunStep   string            `json:"no_mistakes_run_step,omitempty"`
	StatusLogSuperseded bool              `json:"status_log_superseded"`
	OpenActivities      []domain.Activity `json:"open_activities,omitempty"`
}

// resolveCurrentState is a function pointer wired from CLI to use soldierstate.Read().
// When nil, Snapshot falls back to simple meta+status logic.
var resolveCurrentState func(homeDir, id string) (*CurrentStateInfo, error)

// SetCurrentStateResolver installs the current-state resolver used by Snapshot.
func SetCurrentStateResolver(fn func(homeDir, id string) (*CurrentStateInfo, error)) {
	resolveCurrentState = fn
}

// CurrentState computes the resolved current-state projection for a task.
// When a resolver is wired (via SetCurrentStateResolver), it takes precedence.
// Fallback: meta window presence + last status line (display-only, not state truth).
func CurrentState(homeDir, id string, meta map[string]string) *CurrentStateInfo {
	if resolveCurrentState != nil {
		info, err := resolveCurrentState(homeDir, id)
		if err == nil && info != nil {
			return info
		}
	}

	// Fallback (no resolver wired): derive display phase from meta,
	// then let a terminal status verb override. This is display-only.
	paneAlive := meta["window"] != ""
	info := &CurrentStateInfo{
		State: PhaseFromMeta(meta["window"], paneAlive),
	}

	statusPath := filepath.Join(mhome.StateDir(homeDir), id+".status")
	info.OpenActivities = home.OpenActivities(statusPath)

	if data, err := os.ReadFile(statusPath); err == nil {
		lines := strings.TrimSpace(string(data))
		if lines != "" {
			parts := strings.Split(lines, "\n")
			lastLine := strings.TrimSpace(parts[len(parts)-1])
			if lastLine != "" {
				verb := statusVerb(lastLine)
				_, note, _ := strings.Cut(lastLine, ":")
				note = strings.TrimSpace(note)
				switch verb {
				case "working", "done", "failed", "blocked", "paused", "needs-decision", "awaiting_approval":
					info.State = verb
					info.Description = note
				}
			}
		}
	}

	return info
}

// PhaseFromMeta returns the display phase for a task from meta-only facts.
// window empty → registered; window non-empty → alive if paneAlive else dead.
func PhaseFromMeta(window string, paneAlive bool) string {
	switch {
	case window == "":
		return "registered"
	case paneAlive:
		return "alive"
	default:
		return "dead"
	}
}

// PhaseFromProjection returns the display phase using resolved current-state info.
// When CurrentState is set and non-empty it takes precedence over the meta-only phase.
func PhaseFromProjection(ts TaskSnapshot) string {
	if ts.CurrentState != "" {
		return ts.CurrentState
	}
	if ts.PaneAliveUnknown {
		return "unknown"
	}
	return PhaseFromMeta(ts.Window, ts.PaneAlive)
}

// Snapshot builds a fleet snapshot by scanning state/*.meta in the primary
// home and each registered captain home. Captain-owned soldiers remain visible
// to the general after handoff (meta never lives on the parent home).
func Snapshot(homeDir string) (*FleetSnapshot, error) {
	snap := &FleetSnapshot{
		Schema: "munsu-fleet-snapshot.v1",
		Time:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := appendHomeTasks(snap, homeDir, "primary", ""); err != nil {
		return nil, err
	}

	// Captain homes live under <home>/captains/<id>/ (handoff spawn target).
	// Scan the directory tree so general fleet view sees child soldiers without
	// importing the captain package (avoids import cycles).
	capRoot := filepath.Join(homeDir, "captains")
	if entries, err := os.ReadDir(capRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "" || e.Name()[0] == '.' {
				continue
			}
			ch := filepath.Join(capRoot, e.Name())
			src := "captain:" + e.Name()
			if err := appendHomeTasks(snap, ch, src, ch); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("scanning captain home %s: %w", ch, err)
			}
		}
	}

	return snap, nil
}

func appendHomeTasks(snap *FleetSnapshot, taskHome, source, homeLabel string) error {
	// Canonical Task Authority records are the preferred source (Task 7.8):
	// authoritative kind/project/phase win, and the .meta/.status projections
	// are display fallback only — a stale .status can never override a newer
	// authoritative lifecycle transition. A legacy v1 home fails closed
	// (migration is explicit, never automatic).
	canonical, err := canonicalAggregates(taskHome)
	if err != nil {
		return fmt.Errorf("reading canonical task authority state for %s: %w", taskHome, err)
	}

	metasDir := filepath.Join(taskHome, "state")
	entries, err := os.ReadDir(metasDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	seenIDs := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := mhome.ReadMeta(taskHome, id)
		if err != nil {
			continue
		}
		agg, hasCanonical := canonical[id]
		if !hasCanonical {
			// Legacy fail-closed posture (Task 7.8): a meta-only task that
			// claims delivery outcomes without an authoritative record is
			// never silently projected.
			if claim := legacyDeliveryClaim(meta); claim != "" {
				return &LegacyDeliveryEvidenceError{TaskID: id, Field: claim}
			}
		}

		project := meta["project"]
		kind := meta["kind"]
		if hasCanonical {
			if agg.Definition.Project != "" {
				project = agg.Definition.Project
			}
			if agg.Definition.Kind != "" {
				kind = agg.Definition.Kind
			}
		}
		ts := TaskSnapshot{
			ID:       id,
			Project:  project,
			Harness:  meta["harness"],
			Model:    meta["model"],
			Kind:     kind,
			Mode:     meta["mode"],
			Yolo:     meta["yolo"],
			Window:   meta["window"],
			Worktree: meta["worktree"],
			Home:     homeLabel,
			Source:   source,
		}
		if w := meta["window"]; w != "" {
			if endpointProbe != nil || paneAliveForCaptain != nil {
				status, err := observeEndpoint(taskHome, meta)
				if err != nil || status.State != EndpointAlive {
					ts.PaneAlive = false
					ts.PaneAliveUnknown = true
				} else {
					ts.PaneAlive = true
					ts.PaneAliveUnknown = false
				}
			} else {
				ts.PaneAlive = false
				ts.PaneAliveUnknown = true
			}
		}
		statusPath := filepath.Join(taskHome, "state", id+".status")
		if data, err := os.ReadFile(statusPath); err == nil {
			lines := strings.TrimSpace(string(data))
			if lines != "" {
				parts := strings.Split(lines, "\n")
				ts.LastStatus = strings.TrimSpace(parts[len(parts)-1])
			}
		}

		// Resolve current-state projection when resolver is wired. A
		// canonical record wins: the authoritative phase is the current state
		// and the status log is superseded display (a stale .status can never
		// override a newer authoritative lifecycle transition).
		info := CurrentState(taskHome, id, meta)
		if hasCanonical {
			ts.CurrentState = string(agg.Phase)
			ts.CurrentDescription = agg.PhaseDetail
			if ts.CurrentDescription == "" {
				ts.CurrentDescription = agg.Definition.Description
			}
			ts.StatusLogSuperseded = true
			ts.OpenActivities = info.OpenActivities
		} else {
			ts.CurrentState = info.State
			ts.CurrentDescription = info.Description
			ts.NoMistakesRunStep = info.NoMistakesRunStep
			ts.StatusLogSuperseded = info.StatusLogSuperseded
			ts.OpenActivities = info.OpenActivities
		}

		snap.Tasks = append(snap.Tasks, ts)
		seenIDs[id] = true
	}
	// Canonical tasks with no .meta projection are still part of the fleet.
	canonicalIDs := make([]string, 0, len(canonical))
	for id := range canonical {
		canonicalIDs = append(canonicalIDs, id)
	}
	sort.Strings(canonicalIDs)
	for _, id := range canonicalIDs {
		if seenIDs[id] {
			continue
		}
		agg := canonical[id]
		ts := TaskSnapshot{ID: id, Project: agg.Definition.Project, Kind: agg.Definition.Kind, Home: homeLabel, Source: source, CurrentState: string(agg.Phase), CurrentDescription: agg.PhaseDetail, StatusLogSuperseded: true}
		if ts.CurrentDescription == "" {
			ts.CurrentDescription = agg.Definition.Description
		}
		snap.Tasks = append(snap.Tasks, ts)
	}
	return nil
}

// View renders the fleet snapshot as Markdown.
func View(homeDir string) error {
	snap, err := Snapshot(homeDir)
	if err != nil {
		return err
	}

	fmt.Printf("# Fleet View — %s\n\n", snap.Time)
	fmt.Printf("Tasks: %d\n\n", len(snap.Tasks))

	for _, ts := range snap.Tasks {
		phase := PhaseFromProjection(ts)

		src := ts.Source
		if src == "" {
			src = "primary"
		}
		fmt.Printf("- **%s** (repo: %s) [%s]\n", ts.ID, ts.Project, src)
		fmt.Printf("  kind: %s | mode: %s | yolo: %s\n", ts.Kind, ts.Mode, ts.Yolo)
		// Only show harness line; omit empty model/effort
		var modelEffortParts []string
		if ts.Harness != "" {
			modelEffortParts = append(modelEffortParts, fmt.Sprintf("harness: %s", ts.Harness))
		}
		if ts.Model != "" {
			modelEffortParts = append(modelEffortParts, fmt.Sprintf("model: %s", ts.Model))
		}
		if len(modelEffortParts) > 0 {
			fmt.Printf("  %s\n", strings.Join(modelEffortParts, " | "))
		}

		// Show current state description when available, else status.
		displayStatus := ts.CurrentDescription
		if displayStatus == "" {
			displayStatus = ts.LastStatus
		}
		fmt.Printf("  pane: %s (%s)\n", ts.Window, phase)
		if displayStatus != "" {
			fmt.Printf("  status: %s\n", displayStatus)
		}
		fmt.Println()
	}

	return nil
}

// Bearings prints a compact resume report.
func Bearings(homeDir string, projectDir string) error {
	snap, err := Snapshot(homeDir)
	if err != nil {
		return err
	}

	fmt.Printf("# Bearings — %s\n\n", snap.Time)

	inFlight := 0
	for _, ts := range snap.Tasks {
		if ts.Kind != "ship" && ts.Kind != "scout" {
			continue
		}
		inFlight++
		phase := PhaseFromProjection(ts)
		src := ts.Source
		if src == "" {
			src = "primary"
		}
		displayStatus := ts.CurrentDescription
		if displayStatus == "" {
			displayStatus = ts.LastStatus
		}
		fmt.Printf("- **%s** (%s) [%s] — %s [%s]\n", ts.ID, ts.Project, src, displayStatus, phase)
	}

	if inFlight == 0 {
		fmt.Println("No in-flight ship/scout tasks. Fleet is idle.")
	}

	return nil
}

// CaptainStatus returns endpoint/meta truth for a captain.
// Prefer parent state/captain:<id>.meta window + backend Alive when meta exists.
// Home presence without launch meta is seeded; missing home is unknown.
// Captain-home state/.lock is not used for launched liveness.
func CaptainStatus(parentHome, captainID, homeDir string) string {
	if homeDir == "" {
		return "unknown"
	}
	if _, err := os.Stat(homeDir); err != nil {
		return "unknown"
	}
	if parentHome == "" || captainID == "" {
		return "seeded"
	}

	taskID := "captain:" + captainID
	meta, err := mhome.ReadMeta(parentHome, taskID)
	if err != nil {
		return "seeded"
	}
	if kind := meta["kind"]; kind != "" && kind != "captain" {
		return "seeded"
	}
	if id := meta["sm_id"]; id != "" && id != captainID {
		return "seeded"
	}
	window := meta["window"]
	if window == "" {
		return "seeded"
	}

	if endpointProbe == nil && paneAliveForCaptain == nil {
		return "unknown"
	}
	status, err := observeEndpoint(parentHome, meta)
	if err != nil {
		return "unknown"
	}
	switch status.State {
	case EndpointAlive:
		return "alive"
	case EndpointStarting:
		return "starting"
	case EndpointDead:
		return "dead"
	case EndpointUnresponsive:
		return "unresponsive"
	case EndpointUnresolved:
		return "unresolved"
	case EndpointStaleIdentity:
		return "stale-identity"
	default:
		return "unknown"
	}
}

// EndpointProbe is the narrow backend port used by fleet projections.
type EndpointRef struct {
	Backend      string
	Handle       string
	SessionOwner string
	WorkspaceID  string
	TabID        string
	Home         string
}

type EndpointProbe interface {
	ProbeEndpoint(EndpointRef) (EndpointStatus, error)
}

var endpointProbe EndpointProbe

// SetEndpointProbe installs the typed endpoint probe at the composition root.
func SetEndpointProbe(probe EndpointProbe) { endpointProbe = probe }

// paneAliveForCaptain remains only as a test seam while existing fleet tests are cut over.
var paneAliveForCaptain func(parentHome string, meta map[string]string) (bool, error)

func SetPaneAliveProbe(fn func(parentHome string, meta map[string]string) (bool, error)) {
	paneAliveForCaptain = fn
}

func observeEndpoint(parentHome string, meta map[string]string) (EndpointStatus, error) {
	if endpointProbe != nil {
		ownerHome := meta["home"]
		if ownerHome == "" {
			ownerHome = parentHome
		}
		return endpointProbe.ProbeEndpoint(EndpointRef{Backend: meta["backend"], Handle: meta["window"], SessionOwner: meta["herdr_session"], WorkspaceID: meta["herdr_workspace_id"], TabID: meta["herdr_tab_id"], Home: ownerHome})
	}
	alive, err := paneAliveForCaptain(parentHome, meta)
	if err != nil {
		return EndpointStatus{State: EndpointUnknown, Detail: err.Error()}, err
	}
	if alive {
		return EndpointStatus{State: EndpointAlive}, nil
	}
	return EndpointStatus{State: EndpointUnknown}, nil
}
