package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/task"
)

// FleetSnapshot represents the full fleet state.
type FleetSnapshot struct {
	Schema  string         `json:"schema"`
	Time    string         `json:"time"`
	Tasks   []TaskSnapshot `json:"tasks"`
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
}

// Snapshot builds a fleet snapshot by scanning state/*.meta and state/*.status.
func Snapshot(homeDir string) (*FleetSnapshot, error) {
	snap := &FleetSnapshot{
		Schema: "munsu-fleet-snapshot.v1",
		Time:   time.Now().UTC().Format(time.RFC3339),
	}

	metasDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(metasDir)
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := task.ReadMeta(homeDir, id)
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
		}

		// Check pane liveness by looking for the window in meta
		// (pane check skipped to avoid import cycle with session package)
		if w := meta["window"]; w != "" {
			ts.PaneAlive = true // assumed alive if meta has window
		}

		// Read last status line
		statusPath := filepath.Join(homeDir, "state", id+".status")
		if data, err := os.ReadFile(statusPath); err == nil {
			lines := strings.TrimSpace(string(data))
			if lines != "" {
				parts := strings.Split(lines, "\n")
				ts.LastStatus = strings.TrimSpace(parts[len(parts)-1])
			}
		}

		snap.Tasks = append(snap.Tasks, ts)
	}

	return snap, nil
}

// JSON returns the snapshot as indented JSON.
func (s *FleetSnapshot) JSON() (string, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
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
		phase := "dead"
		if ts.PaneAlive {
			phase = "alive"
		} else if ts.Window == "" {
			phase = "registered"
		}

		fmt.Printf("- **%s** (repo: %s)\n", ts.ID, ts.Project)
		fmt.Printf("  kind: %s | mode: %s | yolo: %s\n", ts.Kind, ts.Mode, ts.Yolo)
		fmt.Printf("  harness: %s | model: %s\n", ts.Harness, ts.Model)
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
		if ts.Kind == "ship" || ts.Kind == "scout" {
			inFlight++
			phase := "alive"
			if !ts.PaneAlive {
				if ts.Window == "" {
					phase = "registered"
				} else {
					phase = "dead"
				}
			}
			fmt.Printf("- **%s** (%s) — %s [%s]\n", ts.ID, ts.Project, ts.LastStatus, phase)
		}
	}

	if inFlight == 0 {
		fmt.Println("No in-flight tasks. Fleet is idle.")
	}

	return nil
}
