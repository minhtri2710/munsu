// Package teardown implements soldier teardown safety checks and lifecycle.
package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// RetirementCleanupPendingError is the typed partial outcome of a retirement
// whose authoritative Retire transition committed but whose saga-side cleanup
// did not complete (ADR-0007 §7): the retired phase and typed audit stand
// durably, cleanup is resumable, and a retry replays the durable receipt
// idempotently and resumes cleanup — never re-running merge/reconciliation.
// The committed merged truth is never rolled back or mutated by a cleanup
// failure.
type RetirementCleanupPendingError struct {
	TaskID     string
	Generation taskauthority.Generation
	Revision   taskauthority.Revision
	CleanupErr error
}

func (e *RetirementCleanupPendingError) Error() string {
	return fmt.Sprintf("retirement committed for %s (generation %s revision %d) but cleanup is pending: %v", e.TaskID, e.Generation, e.Revision, e.CleanupErr)
}

func (e *RetirementCleanupPendingError) Unwrap() error { return e.CleanupErr }

// taskRetireOperationID returns the stable Task Operation identity of the
// retirement transition for one task generation (ADR-0007 §6): retries replay
// the durable receipt idempotently instead of re-committing, and a reopened
// generation retires under its own identity.
func taskRetireOperationID(taskID string, generation taskauthority.Generation) string {
	return fmt.Sprintf("task-retire-%s-%s", taskID, generation)
}

// retireTaskAuthoritatively commits the retired phase transition through the
// composed canonical Task Authority (Task 7.7). It is exact-generation and
// idempotent: the request carries the expected Generation/Revision
// precondition read from the current aggregate, and the Operation identity is
// stable per task generation, so a retry after a committed receipt observes
// the already-retired generation and resumes cleanup without re-committing
// (a reopened generation retires under its own identity). The verified-
// delivery prerequisite is derived from the task meta's delivery identity: an
// identity-bearing task is only retired-eligible with provider-verified
// merged evidence in its delivery projection (the calling flow's safety
// checks verified the provider evidence and recorded delivery_state=merged
// via the merge flow or the merged-poll MarkMerged path); a task without a
// delivery identity retires under the baseline prerequisite (exact
// generation, not already retired). Production callers always supply the
// canonical Authority; nil fails closed.
func retireTaskAuthoritatively(opts Options, meta map[string]string, authority *taskauthority.Canonical) (taskauthority.Outcome, error) {
	if authority == nil {
		return taskauthority.Outcome{}, fmt.Errorf("retirement requires a composed task authority")
	}
	taskID, err := domain.NewTaskID(opts.ID)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("resolving task identity: %w", err)
	}
	agg, err := authority.Get(taskID)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("resolving task generation: %w", err)
	}
	// A retry after a committed receipt observes the retired generation: the
	// canonical receipt replays the original outcome, so the committed state
	// is reported (Replayed=true) and cleanup resumes without re-committing.
	if agg.Phase == taskauthority.PhaseRetired {
		return taskauthority.Outcome{TaskID: taskID, Generation: agg.Generation, Revision: agg.Revision, Phase: agg.Phase, Replayed: true}, nil
	}
	// An identity-bearing task is only retired-eligible with provider-verified
	// merged evidence in its delivery projection; otherwise the operation fails
	// closed with a typed precondition error (mirrors the deleted legacy
	// RequireVerifiedDelivery gate, now enforced at the caller against the
	// delivery_state projection).
	if ident, identErr := domain.IdentityFromMeta(meta); identErr == nil && ident != nil {
		if meta[domain.MetaDeliveryState] != string(domain.DeliveryStateMerged) {
			return taskauthority.Outcome{}, fmt.Errorf("task %s has no provider-verified merged evidence (delivery_state=%q); retirement requires delivery_state=merged", opts.ID, meta[domain.MetaDeliveryState])
		}
	}
	req := taskauthority.CanonicalRetireRequest{
		HomeID:       authority.HomeID(),
		TaskID:       taskID,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "retirement",
	}
	opID, err := domain.NewOperationID(taskRetireOperationID(opts.ID, agg.Generation))
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("retirement operation identity: %w", err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("retirement operation: %w", err)
	}
	return authority.Retire(op, req)
}

