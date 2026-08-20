package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
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

// CurrentStateInfo carries the authoritative current state for a task. The
// State/Description derive from the canonical Task Authority phase; all other
// fields are evidence/diagnostic only.
type CurrentStateInfo struct {
	State               string            `json:"state"`
	Description         string            `json:"description"`
	NoMistakesRunStep   string            `json:"no_mistakes_run_step,omitempty"`
	StatusLogSuperseded bool              `json:"status_log_superseded"`
	OpenActivities      []domain.Activity `json:"open_activities,omitempty"`
}

// CurrentStateQuery reads the authoritative current state for one task. It is
// the single current-state read used by snapshot, guard, and observation so
// agents and the CLI receive the same Task truth. A missing or malformed
// canonical record is an operation error, never a projection fallback.
type CurrentStateQuery interface {
	Read(homeDir, taskID string) (*CurrentStateInfo, error)
}

// SnapshotDependencies carries the explicit read dependencies for a fleet
// snapshot. CurrentState is required; Endpoint is an optional diagnostic-only
// probe. There is no implicit/package-global wiring.
type SnapshotDependencies struct {
	// CurrentState is the required canonical current-state query.
	CurrentState CurrentStateQuery
	// Endpoint is an optional probe used only to populate PaneAlive/
	// PaneAliveUnknown diagnostics; it never changes lifecycle state.
	Endpoint EndpointProbe
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

// Snapshot builds a fleet snapshot from canonical Task Authority records in
// the primary home and each registered captain home. The canonical record is
// the only authority; a task-facing `.meta` entry without a canonical record
// is rejected (clean break), while captain metadata entries remain as
// non-authority display. Endpoint probing is diagnostic only.
func Snapshot(homeDir string, deps SnapshotDependencies) (*FleetSnapshot, error) {
	if deps.CurrentState == nil {
		return nil, fmt.Errorf("reading authoritative current state: no current-state query provided (home %s)", homeDir)
	}
	snap := &FleetSnapshot{
		Schema: "munsu-fleet-snapshot.v1",
		Time:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := appendHomeTasks(snap, homeDir, "primary", "", deps); err != nil {
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
			if err := appendHomeTasks(snap, ch, src, ch, deps); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("scanning captain home %s: %w", ch, err)
			}
		}
	}

	return snap, nil
}

