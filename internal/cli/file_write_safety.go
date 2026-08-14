package cli

import (
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// evaluateFileWriteSafety decides whether a native file-write tool call
// (Write/Edit/MultiEdit/NotebookEdit and their per-harness equivalents) may
// touch filePath. Unlike evaluateGitMutationSafety it never inspects a command
// string: the target comes straight out of the tool payload, so the decision is
// made on location alone.
//
// It blocks exactly one case — a target inside a primary checkout while a munsu
// task run is active. Everything else passes:
//
//   - No MUNSU_TASK_ID: not a task run. Generals, humans and munsu's own sync
//     paths write into the primary checkout on purpose.
//   - Target outside any git repository, or unresolvable: writing to /tmp, the
//     agent workdir or a scratch directory is ordinary work. Refusing those
//     would stall every run, which is the failure mode this guard must not have.
//
// The fail-open choice for classification errors is deliberate and is the
// opposite of the cwd-based gate in bootstrap.SafetyCheck: that one classifies
// the session's working tree, where "not a repository" is already anomalous,
// while here the argument is an arbitrary file path.
func evaluateFileWriteSafety(filePath string) (bool, string) {
	if filePath == "" {
		return false, ""
	}
	if !bootstrap.TaskRunActive() {
		return false, ""
	}
	dir := nearestExistingDir(filePath)
	if dir == "" {
		return false, ""
	}
	identity, _, _, err := fleet.ClassifyIdentity(dir)
	if err != nil {
		return false, ""
	}
	if identity != fleet.Primary {
		return false, ""
	}
	return true, "primary checkout refused for file write inside an active munsu task run: " +
		filePath + " — write inside the bound worktree instead"
}

// nearestExistingDir returns the closest existing ancestor directory of path.
// A tool that creates a new file names a target that does not exist yet, and
// git classification needs a directory that does.
func nearestExistingDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	current := abs
	for {
		if info, err := os.Stat(current); err == nil {
			if info.IsDir() {
				return current
			}
			return filepath.Dir(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
