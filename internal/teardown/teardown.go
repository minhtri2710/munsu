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
	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/ghurl"
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
	Steps  []string
	Proofs []string // merge-proof evidence emitted by safety checks
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
		proofs, err := safetyCheck(opts, meta, kind)
		if err != nil {
			return nil, fmt.Errorf("teardown %s: safety check failed: %w", opts.ID, err)
		}
		result.Proofs = append(result.Proofs, proofs...)
		for _, p := range proofs {
			result.Steps = append(result.Steps, "proof: "+p)
		}
	}

	// 1. Kill session window
	var wtPath string
	if windowID, ok := meta["window"]; ok && windowID != "" {
		bk, bkName, err := session.BackendForTask(opts.HomeDir, meta)
		if err != nil {
			result.Steps = append(result.Steps, fmt.Sprintf("session backend unavailable: %v", err))
		} else {
			// For herdr backends, deny workspace close if another task references it
			if bkName == "herdr" {
				if hb, ok := bk.(*session.HerdrBackend); ok {
					if wsID := meta["herdr_workspace_id"]; wsID != "" {
						if refs := otherWorkspaceRefs(opts.HomeDir, opts.ID, wsID); len(refs) > 0 {
							hb.DenyCloseWorkspaceIDs = append(hb.DenyCloseWorkspaceIDs, wsID)
						}
					}
				}
			}
			if !bk.Alive(windowID) {
				result.Steps = append(result.Steps, fmt.Sprintf("session window %s already gone (still tearing down)", windowID))
			}
			if err := bk.Teardown(windowID); err != nil {
				result.Steps = append(result.Steps, fmt.Sprintf("session teardown %s: %v", windowID, err))
			} else {
				result.Steps = append(result.Steps, fmt.Sprintf("session window %s killed", windowID))
			}
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
	// During the item-5 dual-read migration window, both old and new names
	// are cleaned up. Old names (.check.sh, .turn-ended) will be removed
	// in a future release; new names (.check, .turnend) are the canonical forms.
	// Harness-specific artifacts are driven by the adapter registry.
	stateDir := filepath.Join(opts.HomeDir, "state")
	munsuArtifacts := []string{
		// Munsu-native: canonical names (item-5 rename)
		opts.ID + ".status",
		opts.ID + ".check",          // new canonical name
		opts.ID + ".turnend",        // new canonical name
		// Legacy names (dual-read, remove next release)
		opts.ID + ".check.sh",       // legacy name (deprecated)
		opts.ID + ".turn-ended",     // legacy name (deprecated)
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
// Returns proof strings alongside any error. Proofs are only populated on success.
func safetyCheck(opts Options, meta map[string]string, kind string) ([]string, error) {
	switch kind {
	case "scout":
		if err := scoutSafetyCheck(opts, meta); err != nil {
			return nil, err
		}
		return nil, nil
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

// shipSafetyCheck verifies work is landed before teardown.
// It separates cleanliness checks (dirty worktree) from merge-proof checks
// (topology-aware PR merge verification using delivery identity).
// Returns proof strings emitted during merge-proof checks.
func shipSafetyCheck(opts Options, meta map[string]string) ([]string, error) {
	wtPath, ok := meta["worktree"]
	if !ok || wtPath == "" {
		return nil, fmt.Errorf("no worktree path in meta for %s", opts.ID)
	}

	// --- Cleanliness checks (always run) ---

	// Check worktree exists
	if _, err := os.Stat(wtPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("worktree %s does not exist", wtPath)
		}
		return nil, fmt.Errorf("checking worktree %s: %w", wtPath, err)
	}

	// Check worktree is not dirty
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return nil, fmt.Errorf("worktree %s has uncommitted changes (use --force to override)", wtPath)
	}

	// --- Merge-proof checks (topology-aware) ---

	// If the task has a delivery identity, use provider-confirmed PR status
	// to determine whether the work is safely landed.
	ident, err := identityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("reading delivery identity: %w", err)
	}

	if ident != nil {
		// Validate identity before any provider query or fallback.
		// Partial/corrupt identity must fail closed and never silently
		// degrade to the legacy branch check.
		if err := delivery.ValidateIdentity(ident); err != nil {
			return nil, fmt.Errorf("invalid delivery identity (fail-closed, no legacy fallback): %w", err)
		}

		proof, err := topologyAwareMergeCheck(opts, meta, wtPath, ident)
		if err != nil {
			return nil, err
		}
		return []string{proof}, nil
	}

	// No delivery identity: fall back to simple remote branch check
	if err := checkRemoteBranch(wtPath); err != nil {
		return nil, err
	}

	return nil, nil
}

// identityFromMeta attempts to reconstruct a delivery identity from task meta.
// Returns nil (no error) when no identity metadata exists, so the caller can
// fall back to a simple remote branch check.
func identityFromMeta(meta map[string]string) (*delivery.DeliveryIdentity, error) {
	prURL := meta["pr_url"]
	if prURL == "" {
		// No identity at all — fall through to simple check
		return nil, nil
	}
	return delivery.IdentityFromMeta(meta)
}

// topologyAwareMergeCheck verifies the work is landed using the provider's
// confirmed PR merge status. It supports three merge topologies:
//   - Squash/rebase merge with deleted head: accepts provider-confirmed merged PR identity
//   - Ordinary merge: retains ancestry proof (merged branch still exists locally)
//   - Unknown/unverifiable: refuses teardown
// Returns the proof string on success.
func topologyAwareMergeCheck(opts Options, meta map[string]string, wtPath string, ident *delivery.DeliveryIdentity) (string, error) {
	ghURL, err := ghurl.ParseGHURL(ident.URL)
	if err != nil {
		return "", fmt.Errorf("invalid PR URL in delivery identity: %w", err)
	}

	// Query the provider for the current PR merge status
	status, err := delivery.QueryPRMergeStatus(ghURL)
	if err != nil {
		return "", fmt.Errorf("cannot verify PR merge status: %w (use --force to override)", err)
	}

	// Check for refused states
	if status.Closed && !status.Merged {
		return "", fmt.Errorf("PR #%d is closed but not merged (use --force to override)", ident.Number)
	}

	if !status.Merged && status.State == "OPEN" {
		return "", fmt.Errorf("PR #%d is still open and not merged (use --force to override)", ident.Number)
	}

	// PR is confirmed merged. Verify head SHA consistency for safety.
	if status.Merged {
		// Build the exact proof used, augmented with topology details below.
		proof := fmt.Sprintf("PR #%d merged; provider-confirmed state=merged headSHA=%s", ident.Number, status.HeadSHA)

		// For deleted remote head (squash/rebase), confirm the PR IS merged
		// — that's sufficient proof. The remote branch may be gone.
		// Check: if the remote branch still exists, we can do a stronger check.
		remoteBranchExists := false
		if ident.HeadRef != "" {
			checkCmd := exec.Command("git", "ls-remote", "--exit-code", "origin", "refs/heads/"+ident.HeadRef)
			checkCmd.Dir = wtPath
			if checkCmd.Run() == nil {
				remoteBranchExists = true
			}
		}

		if remoteBranchExists {
			// Remote branch still exists — ordinary merge topology.
			// Confirm the head SHA still matches, then prove the captured
			// head SHA is an ancestor of the base/default target using
			// git merge-base --is-ancestor.
			if status.HeadSHA == "" {
				return "", fmt.Errorf("provider returned empty head SHA for merged PR #%d (use --force to override)", ident.Number)
			}
			if ident.HeadSHA != "" && status.HeadSHA != ident.HeadSHA {
				return "", fmt.Errorf("PR head SHA mismatch: stored %s, provider reports %s; the worktree branch may have moved (use --force to override)", ident.HeadSHA, status.HeadSHA)
			}

			// Prove Git ancestry: the captured/verified head SHA must be
			// an ancestor of the base target. This distinguishes a true
			// ancestor (merged commit) from orphaned or force-pushed branches.
			baseRef := ident.BaseRef
			if baseRef != "" {
				ancestorCmd := exec.Command("git", "merge-base", "--is-ancestor", status.HeadSHA, "origin/"+baseRef)
				ancestorCmd.Dir = wtPath
				if err := ancestorCmd.Run(); err != nil {
					return "", fmt.Errorf("captured head SHA %s is not an ancestor of origin/%s: the merge is not proven in local git topology (use --force to override)", status.HeadSHA, baseRef)
				}
				proof += fmt.Sprintf("; ancestry verified: %s is ancestor of origin/%s", status.HeadSHA, baseRef)
			}
		}

		// PR is confirmed merged by the provider — accept as landed and
		// return the exact proof used.
		return proof, nil
	}

	return "", fmt.Errorf("PR #%d is in an unexpected state: merged=%v closed=%v (use --force to override)", ident.Number, status.Merged, status.Closed)
}

// checkRemoteBranch verifies the worktree branch has a remote tracking branch
// that is reachable. This is a fallback when no delivery identity is available.
func checkRemoteBranch(wtPath string) error {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	cmd.Dir = wtPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("branch has no remote tracking branch (use --force to override): %w", err)
	}

	cmd = exec.Command("git", "fetch", "--dry-run")
	cmd.Dir = wtPath
	fetchOut, fetchErr := cmd.CombinedOutput()
	if fetchErr != nil {
		return fmt.Errorf("cannot reach remote (use --force to override): %s", strings.TrimSpace(string(fetchOut)))
	}

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

// otherWorkspaceRefs scans all task meta files in homeDir for references to the given
// workspace ID, excluding the task with the given ID. Returns a list of task IDs that
// still reference the workspace. This prevents closing a workspace that another task is using.
func otherWorkspaceRefs(homeDir, excludeID, workspaceID string) []string {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}

	var refs []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		taskID := strings.TrimSuffix(entry.Name(), ".meta")
		if taskID == excludeID {
			continue
		}

		data, err := os.ReadFile(filepath.Join(stateDir, entry.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "herdr_workspace_id="+workspaceID) {
			refs = append(refs, taskID)
		}
	}
	return refs
}