func appendHomeTasks(snap *FleetSnapshot, taskHome, source, homeLabel string, deps SnapshotDependencies) error {
	// Canonical Task Authority records are the only authority (clean break,
	// Task 7.8): kind/project/phase come from the canonical record; the
	// .meta/.status/probe data is diagnostic display only and can never
	// override an authoritative lifecycle transition. A home that is not a
	// canonical v1 home, or a task-facing meta without an authoritative
	// record, fails closed.
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

	// Reject task-facing meta-only entries (no canonical record). Captain
	// metadata (kind=captain) is exempt: it is non-task authority metadata
	// that lives outside the Task Authority by design.
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id, err := mhome.ReverseDurableKey(strings.TrimSuffix(entry.Name(), ".meta"))
		if err != nil {
			continue
		}
		if _, hasCanonical := canonical[id]; hasCanonical {
			continue
		}
		meta, metaErr := mhome.ReadMeta(taskHome, id)
		if metaErr == nil && meta["kind"] == "captain" {
			continue
		}
		return fmt.Errorf("reading authoritative current state for task %q in home %q: no canonical Task Authority record (legacy/meta-only tasks are not authoritative)", id, taskHome)
	}

	canonicalIDs := make([]string, 0, len(canonical))
	for id := range canonical {
		canonicalIDs = append(canonicalIDs, id)
	}
	sort.Strings(canonicalIDs)

	for _, id := range canonicalIDs {
		agg := canonical[id]

		// .meta holds operational/display fields (diagnostic only).
		meta, _ := mhome.ReadMeta(taskHome, id)

		// Authoritative current state comes from the single canonical query.
		info, err := deps.CurrentState.Read(taskHome, id)
		if err != nil {
			return fmt.Errorf("reading authoritative current state for task %q in home %q: %w", id, taskHome, err)
		}

		ts := TaskSnapshot{
			ID:                  id,
			Project:             agg.Definition.Project,
			Kind:                agg.Definition.Kind,
			Harness:             meta["harness"],
			Model:               meta["model"],
			Mode:                meta["mode"],
			Yolo:                meta["yolo"],
			Window:              meta["window"],
			Worktree:            meta["worktree"],
			Home:                homeLabel,
			Source:              source,
			CurrentState:        info.State,
			CurrentDescription:  info.Description,
			NoMistakesRunStep:   info.NoMistakesRunStep,
			StatusLogSuperseded: true,
			OpenActivities:      info.OpenActivities,
		}
		if ts.CurrentDescription == "" {
			ts.CurrentDescription = agg.Definition.Description
		}

		// Endpoint liveness is diagnostic only and never changes lifecycle state.
		if ts.Window != "" && deps.Endpoint != nil {
			status, perr := observeEndpointWith(deps.Endpoint, taskHome, meta)
			if perr != nil || !status.Live() {
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

		// LastStatus is a diagnostic display line (never state truth).
		if statusPath, err := mhome.StatusFilePath(taskHome, id); err == nil {
			if data, err := os.ReadFile(statusPath); err == nil {
				lines := strings.TrimSpace(string(data))
				if lines != "" {
					parts := strings.Split(lines, "\n")
					ts.LastStatus = strings.TrimSpace(parts[len(parts)-1])
				}
			}
		}

		snap.Tasks = append(snap.Tasks, ts)
	}

	// Surface captain metadata entries (non-authority) so the fleet view still
	// shows captains that have no task-authority record.
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id, err := mhome.ReverseDurableKey(strings.TrimSuffix(entry.Name(), ".meta"))
		if err != nil {
			continue
		}
		meta, metaErr := mhome.ReadMeta(taskHome, id)
		if metaErr != nil || meta["kind"] != "captain" {
			continue
		}
		ts := TaskSnapshot{
			ID: id, Project: meta["project"], Kind: "captain", Home: homeLabel, Source: source,
			Harness: meta["harness"], Model: meta["model"], Mode: meta["mode"], Yolo: meta["yolo"],
			Window: meta["window"], Worktree: meta["worktree"], PaneAliveUnknown: true,
		}
		snap.Tasks = append(snap.Tasks, ts)
	}

	return nil
}

// View renders the fleet snapshot as Markdown.
func View(homeDir string, deps SnapshotDependencies) error {
	snap, err := Snapshot(homeDir, deps)
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
func Bearings(homeDir string, projectDir string, deps SnapshotDependencies) error {
	snap, err := Snapshot(homeDir, deps)
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
func CaptainStatus(parentHome, captainID, homeDir string, probe EndpointProbe) string {
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

	if probe == nil {
		return "unknown"
	}
	status, err := observeProbe(probe, parentHome, meta)
	if err != nil {
		return "unknown"
	}
	switch status.State() {
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
	// Incarnation is the opaque generation-bound endpoint identity (when known)
	// used to freshness cross-check observations of the exact binding.
	Incarnation string
}

type EndpointProbe interface {
	ProbeEndpoint(EndpointRef) (EndpointStatus, error)
}

// observeProbe applies an EndpointProbe to a task's meta-derived endpoint
// identity. The probe is diagnostic only; a probe that errs or reports a
// non-alive state yields an unknown/non-alive diagnostic, never a lifecycle
// decision.
func observeProbe(probe EndpointProbe, parentHome string, meta map[string]string) (EndpointStatus, error) {
	ownerHome := meta["home"]
	if ownerHome == "" {
		ownerHome = parentHome
	}
	return probe.ProbeEndpoint(EndpointRef{Backend: meta["backend"], Handle: meta["window"], SessionOwner: meta["herdr_session"], WorkspaceID: meta["herdr_workspace_id"], TabID: meta["herdr_tab_id"], Home: ownerHome})
}

// observeEndpointWith is the snapshot-local endpoint observation using the
// caller-provided diagnostic probe from SnapshotDependencies.
func observeEndpointWith(probe EndpointProbe, parentHome string, meta map[string]string) (EndpointStatus, error) {
	return observeProbe(probe, parentHome, meta)
}
