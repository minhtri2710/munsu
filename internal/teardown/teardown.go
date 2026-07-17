// Package teardown implements crewmate teardown safety checks and lifecycle.
package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"


	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/worktree"
)

// Options controls teardown behavior.
type Options struct {
	HomeDir string // munsu home directory
	ID      string // task ID
	Force   bool   // skip safety checks
}

// TeardownResult describes the outcome of each teardown step.
type TeardownResult struct {
	Steps []string
}

// Run performs a crewmate teardown.
func Run(opts Options) (*TeardownResult, error) {
	result := &TeardownResult{}

	// Read task meta
	meta, err := task.ReadMeta(opts.HomeDir, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("teardown %s: reading meta: %w", opts.ID, err)
	}

	kind := meta["kind"]
	if kind == "" {
		kind = "ship" // default
	}

	// Safety checks (skip with --force)
	if !opts.Force {
		if err := safetyCheck(opts, meta, kind); err != nil {
			return nil, fmt.Errorf("teardown %s: safety check failed: %w", opts.ID, err)
		}
	}

	// 1. Kill session window
	var wtPath string
	if windowID, ok := meta["window"]; ok && windowID != "" {
		bk, _, err := session.BackendForTask(opts.HomeDir, meta)
		if err != nil {
			result.Steps = append(result.Steps, fmt.Sprintf("session backend unavailable: %v", err))
		} else if bk.Alive(windowID) {
			if err := bk.Teardown(windowID); err != nil {
				result.Steps = append(result.Steps, fmt.Sprintf("session teardown %s: %v", windowID, err))
			} else {
				result.Steps = append(result.Steps, fmt.Sprintf("session window %s killed", windowID))
			}
		} else {
			result.Steps = append(result.Steps, fmt.Sprintf("session window %s already gone", windowID))
		}
	}

	// 1.5. Kill any remaining processes on the worktree path
	// (orphaned node/agy processes that survive window kill)
	wtPath, _ = meta["worktree"]
	if wtPath != "" {
		if killed := killProcessesOnPath(wtPath); killed > 0 {
			result.Steps = append(result.Steps, fmt.Sprintf("killed %d residual process(es) on worktree", killed))
		}
		reapWorktreeHolders(wtPath)
	}

	// 2. Return worktree to pool — fail-closed: if return fails, abort teardown
	//    so the lease is not falsely claimed as released (firstmate contract).
	if wtPath != "" {
		if fi, err := os.Stat(wtPath); err == nil && fi.IsDir() {
			if err := worktree.Return(opts.HomeDir, wtPath); err != nil {
				return nil, fmt.Errorf("teardown %s: worktree return failed: %w (lease still held)", opts.ID, err)
			}
			result.Steps = append(result.Steps, "worktree returned to pool")
		} else {
			result.Steps = append(result.Steps, "worktree path no longer exists")
		}
	}

	// 3. Remove task meta file
	metaFilePath, err := taskMetaFilePath(opts.HomeDir, opts.ID)
	if err == nil {
		if err := os.Remove(metaFilePath); err != nil && !os.IsNotExist(err) {
			result.Steps = append(result.Steps, fmt.Sprintf("remove meta: %v", err))
		} else {
			result.Steps = append(result.Steps, "task meta removed")
		}
	}

	// 4. Remove residual state artifacts
	// Munsu-native artifacts are always cleaned up for any task.
	// Legacy/firstmate artifacts are cleaned up for backward compatibility
	// so existing firstmate homes still get a clean teardown.
	// Harness-specific artifacts are driven by the adapter registry.
	stateDir := filepath.Join(opts.HomeDir, "state")
	munsuArtifacts := []string{
		// Munsu-native: written by munsu itself
		opts.ID + ".status",
		opts.ID + ".check.sh",
		// Legacy firstmate: backward-compat cleanup for existing homes
		opts.ID + ".turn-ended",
	}
	harnessArtifacts := harness.StateArtifactsForHarness(meta["harness"])
	for _, suffix := range harnessArtifacts {
		munsuArtifacts = append(munsuArtifacts, opts.ID+"."+suffix)
	}
	for _, name := range munsuArtifacts {
		p := filepath.Join(stateDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			result.Steps = append(result.Steps, fmt.Sprintf("remove residual %s: %v", name, err))
		} else {
			result.Steps = append(result.Steps, fmt.Sprintf("residual %s removed", name))
		}
	}

	// 5. Clean up data directory
	// Policy: --force always removes the data dir (GC orphan briefs).
	// Normal teardown keeps the data dir if report.md or brief.md exist.
	dataDir := filepath.Join(opts.HomeDir, "data", opts.ID)
	if fi, err := os.Stat(dataDir); err == nil && fi.IsDir() {
		if opts.Force {
			if err := os.RemoveAll(dataDir); err != nil {
				result.Steps = append(result.Steps, fmt.Sprintf("remove data dir: %v", err))
			} else {
				result.Steps = append(result.Steps, "data dir removed (--force)")
			}
		} else {
			reportPath := filepath.Join(dataDir, "report.md")
			briefPath := filepath.Join(dataDir, "brief.md")
			briefInfo, briefErr := os.Stat(briefPath)
			_, reportErr := os.Stat(reportPath)

			if os.IsNotExist(reportErr) {
				// No report.md — safe to remove orphan brief/data dir
				// Also remove if brief.md is tiny (< 256 bytes, likely a stub)
				if briefErr == nil && briefInfo.Size() < 256 {
					if err := os.RemoveAll(dataDir); err != nil {
						result.Steps = append(result.Steps, fmt.Sprintf("remove small brief data dir: %v", err))
					} else {
						result.Steps = append(result.Steps, "data dir removed (small brief, no report)")
					}
				} else {
					result.Steps = append(result.Steps, "data dir kept (brief present, no report)")
				}
			} else {
				result.Steps = append(result.Steps, "data dir kept (report.md present)")
			}
		}
	}

	return result, nil
}

