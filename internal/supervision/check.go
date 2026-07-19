// Package supervision provides watcher check plugin infrastructure.
// A check plugin is an executable script (per-task or global) that the watcher
// discovers and surfaces as a Kind=check wake for the AFK/general pipeline.
package supervision
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

// CheckResult holds the outcome of validating or migrating a check plugin.
type CheckResult struct {
	Plugin    CheckPlugin
	Valid     bool   // true when the check is present and ready
	Stale     bool   // true when the artifact should be refused/migrated
	Staleness string // description of what is stale, if applicable
}

// DiscoverPerTaskChecks finds per-task .check files under state/.
// These are written by `munsu delivery pr-check` and named <task-id>.check.
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

// ValidateCheck ensures the check script exists, is executable,
// and starts with a valid shebang.
func ValidateCheck(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("check not found: %w", err)
	}
	// Must be a regular file
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("check is not a regular file: %s", path)
	}
	// Must be executable (owner at minimum)
	if fi.Mode()&0100 == 0 {
		return fmt.Errorf("check is not executable: %s", path)
	}
	// Read first line to verify shebang
	data := make([]byte, 2)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening check: %w", err)
	}
	defer f.Close()
	n, err := f.Read(data)
	if err != nil || n < 2 {
		return fmt.Errorf("check is empty or unreadable: %s", path)
	}
	if data[0] != '#' || data[1] != '!' {
		return fmt.Errorf("check is missing shebang (#!): %s", path)
	}
	return nil
}

// MigrateOrRefuseStale checks whether a check artifact is stale and either
// migrates it (if possible) or signals refusal. Returns true if the check
// was migrated, false if it was refused (caller should remove/recreate).
// A check is stale when:
//   - The file is zero-length
//   - The file content does not start with a shebang
//   - The file is older than its companion .meta file's last modification
//     (meta has been updated since the check was written, meaning the
//      task state has advanced but the check was not regenerated)
func MigrateOrRefuseStale(path string) (bool, error) {
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

