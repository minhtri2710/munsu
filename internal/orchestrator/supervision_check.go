// Package supervision provides watcher check plugin infrastructure.
// A check plugin is an executable script (per-task or global) that the watcher
// discovers and surfaces as a Kind=check wake for the AFK/general pipeline.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
)

// CheckKind classifies a check plugin origin.
type CheckKind string

const (
	CheckPerTask CheckKind = "per-task" // state/<durable-stem>.check — one per task
	CheckGlobal  CheckKind = "global"   // state/checks/<name>.check — shared
)

// CheckPlugin represents a discoverable check script.
type CheckPlugin struct {
	Path  string    // absolute path to the check script
	Label string    // task ID for per-task, "global:<name>" for global
	Kind  CheckKind // origin classification
}

// DiscoverPerTaskChecks finds per-task .check files under state/. Their
// durable filename stems are reverse-decoded to logical task IDs at this
// discovery boundary; malformed stems fail closed. Nothing in munsu writes
// these files, so they arrive from the operator or agent. The watcher validates
// each, retires the ones whose PR has merged, and surfaces the rest as check wakes.
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
		taskStem := strings.TrimSuffix(name, ".check")
		if taskStem == "" || strings.HasPrefix(taskStem, ".") {
			continue
		}
		taskID, err := home.ReverseDurableKey(taskStem)
		if err != nil {
			return nil, fmt.Errorf("decoding per-task check stem %q: %w", taskStem, err)
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

// AcceptOrRefuseStale reports whether a check artifact the caller has already
// validated is still current: nil when it is, otherwise an error naming what is
// stale. A check is stale when it is older than its companion .meta file's last
// modification — meta has been updated since the check was written, so the task
// state has advanced but the check was not regenerated. Global checks have no
// companion and are never stale.
//
// It only reads. No refusal removes or rewrites the artifact, here or in the
// caller, because a .check file is written by an operator or an agent and never
// by munsu — deleting one destroys the only copy of something a human may need
// to look at, and no refusal here is worth that.
//
// PRECONDITION: path has passed CheckValidationPort.ValidateCheck. Check
// runnability is owned by fleet.ValidateCheckWithLstat; this function only
// compares artifact and task-state timestamps.
func AcceptOrRefuseStale(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat check: %w", err)
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
				return fmt.Errorf("refused stale check (meta newer): %s", path)
			}
		}
	}
	return nil
}
