package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/task"
)

// FleetSnapshot represents the full fleet state.
type FleetSnapshot struct {
	Schema string         `json:"schema"`
	Time   string         `json:"time"`
	Tasks  []TaskSnapshot `json:"tasks"`
}

// TaskSnapshot represents one task's state.
type TaskSnapshot struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	Harness    string `json:"harness"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	Mode       string `json:"mode"`
	Yolo       string `json:"yolo"`
	Window     string `json:"window"`
	Worktree   string `json:"worktree"`
	PaneAlive  bool   `json:"pane_alive"`
	LastStatus string `json:"last_status,omitempty"`
	// Home is the munsu home that owns this task meta (primary or captain).
	Home string `json:"home,omitempty"`
	// Source labels where the task was discovered: "primary" or "captain:<id>".
	Source string `json:"source,omitempty"`
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
	metasDir := filepath.Join(taskHome, "state")
	entries, err := os.ReadDir(metasDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := task.ReadMeta(taskHome, id)
		if err != nil {
			continue
		}

		ts := TaskSnapshot{
			ID:       id,
			Project:  meta["project"],
			Harness:  meta["harness"],
			Model:    meta["model"],
			Kind:     meta["kind"],
			Mode:     meta["mode"],
			Yolo:     meta["yolo"],
			Window:   meta["window"],
			Worktree: meta["worktree"],
			Home:     homeLabel,
			Source:   source,
		}
		// Pane check skipped here to avoid import cycle with session package.
		if w := meta["window"]; w != "" {
			ts.PaneAlive = true
		}
		statusPath := filepath.Join(taskHome, "state", id+".status")
		if data, err := os.ReadFile(statusPath); err == nil {
			lines := strings.TrimSpace(string(data))
			if lines != "" {
				parts := strings.Split(lines, "\n")
				ts.LastStatus = strings.TrimSpace(parts[len(parts)-1])
			}
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
		phase := PhaseFromMeta(ts.Window, ts.PaneAlive)

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
		fmt.Printf("  pane: %s (%s)\n", ts.Window, phase)
		if ts.LastStatus != "" {
			fmt.Printf("  status: %s\n", ts.LastStatus)
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
		phase := PhaseFromMeta(ts.Window, ts.PaneAlive)
		src := ts.Source
		if src == "" {
			src = "primary"
		}
		fmt.Printf("- **%s** (%s) [%s] — %s [%s]\n", ts.ID, ts.Project, src, ts.LastStatus, phase)
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
	meta, err := task.ReadMeta(parentHome, taskID)
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

	if paneAliveForCaptain == nil {
		return "dead"
	}
	alive, aliveErr := paneAliveForCaptain(parentHome, meta)
	if aliveErr != nil {
		return "dead"
	}
	if alive {
		return "alive"
	}
	return "dead"
}

// paneAliveForCaptain probes endpoint liveness from parent meta.
// Wired by SetPaneAliveProbe (CLI) so fleet does not import session (cycle).
// Nil means backend cannot be resolved → dead when meta has a window.
var paneAliveForCaptain func(parentHome string, meta map[string]string) (bool, error)

// SetPaneAliveProbe installs the session-backend Alive probe used by CaptainStatus.
func SetPaneAliveProbe(fn func(parentHome string, meta map[string]string) (bool, error)) {
	paneAliveForCaptain = fn
}