// Run fails closed because teardown requires a task-bound endpoint capability.
// Production callers must compose that capability through RunWithBackend.
func RetireTask(opts Options, backend BoundTeardown, journals RetirementJournalPort, authority *taskauthority.Canonical) (*TeardownResult, error) {
	result := &TeardownResult{}

	// Gate refusal: no-mistakes gate agents must not drive fleet lifecycle.
	if err := backend.RefuseGate(); err != nil {
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
		proofs, err := safetyCheck(opts, meta, kind, backend)
		if err != nil {
			return nil, fmt.Errorf("teardown %s: safety check failed: %w", opts.ID, err)
		}
		result.Proofs = append(result.Proofs, proofs...)
		for _, p := range proofs {
			result.Steps = append(result.Steps, "proof: "+p)
		}
	} else {
		steps, err := journals.PrepareForcedRetirementEvidence(opts.HomeDir, opts.ID)
		if err != nil {
			return nil, fmt.Errorf("teardown %s: preserving evidence: %w", opts.ID, err)
		}
		result.Steps = append(result.Steps, steps...)
	}

	// Step 0: Terminal uplink continuity check
	// Before any destructive teardown, ensure material terminal reports are
	// durably acknowledged. If the ReportRelay obligation is still open and
	// the task has material status (done/failed/blocked/needs-decision),
	// fail closed — the parent supervisor must receive confirmation before
	// local artifacts are removed. Use --force to override.
	if !opts.Force {
		if err := journals.VerifyRetirementContinuity(opts.HomeDir, opts.ID); err != nil {
			return nil, fmt.Errorf("teardown %s: %w", opts.ID, err)
		}
	}

	// The authoritative retirement transition commits via the composed
	// canonical Task Authority BEFORE saga-side cleanup (Task 7.7): the
	// durable receipt pins the retired phase + preserved retirement evidence,
	// and cleanup runs strictly after it and never re-runs
	// merge/reconciliation. A retry after a committed receipt observes the
	// retired generation (Replayed outcome) and resumes cleanup without
	// re-committing. An identity-bearing task is only retired-eligible with
	// provider-verified merged evidence in its delivery projection; otherwise
	// the operation fails closed with a typed precondition error. nil fails
	// closed.
	committed, err := retireTaskAuthoritatively(opts, meta, authority)
	if err != nil {
		return nil, fmt.Errorf("teardown %s: %w", opts.ID, err)
	}

	// cleanupPending wraps a saga-side cleanup failure after the durable
	// receipt: the retired phase and typed audit stand, cleanup is resumable
	// and never reruns merge/reconciliation, and the committed merged truth is
	// never rolled back or mutated.
	cleanupPending := func(stepErr error) (*TeardownResult, error) {
		return result, &RetirementCleanupPendingError{
			TaskID:     opts.ID,
			Generation: committed.Generation,
			Revision:   committed.Revision,
			CleanupErr: stepErr,
		}
	}

	// 1. Kill session window
	var wtPath string
	if windowID, ok := meta["window"]; ok && windowID != "" {
		status, err := backend.Probe(opts.HomeDir, meta)
		if err != nil {
			return cleanupPending(fmt.Errorf("teardown %s: verifying bound endpoint: %w", opts.ID, err))
		}
		if !status.Alive {
			result.Steps = append(result.Steps, fmt.Sprintf("session window %s already gone (still tearing down)", windowID))
		} else {
			request := DisposeRequest{Backend: meta["backend"], Handle: windowID, SessionOwner: meta["herdr_session"], WorkspaceID: meta["herdr_workspace_id"], TabID: meta["herdr_tab_id"], Home: opts.HomeDir, TaskID: opts.ID}
			if request.WorkspaceID != "" && len(otherWorkspaceRefs(opts.HomeDir, opts.ID, request.WorkspaceID)) > 0 {
				request.DenyWorkspaceClose = true
			}
			if err := backend.Dispose(opts.HomeDir, meta, request); err != nil {
				return cleanupPending(fmt.Errorf("teardown %s: disposing bound endpoint: %w", opts.ID, err))
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
			// Recheck launch artifacts immediately before destructive cleanup,
			// closing the mutation window between the initial safety check
			// and ReturnWorktree.
			if !opts.Force {
				expectedManifestSHA := meta["launch_manifest_sha256"]
				if err := VerifyLaunchArtifacts(wtPath, expectedManifestSHA); err != nil {
					return cleanupPending(fmt.Errorf("teardown %s: pre-return artifact verification failed: %w (use --force to override)", opts.ID, err))
				}
			}
			if err := backend.ReturnWorktree(opts.HomeDir, wtPath); err != nil {
				return cleanupPending(fmt.Errorf("teardown %s: worktree return failed: %w (lease still held)", opts.ID, err))
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
	// from appearing as the current reconciled state after
	// Appending to both the status file (before cleanup) and the typed event log
	// (for permanent durability) follows the current-state precedence pattern.
	journalSteps, err := journals.FinalizeRetirementJournals(opts.HomeDir, opts.ID)
	if err != nil {
		return cleanupPending(fmt.Errorf("teardown %s: finalizing journals: %w", opts.ID, err))
	}
	result.Steps = append(result.Steps, journalSteps...)

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

// safetyCheck verifies that work is landed before allowing
// Returns proof strings alongside any error. Proofs are only populated on success.
func safetyCheck(opts Options, meta map[string]string, kind string, backend BoundTeardown) ([]string, error) {
	switch kind {
	case "scout":
		if err := scoutSafetyCheck(opts, meta); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return shipSafetyCheck(opts, meta, backend)
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

	// After report exists, check for unresolved decision holds. The decision
	// hold lifecycle mirrors into the task status projection (a needs-decision
	// line until a resolved line is recorded), and teardown is not a held
	// dispatch action (holds gate handoff/start/spawn, ADR-0004 §7), so the
	// safety check reads the status projection — projection compatibility
	// remains permitted while the authoritative dispatch cutover proceeds.
	unresolvedKeys, err := unresolvedDecisionKeysFromStatus(opts.HomeDir, opts.ID)
	if err != nil {
		return fmt.Errorf("checking decision holds: %w", err)
	}
	if len(unresolvedKeys) > 0 {
		return fmt.Errorf("scout task %s has %d unresolved decision hold(s): %s (use --force to override)", opts.ID, len(unresolvedKeys), strings.Join(unresolvedKeys, ", "))
	}

	return nil
}

// unresolvedDecisionKeysFromStatus derives unresolved decision keys from the
// task status projection: a needs-decision line whose key has no resolved
// counterpart is still open.
func unresolvedDecisionKeysFromStatus(homeDir, taskID string) ([]string, error) {
	statusLines, err := home.ReadStatus(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("reading status for %s: %w", taskID, err)
	}
	resolved := map[string]bool{}
	needs := map[string]bool{}
	for _, line := range statusLines {
		_, key := home.ParseStatusKey(line)
		if key == "" {
			continue
		}
		if strings.HasPrefix(line, "resolved:") {
			resolved[key] = true
		}
		if strings.HasPrefix(line, "needs-decision:") {
			needs[key] = true
		}
	}
	var unresolved []string
	for key := range needs {
		if !resolved[key] {
			unresolved = append(unresolved, key)
		}
	}
	sort.Strings(unresolved)
	return unresolved, nil
}

// shipSafetyCheck verifies work is landed before
// It separates cleanliness checks (dirty worktree) from merge-proof checks
// (topology-aware PR merge verification using delivery identity).
// Returns proof strings emitted during merge-proof checks.
// shipSafetyCheck verifies work is landed before
// It separates cleanliness checks (dirty worktree) from merge-proof checks
// (topology-aware PR merge verification using delivery identity).
// Returns proof strings emitted during merge-proof checks.
func shipSafetyCheck(opts Options, meta map[string]string, backend BoundTeardown) ([]string, error) {
	wtPath, ok := meta["worktree"]
	if !ok || wtPath == "" {
		return nil, fmt.Errorf("no worktree path in meta for %s", opts.ID)
	}

	// --- Cleanliness checks (always run) ---

	// Check worktree exists.
	if _, err := os.Stat(wtPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("worktree %s does not exist", wtPath)
		}
		return nil, fmt.Errorf("checking worktree %s: %w", wtPath, err)
	}

	// Get expected manifest SHA-256 from task metadata (anchored outside worktree).
	expectedManifestSHA := meta["launch_manifest_sha256"]
	if expectedManifestSHA == "" || !sha256Regex.MatchString(expectedManifestSHA) {
		// Fallback: compute the manifest digest from the worktree when meta
		// does not provide it. This is a backward-compatibility path for
		// callers that have not yet persisted the anchor. In production,
		// the meta should always contain launch_manifest_sha256.
		manifestPath := filepath.Join(wtPath, ManifestName)
		if manifestBytes, readErr := os.ReadFile(manifestPath); readErr == nil {
			expectedManifestSHA = sha256Content(manifestBytes)
		}
	}

	// Verify launch artifacts using the manifest.
	if err := VerifyLaunchArtifacts(wtPath, expectedManifestSHA); err != nil {
		return nil, fmt.Errorf("worktree %s: launch artifact verification failed: %w (use --force to override)", wtPath, err)
	}

	// Check for unlisted dirt: files not in the manifest that are dirty.
	// Use --ignored=matching so ignored files (including manifest entries)
	// are also listed; we'll skip declared artifacts and legacy after their
	// verifier passes.
	cmd := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "-z")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("checking git status: %w", err)
	}

	// Build a set of manifest artifact paths for quick lookup.
	manifest, manifestErr := ReadManifest(wtPath)
	if manifestErr != nil {
		return nil, fmt.Errorf("reading manifest for unlisted check: %w", manifestErr)
	}
	manifestPaths := manifest.ArtifactPaths()

	var remaining []string
	if len(out) > 0 {
		// Parse -z separated porcelain lines.
		lines := strings.Split(string(out), "\x00")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			name := parsePorcelainFilename(line)
			if name == "" {
				continue
			}

			// Skip the manifest file itself (already verified by VerifyLaunchArtifacts).
			if name == ManifestName {
				continue
			}

			// Skip declared manifest entries (already verified by VerifyLaunchArtifacts).
			if manifestPaths[name] {
				continue
			}

			// Check legacy .soldier-md migration.
			if name == ".soldier-md" && manifest.LegacyBriefMigration != nil {
				if err := CheckLegacyBriefMigration(wtPath, manifest); err == nil {
					continue
				}
			}

			remaining = append(remaining, line)
		}
	}

	if len(remaining) > 0 {
		return nil, fmt.Errorf("worktree %s has uncommitted changes (use --force to override)\n  %s", wtPath, strings.Join(remaining, "\n  "))
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
		if err := domain.ValidateIdentity(ident); err != nil {
			return nil, fmt.Errorf("invalid delivery identity (fail-closed, no legacy fallback): %w", err)
		}

		proof, err := topologyAwareMergeCheck(opts, meta, wtPath, ident, backend)
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
// authoritative IdentityFromMeta, which correctly distinguishes:
//   - nil, nil     (truly no identity metadata — legacy fallback allowed)
//   - nil, error   (partial identity with missing pr_url — fail closed)
//   - identity, nil (valid identity — proceed to topology-aware check)
func identityFromMeta(meta map[string]string) (*domain.DeliveryIdentity, error) {
	return domain.IdentityFromMeta(meta)
}

// topologyAwareMergeCheck verifies the work is landed using the provider's
// confirmed PR/MR merge status. It supports three merge topologies:
//   - Squash/rebase merge with deleted head: accepts provider-confirmed merged PR identity
//   - Ordinary merge: retains ancestry proof (merged branch still exists locally)
//   - Unknown/unverifiable: refuses teardown
//
// Returns the proof string on success.
func topologyAwareMergeCheck(opts Options, meta map[string]string, wtPath string, ident *domain.DeliveryIdentity, backend BoundTeardown) (string, error) {
	// Check lifecycle state if set: require merged state for landed delivery.
	if ds := meta[domain.MetaDeliveryState]; ds != "" && ds != string(domain.DeliveryStateMerged) {
		return "", fmt.Errorf("delivery lifecycle is in state %q, expected %q (use --force to override)", ds, domain.DeliveryStateMerged)
	}

	// Query the provider for the current PR/MR merge status using the
	// provider-neutral seam that routes by identity provider.
	status, err := backend.QueryMergeStatus(ident)
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
