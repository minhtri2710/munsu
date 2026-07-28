// Package teardown implements soldier teardown safety checks and lifecycle.
package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/soldier"
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

// Run fails closed because teardown requires a task-bound endpoint capability.
// Production callers must compose that capability through RunWithBackend.
func Run(Options) (*TeardownResult, error) {
	return nil, fmt.Errorf("teardown endpoint capability is required; use RunWithBackend")
}
func RunWithBackend(opts Options, backend BoundTeardown) (*TeardownResult, error) {
	result := &TeardownResult{}

	// Gate refusal: no-mistakes gate agents must not drive fleet lifecycle.
	if err := fleet.GateRefuseFromCWD(); err != nil {
		return nil, fmt.Errorf("teardown refused: %w", err)
	}

	// Read task meta
	meta, err := home.ReadMeta(opts.HomeDir, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("teardown %s: reading meta: %w", opts.ID, err)
	}

	kind := meta["kind"]
	if kind == "" {
		kind = "ship" // default
	}

	if !opts.Force {
		proofs, err := safetyCheck(opts, meta, kind)
		if err != nil {
			return nil, fmt.Errorf("teardown %s: safety check failed: %w", opts.ID, err)
		}
		result.Proofs = append(result.Proofs, proofs...)
		for _, p := range proofs {
			result.Steps = append(result.Steps, "proof: "+p)
		}
	} else {
		// --force: preserve evidence before destructive cleanup
		// Copy status file and any terminal receipts to a .backup/ dir
		// so unacked terminal evidence is not silently erased.
		backupDir := filepath.Join(opts.HomeDir, "state", ".backup", opts.ID)
		os.MkdirAll(backupDir, 0755)
		stateDir := filepath.Join(opts.HomeDir, "state")
		// Copy status file
		if src, err := os.ReadFile(filepath.Join(stateDir, opts.ID+".status")); err == nil {
			os.WriteFile(filepath.Join(backupDir, opts.ID+".status"), src, 0644)
		}
		// Copy receipt files
		receiptsDir := orchestrator.ReceiptDir(opts.HomeDir)
		if entries, err := os.ReadDir(receiptsDir); err == nil {
			prefix := opts.ID + "."
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), prefix) {
					if src, err := os.ReadFile(filepath.Join(receiptsDir, e.Name())); err == nil {
						os.WriteFile(filepath.Join(backupDir, e.Name()), src, 0644)
					}
				}
			}
		}
		result.Steps = append(result.Steps, "evidence preserved to state/.backup/"+opts.ID+" (--force)")
	}

	// Step 0: Terminal uplink continuity check
	// Before any destructive teardown, ensure material terminal reports are
	// durably acknowledged. If the ReportRelay obligation is still open and
	// the task has material status (done/failed/blocked/needs-decision),
	// fail closed — the parent supervisor must receive confirmation before
	// local artifacts are removed. Use --force to override.
	if !opts.Force {
		if err := uplinkCheck(opts); err != nil {
			return nil, fmt.Errorf("teardown %s: %w", opts.ID, err)
		}
	}

	// 1. Kill session window
	var wtPath string
	if windowID, ok := meta["window"]; ok && windowID != "" {
		status, err := backend.Probe(opts.HomeDir, meta)
		if err != nil {
			return nil, fmt.Errorf("teardown %s: verifying bound endpoint: %w", opts.ID, err)
		}
		if !status.Alive {
			result.Steps = append(result.Steps, fmt.Sprintf("session window %s already gone (still tearing down)", windowID))
		} else {
			request := DisposeRequest{Backend: meta["backend"], Handle: windowID, SessionOwner: meta["herdr_session"], WorkspaceID: meta["herdr_workspace_id"], TabID: meta["herdr_tab_id"], Home: opts.HomeDir, TaskID: opts.ID}
			if request.WorkspaceID != "" && len(otherWorkspaceRefs(opts.HomeDir, opts.ID, request.WorkspaceID)) > 0 {
				request.DenyWorkspaceClose = true
			}
			if err := backend.Dispose(opts.HomeDir, meta, request); err != nil {
				return nil, fmt.Errorf("teardown %s: disposing bound endpoint: %w", opts.ID, err)
			}
			result.Steps = append(result.Steps, fmt.Sprintf("session window %s killed", windowID))
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
	//    so the lease is not falsely claimed as released.
	if wtPath != "" {
		if fi, err := os.Stat(wtPath); err == nil && fi.IsDir() {
			if err := backend.ReturnWorktree(opts.HomeDir, wtPath); err != nil {
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

	// 3.5. Terminal event: close any open keyed phases before removing the status file.
	// This ensures the append-only log has proper terminal events for each
	// open keyed phase (working/paused), preventing stale working/blocked status
	// from appearing as the current reconciled state after teardown.
	// Appending to both the status file (before cleanup) and the typed event log
	// (for permanent durability) follows the current-state precedence pattern.
	closeTerminalPhases(opts, result)

	// 4. Remove residual state artifacts
	stateDir := filepath.Join(opts.HomeDir, "state")
	munsuArtifacts := []string{
		// Munsu-native: canonical names (item-5 rename)
		opts.ID + ".status",
		opts.ID + ".check",   // new canonical name
		opts.ID + ".turnend", // new canonical name
		// Legacy names (dual-read, remove next release)
		opts.ID + ".check.sh",   // legacy name (deprecated)
		opts.ID + ".turn-ended", // legacy name (deprecated)
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

	// 4.5. Clear per-task obligation records for this task
	// Uses per-task path instead of global per-role file.
	if err := orchestrator.ClearTaskCompleted(opts.HomeDir, opts.ID); err != nil {
		result.Steps = append(result.Steps, fmt.Sprintf("clear task obligations: %v", err))
	} else {
		result.Steps = append(result.Steps, "task obligations cleared")
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

	// Check worktree is not dirty (known launch artifacts are always allowed)
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("checking git status: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if raw != "" {
		// Filter out known munsu-owned launch artifacts (e.g. .soldier-charter.md,
		// .soldier-envelope.json, .soldier-prompt.md, .soldier-brief.md,
		// .soldier-launch.sh). These are lifecycle-owned and cleanable during
		// normal teardown. Any other untracked/modified files still fail.
		lines := strings.Split(raw, "\n")
		allowlist := make(map[string]bool)
		for _, name := range soldier.LaunchArtifactNames() {
			allowlist[name] = true
		}
		var remaining []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// porcelain format: XY filename or XY "filename with spaces"
			// Skip the two status chars and optional space, then parse filename.
			name := parsePorcelainFilename(line)
			if name != "" && allowlist[name] {
				continue
			}
			remaining = append(remaining, line)
		}
		if len(remaining) > 0 {
			return nil, fmt.Errorf("worktree %s has uncommitted changes (use --force to override)\n  %s", wtPath, strings.Join(remaining, "\n  "))
		}
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

// identityFromMeta reconstructs a delivery identity from task meta using the
// authoritative delivery.IdentityFromMeta, which correctly distinguishes:
//   - nil, nil     (truly no identity metadata — legacy fallback allowed)
//   - nil, error   (partial identity with missing pr_url — fail closed)
//   - identity, nil (valid identity — proceed to topology-aware check)
func identityFromMeta(meta map[string]string) (*delivery.DeliveryIdentity, error) {
	return delivery.IdentityFromMeta(meta)
}

// topologyAwareMergeCheck verifies the work is landed using the provider's
// confirmed PR/MR merge status. It supports three merge topologies:
//   - Squash/rebase merge with deleted head: accepts provider-confirmed merged PR identity
//   - Ordinary merge: retains ancestry proof (merged branch still exists locally)
//   - Unknown/unverifiable: refuses teardown
//
// Returns the proof string on success.
func topologyAwareMergeCheck(opts Options, meta map[string]string, wtPath string, ident *delivery.DeliveryIdentity) (string, error) {
	// Check lifecycle state if set: require merged state for landed delivery.
	if ds := meta[delivery.MetaDeliveryState]; ds != "" && ds != string(delivery.DeliveryStateMerged) {
		return "", fmt.Errorf("delivery lifecycle is in state %q, expected %q (use --force to override)", ds, delivery.DeliveryStateMerged)
	}

	// Query the provider for the current PR/MR merge status using the
	// provider-neutral seam that routes by identity provider.
	status, err := delivery.QueryDeliveryMergeStatus(ident)
	if err != nil {
		return "", fmt.Errorf("cannot verify merge status: %w (use --force to override)", err)
	}

	// Check for refused states
	if status.Closed && !status.Merged {
		return "", fmt.Errorf("PR #%d is closed but not merged (use --force to override)", ident.Number)
	}

	if !status.Merged && status.State == "OPEN" {
		return "", fmt.Errorf("PR #%d is still open and not merged (use --force to override)", ident.Number)
	}

	// PR is confirmed merged. Verify head SHA consistency for ALL merged
	// topologies, including deleted remote head (squash/rebase).
	// The live provider HeadSHA must be nonempty and exactly equal to the
	// stored ident.HeadSHA before accepting. A wrong head must fail even
	// when the remote head has been deleted.
	// Require non-empty MergedSHA as merge-result evidence.
	if status.Merged {
		if status.HeadSHA == "" {
			return "", fmt.Errorf("provider returned empty head SHA for merged PR #%d (use --force to override)", ident.Number)
		}
		if status.MergedSHA == "" {
			return "", fmt.Errorf("provider returned no merge-result evidence for merged PR #%d (use --force to override)", ident.Number)
		}
		if ident.HeadSHA != "" && status.HeadSHA != ident.HeadSHA {
			return "", fmt.Errorf("PR head SHA mismatch: stored %s, provider reports %s; the worktree branch may have moved (use --force to override)", ident.HeadSHA, status.HeadSHA)
		}

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
			baseRef := ident.BaseRef
			if baseRef != "" {
				// For squash/rebase merges, MergedSHA (merge commit on target)
				// is an ancestor of origin/main, but HeadSHA is not. Prefer
				// MergedSHA when it differs from HeadSHA.
				ancestrySHA := status.HeadSHA
				if status.MergedSHA != "" && status.MergedSHA != status.HeadSHA {
					ancestrySHA = status.MergedSHA
				}
				ancestorCmd := exec.Command("git", "merge-base", "--is-ancestor", ancestrySHA, "origin/"+baseRef)
				ancestorCmd.Dir = wtPath
				if err := ancestorCmd.Run(); err != nil {
					// Ancestry check is an additional verification, not a blocker.
					// Provider-confirmed MERGED + head SHA match is sufficient.
					proof += fmt.Sprintf("; ancestry check inconclusive: %s is not ancestor of origin/%s", ancestrySHA, baseRef)
				} else {
					proof += fmt.Sprintf("; ancestry verified: %s is ancestor of origin/%s", ancestrySHA, baseRef)
				}
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

// parsePorcelainFilename extracts the filename from a git status --porcelain line.
// Porcelain format: XY FILENAME, where X is the staging status and Y is the
// worktree status. For filenames with spaces, git quotes them: XY "filename".
func parsePorcelainFilename(line string) string {
	if len(line) < 4 {
		return ""
	}
	// Skip the two status characters and the separator space.
	rest := strings.TrimSpace(line[2:])
	if rest == "" {
		return ""
	}
	// Handle quoted filenames (spaces or special chars).
	if strings.HasPrefix(rest, "\"") {
		unquoted, err := strconv.Unquote(rest)
		if err != nil {
			return rest
		}
		return unquoted
	}
	return rest
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

// closeTerminalPhases reads the status file for the task being torn down,
// finds any open keyed phases (working/paused), and appends a "resolved"
// terminal event for each open phase. This prevents stale working/blocked
// status from remaining as the current reconciled state after teardown.
// It writes to both the status file (before cleanup) and the typed event
// log (for permanent durability). Idempotent: already-closed keys are not
// returned by OpenActivities, so repeating the same resolved key is safe.
func closeTerminalPhases(opts Options, result *TeardownResult) {
	statusPath := filepath.Join(opts.HomeDir, "state", opts.ID+".status")
	openActs := classify.OpenActivities(statusPath)
	if len(openActs) == 0 {
		return
	}
	for _, act := range openActs {
		closeLine := fmt.Sprintf("resolved [key=%s]: soldier torn down", act.Key)
		// Append to the status file before cleanup so any concurrent reader
		// sees the proper close event (even though teardown removes it shortly).
		if err := home.AppendStatus(opts.HomeDir, opts.ID, closeLine); err != nil {
			result.Steps = append(result.Steps, fmt.Sprintf("warning: close phase %s: %v", act.Key, err))
			continue
		}
		result.Steps = append(result.Steps, fmt.Sprintf("closed keyed phase [key=%s]", act.Key))

		// Also write to the typed event log for permanent durability beyond teardown.
		syntheticID := orchestrator.SyntheticEventID()
		if err := orchestrator.AppendWithID(opts.HomeDir, syntheticID, "task.status", opts.ID, act.Key, closeLine); err != nil {
			result.Steps = append(result.Steps, fmt.Sprintf("warning: event log: %v", err))
		}
	}
}

// uplinkCheck verifies terminal uplink continuity before teardown removes
// status/meta artifacts. It ensures material soldier reports are durably
// acknowledged by the parent supervisor before local cleanup proceeds.
//
// Uses per-task obligations (state/.obligations/<taskID>.obligations) to check
// the exact task+key, not a global per-role flag.
//
// If the task has material status (done/failed/blocked/needs-decision) AND the
// ReportRelay obligation is still open, the check fails closed. This prevents
// teardown from removing evidence that the parent supervisor has not yet seen.
//
// The check is idempotent after ReportRelay is completed. Use --force to bypass.
func uplinkCheck(opts Options) error {
	if orchestrator.HasPendingReport(opts.HomeDir, opts.ID) || orchestrator.HasAnyOpenReport(opts.HomeDir, opts.ID) {
		return fmt.Errorf("uplink report not acknowledged: Processing Ack is still pending for task %s (use --force to override)", opts.ID)
	}

	// Legacy read compatibility: check the former ReportRelay obligation.
	// Check if per-task ReportRelay obligation is still open.
	// Per-task obligations are bound to exact taskID+terminalKey, not global role.
	open, err := orchestrator.IsTaskReportRelayOpen(opts.HomeDir, opts.ID)
	if err != nil {
		return fmt.Errorf("reading task obligations: %w", err)
	}
	if !open {
		return nil
	}

	// ReportRelay is open. Check if the task has material status.
	// FAILS CLOSED: MaterialReportExists returns error for unreadable status.
	hasMaterial, err := orchestrator.MaterialReportExists(opts.HomeDir, opts.ID)
	if err != nil {
		return fmt.Errorf("checking material report (fail-closed): %w", err)
	}
	if !hasMaterial {
		return nil
	}

	// Material report exists AND ReportRelay is still open — fail closed.
	return fmt.Errorf("terminal report-relay not acknowledged: material status exists but ReportRelay obligation is still open for task %s (use --force to override, or have captain relay this task)", opts.ID)
}
