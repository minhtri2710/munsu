// Package supervision provides watcher check plugin infrastructure.
// A check plugin is an executable script (per-task or global) that the watcher
// discovers and surfaces as a Kind=check wake for the AFK/general pipeline.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckKind classifies a check plugin origin.
type CheckKind string

const (
	CheckPerTask CheckKind = "per-task" // state/<id>.check — one per task
	CheckGlobal  CheckKind = "global"   // state/checks/<name>.check — shared
)

// CheckPlugin represents a discoverable check script.
type CheckPlugin struct {
	Path  string    // absolute path to the check script
	Label string    // task ID for per-task, "global:<name>" for global
	Kind  CheckKind // origin classification
}

// DiscoverPerTaskChecks finds per-task .check files under state/.
// A per-task check is named <task-id>.check; nothing in munsu writes one, so it
// arrives from the operator or agent. The watcher validates each, retires the
// ones whose PR has merged, and surfaces the rest as check wakes.
func DiscoverPerTaskChecks(homeDir string) ([]CheckPlugin, error) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil, nil // no state dir yet
	}
	var plugins []CheckPlugin
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".check") {
			continue
		}
		name := entry.Name()
		taskID := strings.TrimSuffix(name, ".check")
		if taskID == "" || strings.HasPrefix(taskID, ".") {
			continue
		}
		plugins = append(plugins, CheckPlugin{
			Path:  filepath.Join(stateDir, name),
			Label: taskID,
			Kind:  CheckPerTask,
		})
	}
	return plugins, nil
}

// DiscoverGlobalChecks finds shared check scripts under state/checks/.
func DiscoverGlobalChecks(homeDir string) ([]CheckPlugin, error) {
	checksDir := filepath.Join(homeDir, "state", "checks")
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return nil, nil // no global checks dir
	}
	var plugins []CheckPlugin
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".check") {
			continue
		}
		name := entry.Name()
		label := "global:" + strings.TrimSuffix(name, ".check")
		plugins = append(plugins, CheckPlugin{
			Path:  filepath.Join(checksDir, name),
			Label: label,
			Kind:  CheckGlobal,
		})
	}
	return plugins, nil
}

// DiscoverAllChecks returns all check plugins (per-task + global).
func DiscoverAllChecks(homeDir string) ([]CheckPlugin, error) {
	perTask, err := DiscoverPerTaskChecks(homeDir)
	if err != nil {
		return nil, err
	}
	global, err := DiscoverGlobalChecks(homeDir)
	if err != nil {
		return nil, err
	}
	return append(perTask, global...), nil
}

// AcceptOrRefuseStale returns a verdict on a check artifact: true when the
// check is usable as it stands, false with an error naming what is stale.
// Nothing here migrates or rewrites a check; the only artifact it touches is
// a zero-length one, which it removes. A check is stale when:
//   - The file is zero-length
//   - The file content does not start with a shebang
//   - The file is older than its companion .meta file's last modification
//     (meta has been updated since the check was written, meaning the
//     task state has advanced but the check was not regenerated)
func AcceptOrRefuseStale(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("cannot stat check: %w", err)
	}
	if fi.Size() == 0 {
		// Zero-length: remove it
		os.Remove(path)
		return false, fmt.Errorf("refused zero-length check: %s", path)
	}
	// Read first bytes for shebang
	data := make([]byte, 2)
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("opening check: %w", err)
	}
	defer f.Close()
	n, err := f.Read(data)
	if err != nil || n < 2 || data[0] != '#' || data[1] != '!' {
		// Invalid content: re-write is not safe; refuse
		return false, fmt.Errorf("refused stale check (no valid shebang): %s", path)
	}
	// Check against companion .meta modification time if applicable
	// (per-task checks only — global checks have no companion)
	checkName := filepath.Base(path)
	if strings.HasSuffix(checkName, ".check") && !strings.HasPrefix(checkName, ".") {
		taskID := strings.TrimSuffix(checkName, ".check")
		metaPath := filepath.Join(filepath.Dir(path), taskID+".meta")
		if metaFI, err := os.Stat(metaPath); err == nil {
			if fi.ModTime().Before(metaFI.ModTime()) {
				// Meta was updated after check was written — stale
				return false, fmt.Errorf("refused stale check (meta newer): %s", path)
			}
		}
	}
	return true, nil // check is valid
}