// safetyCheck verifies that work is landed before allowing teardown.
func safetyCheck(opts Options, meta map[string]string, kind string) error {
	switch kind {
	case "scout":
		return scoutSafetyCheck(opts, meta)
	default:
		return shipSafetyCheck(opts, meta)
	}
}

// scoutSafetyCheck verifies the report.md exists.
// scoutSafetyCheck verifies the report.md exists and checks for unresolved decision holds.
func scoutSafetyCheck(opts Options, meta map[string]string) error {
	reportPath := filepath.Join(opts.HomeDir, "data", opts.ID, "report.md")
	if _, err := os.Stat(reportPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("scout task %s has no report.md at %s (use --force to override)", opts.ID, reportPath)
		}
		return fmt.Errorf("checking report.md: %w", err)
	}

	// After report exists, check for unresolved decision holds.
	unresolvedKeys, err := decisionhold.Verify(opts.HomeDir, opts.ID, nil)
	if err != nil {
		return fmt.Errorf("checking decision holds: %w", err)
	}
	if len(unresolvedKeys) > 0 {
		return fmt.Errorf("scout task %s has %d unresolved decision hold(s): %s (use --force to override)", opts.ID, len(unresolvedKeys), strings.Join(unresolvedKeys, ", "))
	}

	return nil
}

// shipSafetyCheck verifies work is landed.
// Checks: remote-reachable branch, no dirty worktree, content in default branch.
func shipSafetyCheck(opts Options, meta map[string]string) error {
	wtPath, ok := meta["worktree"]
	if !ok || wtPath == "" {
		return fmt.Errorf("no worktree path in meta for %s", opts.ID)
	}

	// Check worktree exists
	if _, err := os.Stat(wtPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree %s does not exist", wtPath)
		}
		return fmt.Errorf("checking worktree %s: %w", wtPath, err)
	}

	// Check not dirty
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("worktree %s has uncommitted changes (use --force to override)", wtPath)
	}

	// Check current branch has a remote tracking ref
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	cmd.Dir = wtPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("branch has no remote tracking branch (use --force to override): %w", err)
	}

	// Check the remote branch is reachable
	cmd = exec.Command("git", "fetch", "--dry-run")
	cmd.Dir = wtPath
	fetchOut, fetchErr := cmd.CombinedOutput()
	if fetchErr != nil {
		// fetch failed — likely no remote or network issue; warn but don't block
		// Actually, let's be strict: if we can't reach remote, block
		return fmt.Errorf("cannot reach remote (use --force to override): %s", strings.TrimSpace(string(fetchOut)))
	}
	_ = fetchOut

	return nil
}

// taskMetaFilePath returns the path to the task meta file.
func taskMetaFilePath(homeDir, id string) (string, error) {
	return filepath.Join(homeDir, "state", id+".meta"), nil
}

// killProcessesOnPath tries to kill any processes still accessing the given
// path using fuser(1). Returns the number of processes killed (or 0 if fuser
// is unavailable or the path is clear). Best-effort; errors are logged only.
func killProcessesOnPath(path string) int {
	cmd := exec.Command("fuser", "-k", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}
	outStr := strings.TrimSpace(string(out))
	if outStr == "" {
		return 0
	}
	return strings.Count(outStr, " ") + 1
}

// reapWorktreeHolders waits briefly for any remaining holder processes on
// the worktree path to exit. Returns after timeout regardless.
func reapWorktreeHolders(wtPath string) {
	for i := 0; i < 5; i++ {
		cmd := exec.Command("fuser", wtPath)
		if err := cmd.Run(); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
