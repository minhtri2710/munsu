// Package captain manages persistent domain supervisors (captains).
package fleet

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// ProvenanceMarkerName is the marker file written to a seeded captain home root.
const ProvenanceMarkerName = home.CaptainProvenanceMarkerName

// CaptainCharterVersion is the current version identifier embedded in every generated
// .captain-charter.md file so agents and operators can verify the charter revision.
const CaptainCharterVersion = "captain-charter-v1"

// ProvenanceVersion is the current provenance marker format version.
const ProvenanceVersion = home.CaptainProvenanceVersion

// ConvergeLockName is the converge-specific lock file under parent state.
const ConvergeLockName = ".captain-converge.lock"

// NudgePendingDir is the directory under parent state for pending nudge markers.
const NudgePendingDir = ".captain-nudge-pending"

// UpdateOutcome is the typed result of a single captain update operation.
type UpdateOutcome string

const (
	AlreadyCurrent    UpdateOutcome = "already-current"
	FastForwarded     UpdateOutcome = "fast-forwarded"
	StateOnlySkipped  UpdateOutcome = "state-only-skipped"
	Dirty             UpdateOutcome = "dirty"
	Diverged          UpdateOutcome = "diverged"
	Offline           UpdateOutcome = "offline"
	WrongRemote       UpdateOutcome = "wrong-remote"
	WrongBranch       UpdateOutcome = "wrong-branch"
	InvalidProvenance UpdateOutcome = "invalid-provenance"
)

// UpdateResponse carries the typed result of a captain update.
type UpdateResponse struct {
	Outcome UpdateOutcome
	Before  string
	After   string
	Err     error
}

// IsFailure returns true when the outcome is a failure state.
func (u UpdateOutcome) IsFailure() bool {
	switch u {
	case AlreadyCurrent, FastForwarded, StateOnlySkipped:
		return false
	default:
		return true
	}
}

// String returns a human-readable label for the outcome.
func (u UpdateOutcome) String() string {
	return string(u)
}

// outcomeFromFFReason maps a SafeFFReason to the corresponding UpdateOutcome.
func outcomeFromFFReason(reason SafeFFReason, err error) UpdateOutcome {
	if err != nil {
		switch reason {
		case SafeFFOffBranch:
			return WrongBranch
		case SafeFFMissingOrigin:
			return Offline
		case SafeFFChangesTracked:
			return Dirty
		default:
			return outcomeFromFFError(err)
		}
	}
	switch reason {
	case SafeFFAlreadyCurrent:
		return AlreadyCurrent
	case SafeFFSuccess:
		return FastForwarded
	default:
		return Diverged
	}
}

// outcomeFromFFError maps safeFF error strings to typed outcomes as fallback.
func outcomeFromFFError(err error) UpdateOutcome {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "tracked changes"),
		strings.Contains(msg, "unignored untracked"):
		return Dirty
	case strings.Contains(msg, "remote origin"),
		strings.Contains(msg, "origin/HEAD"),
		strings.Contains(msg, "does not exist locally"):
		return Offline
	case strings.Contains(msg, "remote %q differs"):
		return WrongRemote
	case strings.Contains(msg, "expected"):
		return WrongBranch
	case strings.Contains(msg, "not an ancestor"),
		strings.Contains(msg, "merge --ff-only failed"):
		return Diverged
	default:
		return Diverged
	}
}

func ensureCaptainIntegration(captainHome string, integration IntegrationPort) error {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("refusing pi extensions on unmarked home %s: %w", captainHome, err)
	}
	if integration == nil {
		return fmt.Errorf("captain integration capability is required")
	}
	return integration.EnsureCaptain(captainHome)
}

type CaptainSeedOptions struct {
	ID, Home, ParentHome, Charter string
	Integration                   IntegrationPort
}

type Info struct {
	ID      string
	Home    string
	Scope   string
	Project string
	Added   string
}

// CaptainIDFromTask resolves the registry/sm id for a captain task.
// Prefers meta sm_id; falls back to stripping the captain: task-id prefix.
func CaptainIDFromTask(taskID string, meta map[string]string) string {
	if meta != nil {
		if id := strings.TrimSpace(meta["sm_id"]); id != "" {
			return id
		}
	}
	return strings.TrimPrefix(taskID, "captain:")
}

// --- Injectable seams for testing ---

var captainLookPath = exec.LookPath

// proveSleep is the pause between post-launch liveness probes in the
// package-level Recover/Converge paths; overridden in tests.
var proveSleep = time.Sleep

// convergeLockAcquire acquires the converge lock exclusively.
// Override in tests to avoid fd leaks.
var convergeLockAcquire = func(parentHome string) (func(), error) {
	return acquireExclusiveLock(filepath.Join(parentHome, "state", ConvergeLockName))
}

// gitRun is the git command runner. Override in tests.
var gitRun = func(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// launchCmd builds a shell-safe command string for sending via session backend.
// Override in tests.
var launchCmd = func(binPath string, args []string, captainHome string, parentHome string) (string, error) {
	return buildLaunchScript(binPath, args, captainHome, parentHome)
}

// --- Helpers ---

// shQuote wraps s in single quotes, escaping any embedded single quotes
// for safe shell evaluation.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// buildLaunchScript writes a bash launch script into the general home and
// returns a fish-safe command that runs it. Herdr panes may use fish, so the
// bash-only identity/env plumbing must not be typed directly into the pane.
func buildLaunchScript(binPath string, args []string, cwd string, parentHome string) (string, error) {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd ")
	b.WriteString(shQuote(cwd))
	b.WriteString("\n")
	b.WriteString("export MUNSU_HOME=")
	b.WriteString(shQuote(cwd))
	b.WriteString("\n")
	b.WriteString("export MUNSU_ROLE=captain\n")
	// Identity and parent routing for rank-aware uplink reporting.
	taskID := "captain:" + filepath.Base(cwd)
	b.WriteString("export MUNSU_TASK_ID=")
	b.WriteString(shQuote(taskID))
	b.WriteString("\n")
	b.WriteString("export MUNSU_PARENT_STATUS=")
	b.WriteString(shQuote(parentHome))
	b.WriteString("\n")
	b.WriteString("exec ")
	b.WriteString(shQuote(binPath))
	for _, arg := range args {
		b.WriteString(" ")
		b.WriteString(shQuote(arg))
	}
	b.WriteString("\n")
	scriptPath := filepath.Join(cwd, ".captain-launch.sh")
	if err := os.WriteFile(scriptPath, []byte(b.String()), 0755); err != nil {
		return "", fmt.Errorf("writing captain launch script: %w", err)
	}
	return "bash " + shQuote(scriptPath), nil
}

// sha256Content returns the hex SHA-256 digest of data.
func captainSHA256Content(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// taskIDForCaptain returns the task ID used in state metadata for a captain.
func taskIDForCaptain(smID string) string {
	return "captain:" + smID
}

// checkStaleLegacyRecords is a read-only fail-closed guard that checks for
// stale legacy command transport records that were never migrated.
// The legacy .captain-send-outbox and .command-envelope transport is removed;
// this function detects any remaining records and returns an actionable error
// with exact paths. It never writes, migrates, marks, or deletes anything.
func checkStaleLegacyRecords(parentHome, captainID string) error {
	// Check .captain-send-outbox directory (legacy outbox).
	outboxDir := filepath.Join(parentHome, "state", ".captain-send-outbox", captainID)
	entries, err := os.ReadDir(outboxDir)
	if err == nil {
		var stale []string
		for _, e := range entries {
			if !e.IsDir() {
				stale = append(stale, filepath.Join(outboxDir, e.Name()))
			}
		}
		if len(stale) > 0 {
			paths := strings.Join(stale, "\n  ")
			return fmt.Errorf("stale legacy .captain-send-outbox records found:\n  %s\nUpgrade: run the last migration-capable release to migrate these records, then retry.", paths)
		}
	}

	// Check .command-envelope directory (legacy envelopes).
	envDir := filepath.Join(parentHome, "state", ".command-envelope")
	envEntries, err := os.ReadDir(envDir)
	if err == nil {
		var stale []string
		for _, e := range envEntries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				stale = append(stale, filepath.Join(envDir, e.Name()))
			}
		}
		if len(stale) > 0 {
			paths := strings.Join(stale, "\n  ")
			return fmt.Errorf("stale legacy .command-envelope records found:\n  %s\nUpgrade: run the last migration-capable release to migrate these records, then retry.", paths)
		}
	}

	return nil
}

// --- Seed / Provenance ---

// DefaultCaptainCharter returns the versioned, runtime-owned Captain charter.
// It covers the full captain-charter-contract: domain, General authority, command envelope,
// task authority, soldier lifecycle, downlink/uplink discipline, one-hop relay,
// delivery/merge with mode-specific behavior, AXI-first fail-closed, persistence/recovery,
// watcher/AFK safety, forbidden actions, and concise command recipes.
// parentHome must be the General home whose state/captain:<id>.status is the escalation file.
func DefaultCaptainCharter(id, parentHome string) (string, error) {
	statusFile, err := home.StatusFilePath(parentHome, taskIDForCaptain(id))
	if err != nil {
		return "", err
	}
	bt := "`"
	return fmt.Sprintf(`# Captain Charter: %[1]s

**Version: %[2]s**

## Domain

You are a persistent domain Captain under the General fleet hierarchy:
General → Captain → Soldier.

This home is yours. Operate only on work the General routes to you.
Never invent surveys, audits, or self-directed "find work" tasks.
An empty queue is healthy.

## General Authority

The General (parent orchestrator) has full authority over your lifecycle:
- Seeds, launches, retires, and reconciles you.
- Sets your config (model, harness, inheritable settings).
- Routes tasks to you via handoff.
- You operate within the domain assigned at seed time.

## Startup Bootstrap

The %[4]s section is runtime control context already loaded into the system prompt, not a human request or General-routed work. Rely on the runtime integration for session initialization, then wait. Do not read charter or mailbox files directly.

## Requests from the General

Incoming pane text may be:
1. Marked with a leading %[3]s followed by an invisible separator — a General-routed request.
2. Unmarked — the human typing directly into your pane (stay conversational).

When a message carries the General marker:
- If the text is a canonical NotificationRef JSON (%[6]s{\"message_id\":\"...\",\"sender_identity\":\"...\"}%[6]s), the General sent a durable mailbox envelope.
  Receive the envelope (validates/loads payload, no ack):
  %[6]smunsu inbox receive '<json>'%[6]s
  After accepting the command into context, ack:
  %[6]smunsu inbox ack '<json>'%[6]s
- If the text is a plain command, do the work.
- Answer via %[6]smunsu report%[6]s (see Uplink below), never chat-only.
- Terse result: one status line is the whole answer.
- Detailed result: write a doc under this home's data/ and append a status line that points to it.

## Command Envelope (General → Captain)

The General sends commands via durable mailbox envelopes:
- Each envelope is written to your home's state/.inbox/<sender-identity>/<id>.json
- A NotificationRef (%[6]s{\"message_id\":\"<id>\",\"sender_identity\":\"<sender>\"}%[6]s) is submitted to your pane.
- Notification acknowledgment means accepted only; the pending record on the General
  side persists until you write the exact ProcessingAck.
- 1. Receive: %[6]smunsu inbox receive '<ref>'%[6]s
		2. Ack after context: %[6]smunsu inbox ack '<ref>'%[6]s
- The envelope payload carries the %[3]s marker — answer via %[6]smunsu report%[6]s.
- Deduplication: processing the same ref again returns the existing ack (idempotent).

## Downlink: Captain → Soldier

Use %[6]smunsu send%[6]s to relay commands to Soldiers:
- %[6]smunsu send <soldier-id> <message>%[6]s
- NEVER use raw Herdr/tmux pane control to inject text into soldier panes.
- %[6]smunsu send%[6]s provides durability, idempotency, and audit.
- If %[6]smunsu send%[6]s is unavailable, wait — no raw fallback.

## Task Authority

The canonical Task Authority is the authoritative task source:
- Use %[6]smunsu task list%[6]s to list tasks; queued (unblocked) items are ready.
- Only ready items may be started. Never start a blocked or in-flight item.
- Dependencies are resolved by the Task Authority: blocked tasks are withheld until unblocked.
- De-duplication is enforced by the Task Authority (one record per task ID) — never edit task state files directly.
- When a task completes, update its state via %[6]smunsu task done <id>%[6]s.

## Soldier Lifecycle

Spawn Soldiers to do work from this home. The dispatch ordering is:
  %[6]smunsu task list%[6]s → %[6]smunsu task start <id>%[6]s → %[6]smunsu brief <id> <project>%[6]s → %[6]smunsu spawn <id> [<project>] --kind <kind> --mode <mode>%[6]s
- kind: ship (default) | scout — mode: no-mistakes | direct-PR | local-only (empty = auto-detect)
- After spawning, monitor soldier progress through their task state.
- When a soldier completes, receive and ack its Uplink Report, then report the domain result to General (see One-Hop Uplink Report).
- If a soldier is stuck, use the ladder: %[6]smunsu peek <id>%[6]s → %[6]smunsu send <id> ...%[6]s → interrupt → relaunch → fail.
- After a ship PR is merged, run %[6]smunsu teardown <soldier-id>%[6]s.
- Never launch another Captain.

## Uplink (Captain → General)

- Uplink only through %[6]smunsu report%[6]s — the PRIMARY status path.
- Usage: %[6]smunsu report <state> "<msg>" [--key <slug>]%[6]s
- States: working, needs-decision, blocked, paused, done, failed, resolved.
- Material phases get [key=<slug>] so later done/failed/resolved supersede them.
- Material Uplink Reports write a durable envelope, sender pending evidence, and a receiver wake before sending a NotificationRef.
- General explicitly runs inbox receive, accepts the report into context, then runs inbox ack to write the Processing Ack.
- NEVER poll the General for work. NEVER sleep-loop.
- %[6]smunsu peek%[6]s is only for stuck recovery (see Soldier Lifecycle).
- Provider polling only after terminal PR notification — no early polling.
- Fallback (only when %[6]smunsu report%[6]s is unavailable):
    echo "{state}: {one short line}" >> %[5]s

## One-Hop Uplink Report

Material reports (%[6]sdone%[6]s, %[6]sfailed%[6]s, %[6]sblocked%[6]s, %[6]sneeds-decision%[6]s) use the mailbox-only Uplink Report flow:

1. %[6]smunsu report%[6]s writes an immutable envelope to the parent inbox, sender pending evidence, and a parent wake before attempting live notification.
2. The pane receives only a NotificationRef. Read it with %[6]smunsu inbox receive '<ref>'%[6]s.
3. After the report is accepted into context, write the exact Processing Ack with %[6]smunsu inbox ack '<ref>'%[6]s.
4. A failed or busy live notification remains queued. Recovery retries once after watcher restart and otherwise after 60 seconds.
5. For the same task and key, the latest material report supersedes older unacknowledged reports. Different keys remain independent.
6. Teardown is allowed only after the parent Processing Ack has been reconciled into durable accepted evidence. Prompt submission alone is not an Ack.
7. **Stop hooks**: If General sends a stop with a matching task key, stop the soldier immediately — do not wait for completion.

## Delivery / Merge Authorization

When a Soldier opens a PR:
- The General authorizes merges. You do not merge without authorization.
- When authorized: %[6]smunsu delivery pr-merge <id> <url> [--teardown]%[6]s
- After merge, run %[6]smunsu teardown <soldier-id>%[6]s.
- Decision holds: if you need a decision from General, report %[6]sneeds-decision%[6]s and wait.
- Never use bare %[6]sgh pr merge%[6]s without %[6]smunsu delivery%[6]s when meta lives here.
- The selected delivery mode (direct-PR, no-mistakes, local-only) is authoritative:
  - **no-mistakes**: Automated code review, tests, lint, docs, push, PR, CI — all gates must pass. Failure fails closed.
  - **direct-PR**: Commit, push, and open PR directly (no automated gate pipeline).
  - **local-only**: Changes stay local (no push, no PR).

## AXI-First / Fail-Closed

All commands must use AXI-compliant CLIs:
- Prefer the AXI variant: %[6]sgh-axi%[6]s, etc.
- If an AXI tool fails, report the failure — never fall back to unsafe raw commands.
- Fail-closed: when uncertain, don't guess. Report up with what you know.

## Persistence / Recovery / Update / Migration

- Task state is durable in state/ and data/.
- The General runs converge cycles to keep your home synchronized.
- If your pane dies, the General relaunches you with the canonical system context.
- On update (fast-forward), the General refreshes the runtime-owned charter.
- The General handles migration (state-only → managed worktree); you don't need to act.

## Watcher / AFK Safety

This Captain home has its own watcher and AFK-injection safety rules:
- The **watcher** monitors soldier processes outside the agent. It is started/stopped by the General via converge; you interact through normal reporting channels.
- **AFK injection safety**: The AFK daemon guards injection targets to prevent unwanted file mutations in soldier homes.
- Stale-beat and repeated-wake detection are local to this home and managed by converge cycles.

## Forbidden Actions

You MUST NOT:
- Launch another Captain (only Soldiers).
- Use bare %[6]sgh pr merge%[6]s (use %[6]smunsu delivery%[6]s).
- Write to the parent General's status file outside of %[6]smunsu report%[6]s.
- Poll, sleep-loop, or self-initiate work (an empty queue is healthy).
- Modify tracked AGENTS.md (user-owned); the canonical charter lives in .captain-charter.md.
- Use raw Herdr/tmux commands for General communication.
- Merge PRs without General authorization.
- Parse or mutate task state files directly; task lifecycle runs through %[6]smunsu task%[6]s commands.

## Concise Command Recipes

| Action | Command |
|--------|---------|
| Report state | %[6]smunsu report <state> "<msg>" [--key <slug>]%[6]s |
| Brief soldier | %[6]smunsu brief <id> <project>%[6]s |
| Spawn soldier | %[6]smunsu spawn <id> [<project>] --kind <kind> --mode <mode>%[6]s |
| Teardown soldier | %[6]smunsu teardown <id>%[6]s |
| Send to soldier | %[6]smunsu send <id> <message>%[6]s |
| Merge PR | %[6]smunsu delivery pr-merge <id> <url> [--teardown]%[6]s |
| Stuck soldier | %[6]smunsu peek <id>%[6]s → %[6]smunsu send <id> ...%[6]s → ... |
| View tasks | %[6]smunsu task list%[6]s |
| Start task | %[6]smunsu task start <id>%[6]s |
| Complete task | %[6]smunsu task done <id>%[6]s |

`, id, CaptainCharterVersion, home.FromGeneralLabel, "[mu-system:captain-bootstrap]", shQuote(statusFile), bt), nil
}

// writeCaptainCharter writes the charter to .captain-charter.md (runtime-owned, untracked).
// Never writes to tracked AGENTS.md (user-owned). Idempotent: safe to call on every
// setup, reconcile, and recovery path.
func writeCaptainCharter(homePath, charter string) error {
	charterPath := filepath.Join(homePath, CaptainCharterName)
	if err := os.WriteFile(charterPath, []byte(charter), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", CaptainCharterName, err)
	}
	return nil
}

// ensureParentTypedConfig creates a minimal fleet base document and registers
// the default project and captain in the canonical Fleet Registry. This allows
// SeedCaptain and configPush to work without requiring the operator to set up
// typed config first.
func ensureParentTypedConfig(parentHome, captainHome, captainID string) error {
	// Check if fleet base already exists.
	if _, err := os.Stat(filepath.Join(parentHome, config.BaseDocumentPath)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	// Create fleet base document.
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: "pi",
		},
	}
	if err := config.StoreFleetBase(parentHome, base); err != nil {
		return fmt.Errorf("creating fleet base: %w", err)
	}

	// Register the captain with the default project so publishResolvedSnapshot works.
	if err := Register(parentHome, captainID, captainHome, "", captainID); err != nil {
		return fmt.Errorf("registering captain: %w", err)
	}

	return nil
}

func SeedCaptain(opts CaptainSeedOptions) error {
	id, homePath, parentHome, charter := opts.ID, opts.Home, opts.ParentHome, opts.Charter
	if err := os.MkdirAll(homePath, 0755); err != nil {
		return fmt.Errorf("creating captain home %s: %w", homePath, err)
	}

	for _, dir := range []string{"state", "data", "config", "projects"} {
		if err := os.MkdirAll(filepath.Join(homePath, dir), 0755); err != nil {
			return fmt.Errorf("creating %s/%s: %w", homePath, dir, err)
		}
	}

	if strings.TrimSpace(charter) == "" {
		if parentHome == "" {
			return fmt.Errorf("seeding captain %s: empty charter requires parent home for return-channel path", id)
		}
		generated, err := DefaultCaptainCharter(id, parentHome)
		if err != nil {
			return fmt.Errorf("creating default charter: %w", err)
		}
		charter = generated
	}

	// Write the canonical runtime-owned charter to .captain-charter.md.
	if err := writeCaptainCharter(homePath, charter); err != nil {
		return err
	}

	// Write a minimal AGENTS.md pointer ONLY when the file does not already exist.
	// Never overwrite or replace existing user/project-owned AGENTS.md.
	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		agentsContent := fmt.Sprintf("# Captain %s\n\nSee .captain-charter.md for the Captain charter.\n", id)
		if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
			return fmt.Errorf("writing AGENTS.md: %w", err)
		}
	}

	if err := SeedProvenance(homePath, id); err != nil {
		return fmt.Errorf("seeding provenance marker: %w", err)
	}

	if parentHome != "" {
		// Ensure typed config documents exist in the parent home before
		// registration and config push. Create minimal documents if absent.
		if err := ensureParentTypedConfig(parentHome, homePath, id); err != nil {
			return fmt.Errorf("ensuring parent typed config: %w", err)
		}
		if err := Register(parentHome, id, homePath, "", ""); err != nil {
			return fmt.Errorf("registering captain %s: %w", id, err)
		}
		// Store the General parent home in captain config for durable parent resolution.
		if err := config.Set(homePath, "parent-home", parentHome); err != nil {
			return fmt.Errorf("writing parent-home config: %w", err)
		}
		// Inherit General config + project registry so soldiers need not re-add projects.
		// Uses PropagateConfig with a noop sender (no running session yet).
		// The durable requirement is written; notification is deferred until converge.
		if _, err := PropagateConfig(PropagateConfigRequest{
			ParentHome:  parentHome,
			CaptainHome: homePath,
			Mailbox:     &noopBoundSender{},
		}); err != nil {
			return fmt.Errorf("seed inherit: %w", err)
		}
	}

	// Install project-scoped Pi captain extensions so Launch -e always has files.
	if err := ensureCaptainIntegration(homePath, opts.Integration); err != nil {
		return fmt.Errorf("installing captain pi extensions: %w", err)
	}

	fmt.Printf("Seeded captain %s at %s\n", id, homePath)
	return nil
}

// removeExistingWorktree removes a managed worktree at homePath if it exists.
// Errors are logged but not returned — best-effort cleanup before replacement.
func removeExistingWorktree(homePath, repoPath string) {
	// Check if it's a git worktree by looking for .git file.
	if _, err := os.Stat(filepath.Join(homePath, ".git")); err != nil {
		// Not a worktree — remove as regular directory if empty-ish.
		os.RemoveAll(homePath)
		return
	}

	// Read .git file to find owning repo.
	data, err := os.ReadFile(filepath.Join(homePath, ".git"))
	if err != nil {
		os.RemoveAll(homePath)
		return
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		os.RemoveAll(homePath)
		return
	}
	gitDir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(homePath), gitDir)
	}
	repoDir := filepath.Dir(filepath.Dir(filepath.Dir(gitDir)))
	// Try git worktree remove first; fall back to os.RemoveAll.
	if err := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", homePath).Run(); err != nil {
		os.RemoveAll(homePath)
	}
}

// validateWorktreeRemote verifies that the source repo's origin remote matches
// the parent home's origin remote (canonical comparison).
func validateWorktreeRemote(repoPath, parentHome string) error {
	parentRemote, err := gitRun("-C", parentHome, "remote", "get-url", "origin")
	if err != nil {
		// Parent may not be a git repo (state-only home). Skip remote validation.
		return nil
	}

	repoRemote, err := gitRun("-C", repoPath, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("source repo has no remote origin: %w", err)
	}

	if normalizeGitRemote(parentRemote) != normalizeGitRemote(repoRemote) {
		return fmt.Errorf("source repo remote %q does not match parent remote %q (canonical: %q vs %q)",
			repoRemote, parentRemote, normalizeGitRemote(repoRemote), normalizeGitRemote(parentRemote))
	}

	return nil
}

// rollbackWorktree cleans up partial provisioning artifacts on failure.
// Removes the worktree if created, and unregisters if registered.
func rollbackWorktree(worktreeCreated bool, absHome, absRepo string, registered bool, parentHome, id string) {
	if worktreeCreated {
		// Try git worktree remove first; fall back to os.RemoveAll.
		removeExistingWorktree(absHome, absRepo)
		fmt.Fprintf(os.Stderr, "munsu: rolled back worktree at %s\n", absHome)
	}
	if registered && parentHome != "" {
		if urErr := Unregister(parentHome, id); urErr != nil {
			fmt.Fprintf(os.Stderr, "munsu: warning: failed to unregister captain %s during rollback: %v\n", id, urErr)
		}
	}
}

func canonicalCaptainHome(homePath string) (string, error) {
	return home.CanonicalCaptainHome(homePath)
}
func SeedProvenance(homePath, id string) error { return home.SeedCaptainProvenance(homePath, id) }
func ValidateProvenance(homePath string) (string, error) {
	return home.ValidateCaptainProvenance(homePath)
}

// Validate checks a captain home for full structural correctness:
//   - provenance marker exists and is valid
//   - AGENTS.md exists
//   - state/data/config dirs exist
//   - home path is not a parent home, project home, or fake/system path
//   - canonical home (abs, resolved) matches the expected parent containment
func Validate(homePath, parentHome string) error {
	if _, err := ValidateProvenance(homePath); err != nil {
		return err
	}

	for _, dir := range []string{"state", "data", "config"} {
		fi, err := os.Stat(filepath.Join(homePath, dir))
		if err != nil {
			return fmt.Errorf("missing %s/ directory: %w", dir, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s/ exists but is not a directory", dir)
		}
	}

	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err != nil {
		return fmt.Errorf("missing AGENTS.md: %w", err)
	}

	// Refuse fake/project/primary homes using canonical path.
	absHome, err := canonicalCaptainHome(homePath)
	if err != nil {
		return fmt.Errorf("resolving captain home path: %w", err)
	}
	absParent, err := canonicalCaptainHome(parentHome)
	if err != nil {
		return fmt.Errorf("resolving parent home path: %w", err)
	}
	if absHome == absParent {
		return fmt.Errorf("captain home %s is the parent home itself — refuse", homePath)
	}

	bareName := filepath.Base(absHome)
	if bareName == "fake" || bareName == "project" || bareName == "primary" {
		return fmt.Errorf("captain home %s uses reserved name %q — refuse", homePath, bareName)
	}

	return nil
}

// validateStructure checks that a captain home has the expected directory
// structure and AGENTS.md, WITHOUT requiring a provenance home.
// Used by Migrate before it writes the home.
func validateStructure(homePath string) error {
	for _, dir := range []string{"state", "data", "config"} {
		fi, err := os.Stat(filepath.Join(homePath, dir))
		if err != nil {
			return fmt.Errorf("missing %s/ directory: %w", dir, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s/ exists but is not a directory", dir)
		}
	}
	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err != nil {
		return fmt.Errorf("missing AGENTS.md: %w", err)
	}
	return nil
}

// Migrate writes a provenance marker into an existing seeded captain home.
// It checks structural validity before writing and refuses fake/project/primary homes.
func Migrate(homePath, id string) error {
	refuted := filepath.Base(homePath)
	if refuted == "fake" || refuted == "project" || refuted == "primary" {
		return fmt.Errorf("refusing migrate: home %s uses reserved name %q", homePath, refuted)
	}
	if err := validateStructure(homePath); err != nil {
		return fmt.Errorf("migrate pre-check failed: %w", err)
	}
	return SeedProvenance(homePath, id)
}

// --- Registry ---

// Register registers (or re-registers) a captain in the canonical Fleet
// Registry and binds it to the given Project when provided. The Fleet Registry
// is the sole Project/Captain lifecycle authority; Config no longer stores
// lifecycle registries.
func Register(parentHome, id, homePath, scope, project string) error {
	if id == "" || homePath == "" {
		return fmt.Errorf("register requires id and home path")
	}
	canon, err := canonicalCaptainHome(homePath)
	if err != nil {
		return err
	}

	r, err := openRegistry(parentHome)
	if err != nil {
		return err
	}
	captainID, err := domain.NewCaptainID(id)
	if err != nil {
		return fmt.Errorf("register captain %q: %w", id, err)
	}

	capRev, err := r.CaptainRevision()
	if err != nil {
		return fmt.Errorf("reading captain registry: %w", err)
	}
	reg := RegisterCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    captainID,
		Home:         canon,
		Scope:        scope,
		Precondition: preconditionOf(capRev),
		Reason:       "register",
	}
	op, err := mustOpFor(reg)
	if err != nil {
		return err
	}
	// Register is idempotent: if the captain already exists, do not re-register
	// (a different definition would conflict). Only the binding is reconciled.
	if _, err := r.GetCaptain(captainID); errors.Is(err, ErrNotFound) {
		if _, err := r.RegisterCaptain(op, reg); err != nil {
			return fmt.Errorf("registering captain %s: %w", id, err)
		}
	} else if err != nil {
		return fmt.Errorf("reading captain registry: %w", err)
	}

	if project != "" {
		projectID, err := domain.NewProjectID(project)
		if err != nil {
			return fmt.Errorf("register captain %q: %w", id, err)
		}
		// Ensure the project exists so the binding is valid.
		if _, err := r.GetProject(projectID); err != nil {
			if errors.Is(err, ErrNotFound) {
				projRev, perr := r.ProjectRevision()
				if perr != nil {
					return perr
				}
				projReq := RegisterProjectRequest{
					HomeID:       r.HomeID(),
					ProjectID:    projectID,
					Name:         project,
					Path:         parentHome,
					Precondition: preconditionOf(projRev),
					Reason:       "register captain binding",
				}
				opP, err := mustOpFor(projReq)
				if err != nil {
					return err
				}
				if _, err := r.RegisterProject(opP, projReq); err != nil {
					return fmt.Errorf("registering project %s: %w", project, err)
				}
			} else {
				return err
			}
		}
		bindRev, err := r.BindingRevision()
		if err != nil {
			return err
		}
		bind := BindCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    captainID,
			ProjectID:    projectID,
			Precondition: preconditionOf(bindRev),
			Reason:       "register",
		}
		opB, err := mustOpFor(bind)
		if err != nil {
			return err
		}
		if _, err := r.BindCaptain(opB, bind); err != nil {
			return fmt.Errorf("binding captain %s to project %s: %w", id, project, err)
		}
	}

	return nil
}

// Unregister removes a captain id from the canonical Fleet Registry.
// Missing registry or missing id is a no-op (idempotent cleanup).
func Unregister(parentHome, id string) error {
	if id == "" {
		return fmt.Errorf("unregister requires id")
	}

	r, err := openRegistry(parentHome)
	if err != nil {
		return err
	}
	captainID, err := domain.NewCaptainID(id)
	if err != nil {
		return fmt.Errorf("unregister captain %q: %w", id, err)
	}
	if _, err := r.GetCaptain(captainID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("reading captain registry: %w", err)
	}
	capRev, err := r.CaptainRevision()
	if err != nil {
		return fmt.Errorf("reading captain registry: %w", err)
	}
	ret := RetireCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    captainID,
		Precondition: preconditionOf(capRev),
		Reason:       "unregister",
	}
	opR, err := mustOpFor(ret)
	if err != nil {
		return err
	}
	if _, err := r.RetireCaptain(opR, ret); err != nil {
		return fmt.Errorf("unregistering captain %s: %w", id, err)
	}
	return nil
}

// ListCaptains returns all registered captains from the canonical Fleet
// Registry, including the bound Project for each captain. The listing is
// read-only: an uninitialized home carries no captains and is never created —
// read contracts must not mutate home state.
func ListCaptains(parentHome string) ([]Info, error) {
	h, err := home.Open(parentHome)
	if err != nil {
		if errors.Is(err, home.ErrNotInitialized) {
			return nil, nil
		}
		return nil, err
	}
	r, err := NewRegistry(h)
	if err != nil {
		return nil, err
	}
	captains, err := r.ListCaptains()
	if err != nil {
		return nil, fmt.Errorf("reading captain registry: %w", err)
	}
	var result []Info
	for _, c := range captains {
		info := Info{ID: c.ID.Value(), Home: c.Home, Scope: c.Scope}
		if projectID, err := r.ProjectOf(c.ID); err == nil && projectID != (domain.ProjectID{}) {
			info.Project = projectID.Value()
		}
		result = append(result, info)
	}
	return result, nil
}

// --- Launch (session-backed) ---

func captainBootstrapPrompt(charter []byte) string {
	return `[mu-system:captain-bootstrap]
This is startup control context, not a human request.
The embedded charter below is authoritative and already loaded.
Do not read charter files again.
The runtime integration handles session initialization.
Apply the charter, then wait for marked requests or durable notifications.
Do not inspect mailbox files directly.

<captain-charter>
` + string(charter) + `
</captain-charter>`
}

// buildLaunchArgs returns the harness binary name and argument list for a captain launch.
// Pi receives the charter as system context without an initial user turn.
// prof is the captain's published-snapshot CaptainProfile (harness model/effort);
// allowlistHome is the General home whose optional model allowlist is enforced.
func buildLaunchArgs(captainHome, h string, prof config.CaptainProfile, allowlistHome string) (string, []string, error) {
	adapter, ok := harness.GetAdapter(h)
	if !ok {
		return "", nil, fmt.Errorf("captain launch: harness %q is not a verified harness", h)
	}
	contract := adapter.CaptainLaunch
	if !contract.Supported {
		return "", nil, fmt.Errorf("captain launch: harness %q does not have a verified captain launch contract", h)
	}
	if !contract.CwdAtHome || (!contract.PromptArg && adapter.Name != "pi") {
		return "", nil, fmt.Errorf("captain launch: harness %q has an incomplete captain launch contract", h)
	}
	if contract.ProjectArg {
		return "", nil, fmt.Errorf("captain launch: harness %q must not pass a project path arg", h)
	}

	// Read charter: prefer untracked .captain-charter.md (worktree captains)
	// over tracked AGENTS.md as fallback (state-only homes).
	charterPath := filepath.Join(captainHome, CaptainCharterName)
	charter, err := os.ReadFile(charterPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("reading captain charter: %w", err)
		}
		// Fall back to tracked AGENTS.md (state-only homes).
		charter, err = os.ReadFile(filepath.Join(captainHome, "AGENTS.md"))
		if err != nil {
			return "", nil, fmt.Errorf("reading captain charter: %w", err)
		}
	}

	// Model/effort come only from the published-snapshot CaptainProfile. No
	// flat-file or legacy config pin is consulted at launch.
	// Enforce the optional munsu model allowlist before any launch side effects.
	// CheckModelAllowed fails closed when a policy is present but the identity
	// is unresolved (empty model), so a runtime default cannot bypass the policy.
	if err := harness.CheckModelAllowed(allowlistHome, h, prof.Model); err != nil {
		return "", nil, fmt.Errorf("captain launch: %w", err)
	}
	args := []string{}
	if prof.Model != "" && adapter.LaunchTemplate.ModelFlag != "" {
		args = append(args, adapter.LaunchTemplate.ModelFlag, prof.Model)
	}
	if prof.Effort != "" && adapter.LaunchTemplate.EffortFlag != "" {
		args = append(args, adapter.LaunchTemplate.EffortFlag, prof.Effort)
	}
	args = append(args, adapter.LaunchTemplate.ExtraArgs...)
	// Pi captain homes load the canonical project-local integration via -e.
	if adapter.Name == "pi" {
		extDir := filepath.Join(captainHome, ".pi", "extensions")
		for _, name := range harness.PiIntegrationAliasNames() {
			aliasPath := filepath.Join(extDir, name)
			if _, err := os.Stat(aliasPath); err == nil {
				return "", nil, fmt.Errorf("captain launch: compatibility Pi integration alias is present at %s; repair with: munsu integrate repair --harness pi --scope project", aliasPath)
			} else if !os.IsNotExist(err) {
				return "", nil, fmt.Errorf("captain launch: checking compatibility Pi integration alias %s: %w", aliasPath, err)
			}
		}
		path := filepath.Join(extDir, harness.CanonicalPiIntegrationName)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return "", nil, fmt.Errorf("captain launch: canonical Pi integration is missing; repair with: munsu integrate repair --harness pi --scope project")
			}
			return "", nil, fmt.Errorf("captain launch: checking canonical Pi integration: %w", err)
		}
		args = append(args, "-e", path)
	}
	if adapter.Name == "pi" {
		args = append(args, "--append-system-prompt", captainBootstrapPrompt(charter))
	} else {
		if contract.Separator != "" {
			args = append(args, contract.Separator)
		}
		args = append(args, captainBootstrapPrompt(charter))
	}

	return adapter.Name, args, nil
}

func refuseNestedCaptainLaunch(parentHome string) error {
	if os.Getenv("MUNSU_ROLE") == "captain" {
		return fmt.Errorf("captains cannot launch other captains; spawn soldiers in their own home instead")
	}
	markerPath := filepath.Join(parentHome, ProvenanceMarkerName)
	if _, err := os.Stat(markerPath); err == nil {
		if _, validateErr := ValidateProvenance(parentHome); validateErr != nil {
			return fmt.Errorf("active home has invalid captain provenance: %w", validateErr)
		}
		return fmt.Errorf("captain home %s cannot launch another captain", parentHome)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking active home provenance: %w", err)
	}
	return nil
}

// Launch starts a captain using a session-backed endpoint.
// It validates provenance, resolves the harness, creates a new window via
// the session backend, sends a shell-safe launch script, then writes task
// meta with kind=captain and endpoint metadata only after launch succeeds.
func Launch(captainHome, parentHome string, endpoint LaunchEndpoint) error {
	if err := refuseNestedCaptainLaunch(parentHome); err != nil {
		return err
	}
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("provenance validation failed for %s: %w", captainHome, err)
	}

	// Pre-launch bootstrap: push inherited config, then local FF.
	// Uses PropagateConfig with a noop sender. During first launch
	// there is no task meta (notification skipped); during relaunch
	// the config push is redundant after converge's inheritance push.
	if _, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parentHome,
		CaptainHome: captainHome,
		Mailbox:     &noopBoundSender{},
	}); err != nil {
		return fmt.Errorf("pre-launch config-push: %w", err)
	}
	if _, _, _, err := safeFF(captainHome, parentHome); err != nil {
		// Non-git captain homes proceed; only fail when the home is a real clone.
		if _, stErr := os.Stat(filepath.Join(captainHome, ".git")); stErr == nil {
			return fmt.Errorf("pre-launch fast-forward: %w", err)
		}
	}

	// The captain's harness identity and launch profile are bound from the
	// captain's PUBLISHED snapshot (the composed config.ResolveProject output
	// written by publishResolvedSnapshot during PropagateConfig). Resolution
	// fails closed: an empty CaptainProfile is a typed launch failure, never
	// a fallback to flat files or Detect.
	snapshot, err := config.LoadPublishedSnapshot(captainHome)
	if err != nil {
		return fmt.Errorf("loading captain published snapshot for launch: %w", err)
	}
	h, err := harness.ResolveCaptainFromSnapshot(snapshot.Config())
	if err != nil {
		return fmt.Errorf("resolving captain harness: %w", err)
	}

	binName, args, err := buildLaunchArgs(captainHome, h, snapshot.Config().CaptainProfile, parentHome)
	if err != nil {
		return err
	}

	if endpoint == nil {
		return fmt.Errorf("captain launch endpoint capability is required")
	}

	markerID, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("revalidating captain provenance: %w", err)
	}
	canonicalCaptainHome, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return fmt.Errorf("canonicalizing captain home: %w", err)
	}

	binPath, err := captainLookPath(binName)
	if err != nil {
		return fmt.Errorf("%s harness not found on PATH: %w", binName, err)
	}
	cmdLine, err := launchCmd(binPath, args, canonicalCaptainHome, parentHome)
	if err != nil {
		return fmt.Errorf("building launch script: %w", err)
	}
	// The backend identity is bound at creation from the captain's PUBLISHED
	// snapshot (the composed config.ResolveProject output written by
	// publishResolvedSnapshot during PropagateConfig). A strict roundtrip
	// enforces a non-empty identity; the endpoint never receives "".
	backendIdentity := snapshot.Config().Backend
	launched, err := endpoint.Launch(parentHome, LaunchRequest{WindowName: "mu-captain-" + markerID, Command: cmdLine, WorkingDir: canonicalCaptainHome, Backend: backendIdentity})
	if err != nil {
		return fmt.Errorf("launching captain endpoint: %w", err)
	}

	// Persist task meta only after successful launch.
	meta := map[string]string{
		"kind":    "captain",
		"home":    canonicalCaptainHome,
		"window":  launched.Window,
		"backend": launched.Backend,
		"harness": h,
		"sm_id":   markerID,
	}

	for k, v := range launched.Meta {
		meta[k] = v
	}

	taskID := taskIDForCaptain(markerID)
	if err := mhome.WriteMeta(parentHome, taskID, meta); err != nil {
		_ = endpoint.Cleanup(parentHome, launched)
		return fmt.Errorf("writing captain task meta: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Launched captain %s (window=%s, harness=%s) in %s\n",
		markerID, launched.Window, binName, captainHome)
	return nil
}

// inFlightSoldierIDs returns task ids in captainHome/state with kind ship|scout.
func inFlightSoldierIDs(captainHome string) ([]string, error) {
	stateDir := filepath.Join(captainHome, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".meta") || strings.HasPrefix(name, ".") {
			continue
		}
		id, err := mhome.ReverseDurableKey(strings.TrimSuffix(name, ".meta"))
		if err != nil {
			continue
		}
		meta, err := mhome.ReadMeta(captainHome, id)
		if err != nil {
			continue
		}
		kind := meta["kind"]
		if kind == "ship" || kind == "scout" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// --- Retire ---

// Retire tears down a captain using its session-backed endpoint.
// It reads task meta, validates kind/sm_id/home before any action,
// then signals the endpoint via the session backend. Errors from backend
// operations (SendKeys, Teardown) are returned — never silently swallowed.
// Without force, refuse when the captain home still has in-flight soldiers
// (state/*.meta with kind ship|scout). force skips that gate.
// removeHome=true removes the captain home directory after teardown.
// On success, the captain is unregistered from parent data/captains.md.
func Retire(captainHome, parentHome string, removeHome, force bool, endpoint RetireEndpoint) error {
	markerID, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("refusing to retire unowned home %s: %w", captainHome, err)
	}
	canonicalCaptainHome, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return fmt.Errorf("refusing to retire home with ambiguous identity %s: %w", captainHome, err)
	}

	if !force {
		inFlight, err := inFlightSoldierIDs(captainHome)
		if err != nil {
			return fmt.Errorf("refusing to retire: cannot scan captain home for in-flight soldiers: %w", err)
		}
		if len(inFlight) > 0 {
			return fmt.Errorf("refusing to retire captain %s: %d in-flight soldier(s) (%s); use --force to override",
				markerID, len(inFlight), strings.Join(inFlight, ", "))
		}
	}

	taskID := taskIDForCaptain(markerID)
	meta, metaErr := mhome.ReadMeta(parentHome, taskID)

	if metaErr == nil {
		// Validate meta fields before use.
		if meta["kind"] != "captain" {
			return fmt.Errorf("refusing to retire: task meta kind=%q, expected \"captain\"", meta["kind"])
		}
		if meta["sm_id"] != markerID {
			return fmt.Errorf("refusing to retire: task meta sm_id=%q does not match captain marker id %q", meta["sm_id"], markerID)
		}
		if meta["home"] != canonicalCaptainHome {
			return fmt.Errorf("refusing to retire: task meta home=%q does not match canonical captain home %q", meta["home"], canonicalCaptainHome)
		}

		windowID := meta["window"]
		if windowID == "" {
			return fmt.Errorf("refusing to retire: no window in task meta for captain %s", markerID)
		}

		if endpoint == nil {
			return fmt.Errorf("captain retire endpoint capability is required")
		}
		if retireErr := endpoint.Retire(parentHome, meta); retireErr != nil {
			return fmt.Errorf("failed to retire captain %s endpoint: %w", markerID, retireErr)
		}

		// Clear parent task meta so husk prune and fleet snapshot stop treating this
		// captain as live. Status log is retained as historical return-channel evidence.
		metaPath, mpErr := mhome.MetaFilePath(parentHome, taskID)
		if mpErr != nil {
			return fmt.Errorf("failed to resolve captain task meta path for %s: %w", taskID, mpErr)
		}
		if rmErr := os.Remove(metaPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("failed to remove captain task meta %s: %w", taskID, rmErr)
		}
		fmt.Printf("  cleared parent meta for %s\n", taskID)
	} else {
		// Provenance exists but no meta — captain was never launched.
		fmt.Printf("  captain %s has no task meta (never launched)\n", markerID)
	}

	if err := Unregister(parentHome, markerID); err != nil {
		return fmt.Errorf("unregistering captain %s: %w", markerID, err)
	}
	fmt.Printf("  unregistered %s from parent registry\n", markerID)

	if removeHome {
		if err := os.RemoveAll(captainHome); err != nil {
			return fmt.Errorf("removing captain home %s: %w", captainHome, err)
		}
		fmt.Printf("Retired and removed captain home %s\n", captainHome)
	} else {
		fmt.Printf("Retired captain at %s (home retained)\n", captainHome)
	}

	return nil
}

func HandoffAmbiguousTaskID(err error) (*mhome.AmbiguousTaskIDError, bool) {
	var ambiguous *mhome.AmbiguousTaskIDError
	if errors.As(err, &ambiguous) {
		return ambiguous, true
	}
	return nil, false
}

// --- Config inheritance ---

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		if info, statErr := os.Stat(path); statErr == nil && string(existing) == string(data) && info.Mode().Perm() == mode.Perm() {
			return nil
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".munsu-inherit-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp file mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// resolveDeepestAncestor resolves symlinks on the deepest existing ancestor path,
// then appends the non-existent suffix. This avoids EvalSymlinks failure on
// paths with non-existent leaf segments while still catching symlink escapes.
func resolveDeepestAncestor(path string) (string, error) {
	candidate := path
	for {
		_, err := os.Stat(candidate)
		if err == nil {
			// Found existing ancestor. Resolve symlinks on it.
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			abs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			// Append remaining suffix.
			suffix, _ := filepath.Rel(candidate, path)
			if suffix != "." {
				return filepath.Join(abs, suffix), nil
			}
			return abs, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			// Reached root — path is entirely non-existent.
			return filepath.Abs(path)
		}
		candidate = parent
	}
}

// isSafeConfigPath checks that dst is safely contained within captainHome
// and does not symlink-escape into parentHome.
func isSafeConfigPath(dst, parentHome, captainHome string) bool {
	smCanon, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return false
	}

	// Canonicalize dst via deepest-ancestor resolution to handle non-existent paths.
	canonDst, err := resolveDeepestAncestor(dst)
	if err != nil {
		return false
	}

	// Must be under smCanon (filepath.Rel containment).
	rel, err := filepath.Rel(smCanon, canonDst)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}

	// Must NOT be under parentHome (prevents symlink escape from smHome to parent).
	parentCanon, err := canonicalCaptainHome(parentHome)
	if err != nil {
		return false
	}
	parentRel, err := filepath.Rel(parentCanon, canonDst)
	if err == nil && !strings.HasPrefix(parentRel, "..") && !filepath.IsAbs(parentRel) {
		// dst is under parentHome. Only allowed if also under smCanon.
		if !strings.HasPrefix(canonDst, smCanon+string(filepath.Separator)) && canonDst != smCanon {
			return false
		}
	}

	return true
}

func isGitTracked(dir, name string) bool {
	out, err := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", name).CombinedOutput()
	return err == nil && len(out) > 0
}

// preflightConfigPushDestinations validates all destination paths before any
// mutation or log write in configPushWithResult. Returns an error if any
// destination escapes the captain container via symlink/.. or is git-tracked.
func preflightConfigPushDestinations(parentHome, captainHome string) error {
	// Check published resolved snapshot destination.
	snapshotDst := filepath.Join(captainHome, config.PublishedSnapshotPath)
	if !isSafeConfigPath(snapshotDst, parentHome, captainHome) {
		return fmt.Errorf("published config snapshot destination escapes captain container — refuse")
	}
	if isGitTracked(filepath.Dir(snapshotDst), filepath.Base(snapshotDst)) {
		return fmt.Errorf("published config snapshot is tracked in captain git — must be gitignored")
	}

	// Check parent-home config destination.
	parentHomeDst := filepath.Join(captainHome, "config", "parent-home")
	if !isSafeConfigPath(parentHomeDst, parentHome, captainHome) {
		return fmt.Errorf("parent-home config destination escapes captain container — refuse")
	}

	return nil
}

func publishResolvedSnapshot(parentHome, captainHome string) error {
	captainID, err := ValidateProvenance(captainHome)
	if err != nil {
		return err
	}
	base, err := config.LoadFleetBase(parentHome)
	if err != nil {
		return err
	}
	r, err := openRegistry(parentHome)
	if err != nil {
		return err
	}
	id, err := domain.NewCaptainID(captainID)
	if err != nil {
		return err
	}
	captain, err := r.GetCaptain(id)
	if err != nil {
		return fmt.Errorf("Captain %q is not registered in the Fleet registry", captainID)
	}
	canonCaptain, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return err
	}
	canonRegistered, canonErr := canonicalCaptainHome(captain.Home)
	if canonErr != nil {
		return canonErr
	}
	if canonRegistered != canonCaptain {
		return fmt.Errorf("Captain %q home %q does not match %q", captainID, canonRegistered, canonCaptain)
	}
	projectID, err := r.ProjectOf(id)
	if err != nil {
		return err
	}
	if projectID == (domain.ProjectID{}) {
		return fmt.Errorf("Captain %q is not bound to a project in the Fleet registry", captainID)
	}
	project, err := r.GetProject(projectID)
	if err != nil {
		return err
	}
	facts := config.ProjectFacts{
		Name:           project.Name,
		Path:           project.Path,
		Mode:           project.Mode,
		CaptainProfile: config.CaptainProfile{},
	}
	projectOverlay, err := config.LoadProjectOverlay(parentHome, project.Name)
	if err != nil {
		return err
	}
	facts.Overlay = projectOverlay
	resolved, err := config.ResolveProject(base, facts, config.BoundaryOverrides{})
	if err != nil {
		return err
	}
	return config.StorePublishedSnapshot(captainHome, resolved)
}

// configPushWithResult copies inheritable config like configPush and also
// returns the ConfigPushResult with generation tracking. Returns nil result
// on early failure (before generation tracking runs).
func configPushWithResult(parentHome, captainHome string) (*ConfigPushResult, error) {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return nil, fmt.Errorf("refusing config-push to unmarked home %s: %w", captainHome, err)
	}

	// Preflight all destinations before any mutation or log write.
	if err := preflightConfigPushDestinations(parentHome, captainHome); err != nil {
		return nil, fmt.Errorf("config-push preflight: %w", err)
	}

	// Refresh parent-home config so the captain always has a durable reference to its General.
	if err := config.Set(captainHome, "parent-home", parentHome); err != nil {
		return nil, fmt.Errorf("refreshing parent-home: %w", err)
	}

	// Refresh the canonical .captain-charter.md so it stays current on every config-push cycle.
	if err := RefreshCharter(captainHome, parentHome); err != nil {
		return nil, fmt.Errorf("refreshing captain charter: %w", err)
	}

	// Publish resolved snapshot if typed config documents exist in parent home.
	// During initial captain setup (SeedCaptain/seedFromWorktree), the typed
	// config may not exist yet — skip gracefully and let the first propagation
	// establish the snapshot.
	snapshotPublished := false
	if _, statErr := os.Stat(filepath.Join(parentHome, config.BaseDocumentPath)); statErr == nil {
		if err := publishResolvedSnapshot(parentHome, captainHome); err != nil {
			return nil, fmt.Errorf("publishing resolved config snapshot: %w", err)
		}
		snapshotPublished = true
	}

	// Generation tracking: advance the config reread generation when the
	// snapshot was published. Skip during initial captain setup when typed
	// config is not available yet.
	if snapshotPublished {
		changed, newGen, oldDigest, newDigest, genErr := AdvanceConfigRereadGen(captainHome)
		if genErr != nil {
			return nil, fmt.Errorf("advancing config-reread generation: %w", genErr)
		}
		return &ConfigPushResult{
			Changed:    changed,
			Generation: newGen,
			OldDigest:  oldDigest,
			NewDigest:  newDigest,
		}, nil
	}

	return &ConfigPushResult{
		Changed:    false,
		Generation: 0,
	}, nil
}

// RefreshCharter re-generates and writes the .captain-charter.md for a captain home
// using the default charter template. This ensures every captain setup and reconciliation
// path produces the same canonical, versioned charter idempotently.
// parentHome is the General home for return-channel path resolution.
// Idempotent: safe to call on every converge, recover, and config-push cycle.
func RefreshCharter(captainHome, parentHome string) error {
	markerID, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("refresh charter: %w", err)
	}
	charter, err := DefaultCaptainCharter(markerID, parentHome)
	if err != nil {
		return fmt.Errorf("refresh charter: %w", err)
	}
	if err := writeCaptainCharter(captainHome, charter); err != nil {
		return fmt.Errorf("refresh charter: %w", err)
	}
	return nil
}

// --- Safe local fast-forward ---

// normalizeGitRemote maps equivalent GitHub remote URLs to a canonical form.
func normalizeGitRemote(remote string) string {
	// Strip protocol prefix for comparison.
	for _, prefix := range []string{"https://", "git@", "ssh://", "git://"} {
		remote = strings.TrimPrefix(remote, prefix)
	}
	// Map github.com SSH (git@github.com:user/repo) and HTTPS to same form.
	remote = strings.ReplaceAll(remote, ":", "/")
	remote = strings.TrimSuffix(remote, ".git")
	return strings.ToLower(remote)
}

// safeFF performs a LOCAL-only fast-forward of a captain clone to the
// parent's already-local default-branch commit. Verified: same canonical
// remote origin, correct branch/detached state, clean tree (ignoring only
// marker and local inherited paths), ancestor relationship, then git merge --ff-only.
func safeFF(captainHome, parentHome string) (before, after string, reason SafeFFReason, err error) {
	// For managed worktree captains, use the source-repo from .captain-provenance
	// instead of parentHome (General state home, which is not a git repo).
	upstreamRepo := parentHome
	if src := readCaptainProvenance(captainHome); src != "" {
		upstreamRepo = src
	}

	// Verify same canonical remote origin (allows independent clones, HTTPS/SSH equivalence).
	parentRemote, err := gitRun("-C", upstreamRepo, "remote", "get-url", "origin")
	if err != nil {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("upstream remote origin: %w", err)
	}
	smRemote, err := gitRun("-C", captainHome, "remote", "get-url", "origin")
	if err != nil {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("captain remote origin: %w", err)
	}
	if normalizeGitRemote(parentRemote) != normalizeGitRemote(smRemote) {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("captain remote %q differs from upstream remote %q (canonical: %q vs %q)",
			smRemote, parentRemote, normalizeGitRemote(smRemote), normalizeGitRemote(parentRemote))
	}

	// Resolve default branch via origin/HEAD symbolic ref. No fallback.
	symRef, err := gitRun("-C", upstreamRepo, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("upstream origin/HEAD symbolic ref missing — no default branch detected: %w", err)
	}
	// symRef looks like "refs/remotes/origin/main" — extract branch name.
	remoteRefParts := strings.SplitN(symRef, "/", 4)
	if len(remoteRefParts) < 4 || remoteRefParts[0] != "refs" || remoteRefParts[1] != "remotes" || remoteRefParts[2] != "origin" {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("unexpected origin/HEAD symbolic ref format: %q", symRef)
	}
	defaultBranch := remoteRefParts[3]

	// Verify the local branch ref exists.
	localRef := "refs/heads/" + defaultBranch
	if _, err := gitRun("-C", upstreamRepo, "rev-parse", "--verify", localRef); err != nil {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("default branch %q (%s) does not exist locally", defaultBranch, localRef)
	}

	// Resolve the commit that the local default branch points to.
	defaultCommit, err := gitRun("-C", upstreamRepo, "rev-parse", localRef)
	if err != nil {
		return "", "", SafeFFMissingOrigin, fmt.Errorf("resolving default branch commit: %w", err)
	}

	// Branch check — captain must be on default branch or detached HEAD.
	smBranch, err := gitRun("-C", captainHome, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("reading captain branch: %w", err)
	}
	if smBranch != "HEAD" && smBranch != "" && smBranch != defaultBranch {
		return "", "", SafeFFOffBranch, fmt.Errorf("captain is on branch %q, expected %q or detached HEAD", smBranch, defaultBranch)
	}

	// Clean check — reject ALL tracked changes; allow only gitignored untracked files.
	statusOut, err := gitRun("-C", captainHome, "status", "--porcelain")
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("captain git status: %w", err)
	}
	if statusOut != "" {
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			xy := line[:2]
			if xy == "??" {
				// Untracked — allow only if git check-ignore confirms gitignored.
				path := strings.TrimSpace(line[2:])
				if _, err := gitRun("-C", captainHome, "check-ignore", "-q", "--", path); err == nil {
					continue // gitignored, OK
				}
				return "", "", SafeFFChangesTracked, fmt.Errorf("captain home %s has unignored untracked file: %s", captainHome, path)
			}
			// Any non-space character means tracked change (staged or unstaged).
			if xy[0] != ' ' || xy[1] != ' ' {
				return "", "", SafeFFChangesTracked, fmt.Errorf("captain home %s has tracked changes", captainHome)
			}
		}
	}

	before, err = gitRun("-C", captainHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("reading captain HEAD: %w", err)
	}

	// Check ancestry.
	mergeBase, err := gitRun("-C", captainHome, "merge-base", before, defaultCommit)
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("merge-base failed: %w", err)
	}
	if mergeBase != before {
		return "", "", SafeFFError, fmt.Errorf("captain %s is not an ancestor of upstream default-branch commit %s — diverged or unequal history", before[:8], defaultCommit[:8])
	}

	if before == defaultCommit {
		return before, before, SafeFFAlreadyCurrent, nil
	}

	fmt.Printf("  %s: fast-forward %s → %s\n", filepath.Base(captainHome), before[:8], defaultCommit[:8])

	_, err = gitRun("-C", captainHome, "merge", "--ff-only", defaultCommit)
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("git merge --ff-only failed: %w", err)
	}

	after, err = gitRun("-C", captainHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", SafeFFError, fmt.Errorf("reading captain HEAD after ff: %w", err)
	}

	return before, after, SafeFFSuccess, nil
}

// SafeFFReason categorises the outcome of a local safe fast-forward.
type SafeFFReason string

const (
	SafeFFOffBranch      SafeFFReason = "off-branch"
	SafeFFMissingOrigin  SafeFFReason = "missing-origin"
	SafeFFChangesTracked SafeFFReason = "changes-tracked"
	SafeFFAlreadyCurrent SafeFFReason = "already-current"
	SafeFFSuccess        SafeFFReason = "success"
	SafeFFError          SafeFFReason = "error"
)

// StepStatus is the disposition of one converge step.
type ConvergeStepStatus string

const (
	ConvergeOK      ConvergeStepStatus = "ok"
	ConvergeSkipped ConvergeStepStatus = "skipped"
	ConvergeFailed  ConvergeStepStatus = "failed"
)

// StepResult captures one converge step outcome.
type ConvergeStepResult struct {
	Name   string
	Status ConvergeStepStatus
	Detail string
}

// ConvergeResult holds the per-step outcomes for a converge sweep.
type ConvergeResult struct {
	Steps []ConvergeStepResult
}

// OverallStatus computes the overall converge status from per-step outcomes.
func (cr *ConvergeResult) OverallStatus() string {
	if cr == nil || len(cr.Steps) == 0 {
		return "ok"
	}
	var okCount, failCount, skipCount int
	for _, s := range cr.Steps {
		switch s.Status {
		case ConvergeOK:
			okCount++
		case ConvergeFailed:
			failCount++
		case ConvergeSkipped:
			skipCount++
		}
	}
	if failCount > 0 && okCount > 0 {
		return "partial"
	}
	if failCount > 0 {
		return "failed"
	}
	return "ok"
}

// Update performs a single captain home update and returns a typed outcome.
// It validates provenance, detects state-only homes, runs safeFF, and maps
// results to typed outcomes.
func Update(captainHome, parentHome string) UpdateResponse {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return UpdateResponse{
			Outcome: InvalidProvenance,
			Err:     err,
		}
	}

	// Detect state-only homes (no git worktree).
	if _, err := os.Stat(filepath.Join(captainHome, ".git")); os.IsNotExist(err) {
		// Config-push for state-only homes: write config/parent-home from the
		// authoritative registered General home so watcher relay works.
		// Uses PropagateConfig with a noop sender (no running session).
		if _, pErr := PropagateConfig(PropagateConfigRequest{
			ParentHome:  parentHome,
			CaptainHome: captainHome,
			Mailbox:     &noopBoundSender{},
		}); pErr != nil {
			return UpdateResponse{
				Outcome: StateOnlySkipped,
				Err:     fmt.Errorf("config-push after state-only update: %w", pErr),
			}
		}
		return UpdateResponse{
			Outcome: StateOnlySkipped,
		}
	}

	before, after, reason, err := safeFF(captainHome, parentHome)
	outcome := outcomeFromFFReason(reason, err)
	if err != nil {
		return UpdateResponse{
			Outcome: outcome,
			Err:     err,
		}
	}

	if outcome == AlreadyCurrent || outcome == FastForwarded {
		return UpdateResponse{
			Outcome: outcome,
			Before:  before,
			After:   after,
		}
	}

	return UpdateResponse{
		Outcome: outcome,
		Before:  before,
		After:   after,
	}
}

// acquireExclusiveLock creates and acquires an exclusive file lock using flock
// with LOCK_NB (fail-fast). If another process holds the lock, it returns an
// error immediately (never removes the lock file). Release verifies inode
// identity (os.SameFile) AND random token match before unlinking.
func acquireExclusiveLock(lockPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	// Exclusive nonblocking lock — fail fast, never block.
	acquired, err := tryLockFile(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("acquiring converge lock: %w", err)
	}
	if !acquired {
		f.Close()
		// NEVER remove lockPath on failed acquisition — that would unlink
		// the owner's lock and permit a third party to acquire.
		return nil, fmt.Errorf("converge lock is held by another process — try again later")
	}

	// Generate a cryptographically random token for generation-safe release.
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		_ = unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("generating random token: %w", err)
	}
	tokenHex := fmt.Sprintf("%x", token)

	if _, err := fmt.Fprintf(f, "%s\n", tokenHex); err != nil {
		_ = unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("writing token to lock: %w", err)
	}
	f.Sync()

	released := false
	return func() {
		if released {
			return
		}
		released = true
		defer func() {
			_ = unlockFile(f)
			f.Close()
		}()

		// Verify inode identity: our fd must still point to the same file.
		fdStat, err := f.Stat()
		if err != nil {
			return
		}
		pathStat, err := os.Stat(lockPath)
		if err != nil {
			return
		}
		if !os.SameFile(fdStat, pathStat) {
			return // lock file was replaced by a different generation
		}

		// Verify token still matches — never remove a newer generation.
		data, err := os.ReadFile(lockPath)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(data)) != tokenHex {
			return // token changed — different generation
		}

		// This remove is unreachable on Windows, and that is a property of
		// the checks above rather than of Remove itself. Both generation
		// checks reach back into the locked file: os.SameFile's file-ID
		// lookup opens the path with no sharing, and the token comparison
		// reads it through a fresh handle into a range the exclusive
		// whole-file LockFileEx lock covers. Windows denies both; which
		// denial returns first is Windows-internal, and either way the
		// closure exits here and the remove never runs. The consequence
		// that matters is that on Windows those checks never complete, so
		// they are inoperative there: the file is permanent, and release
		// is fail-closed — nothing is ever removed, so a newer generation
		// can never be removed — but the SameFile/token safety net simply
		// does not exist. FILE_SHARE_DELETE was considered and rejected:
		// it would let a third party unlink a live lock file, and it would
		// not fix the token read, which the byte-range lock denies
		// regardless of share mode. The price is one fixed-name file per
		// home, reused on every converge, never accumulated.
		_ = os.Remove(lockPath)
	}, nil
}

// nudgeMarkerPath returns the path for a pending nudge home.
func nudgeMarkerPath(parentHome, smID string) string {
	return filepath.Join(parentHome, "state", NudgePendingDir, smID+".pending")
}

// writeNudgeMarker creates a pending nudge marker with full metadata.
// Written before sending, removed only after successful SendKeys.
func writeNudgeMarker(parentHome, smID, smHome, commit, instructions, message string) error {
	dir := filepath.Join(parentHome, "state", NudgePendingDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("id=%s\nhome=%s\ncommit=%s\ninstructions=%s\nmessage=%s\n",
		smID, smHome, commit, instructions, message)
	return os.WriteFile(nudgeMarkerPath(parentHome, smID), []byte(content), 0644)
}

// readNudgeMarker reads and returns the fields from a pending nudge home.
func readNudgeMarker(parentHome, smID string) (map[string]string, error) {
	data, err := os.ReadFile(nudgeMarkerPath(parentHome, smID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			result[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return result, nil
}

// removeNudgeMarker deletes a pending nudge home.
func removeNudgeMarker(parentHome, smID string) {
	os.Remove(nudgeMarkerPath(parentHome, smID))
}

// Converge performs a locked convergence sweep over registered captains.
// Order: lock, validate registry/provenance, flush send outbox, retry pending
// nudges, safe ff, inheritance push, ownership-backed backend Alive check,
// watcher status check, and reread nudge only if instruction surface advanced.
type ConvergeCapabilities struct {
	Notification any
	Continuity   CaptainContinuityPort
	Messaging    CaptainMessagingPort
	Watcher      CaptainWatcherPort
	Mailbox      home.BoundSender
	Integration  IntegrationPort
	Launch       LaunchEndpoint
	Probe        ProbeEndpoint
	Nudge        NudgeEndpoint
}

func Converge(parentHome string, registered []Info, caps ConvergeCapabilities) (*ConvergeResult, error) {
	sender := caps.Mailbox
	if len(registered) > 0 && caps.Notification == nil {
		return nil, fmt.Errorf("uplink notification transport capability is required")
	}
	if len(registered) > 0 && sender == nil {
		return nil, fmt.Errorf("captain mailbox sender capability is required")
	}
	release, err := convergeLockAcquire(parentHome)
	if err != nil {
		return nil, fmt.Errorf("acquiring converge lock: %w", err)
	}
	defer release()

	if len(registered) == 0 {
		return &ConvergeResult{}, nil
	}

	var result ConvergeResult
	var errs []string

	for _, sm := range registered {
		if sm.Home == "" {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": registry validation", Status: ConvergeSkipped, Detail: "missing home path"})
			errs = append(errs, fmt.Sprintf("%s: missing home path", sm.ID))
			continue
		}

		// a. Registry validation.
		markerID, valErr := ValidateProvenance(sm.Home)
		if valErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": registry validation", Status: ConvergeFailed, Detail: fmt.Sprintf("provenance validation failed: %v", valErr)})
			errs = append(errs, fmt.Sprintf("%s: provenance validation failed: %v", sm.ID, valErr))
			continue
		}
		if markerID != sm.ID {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": registry validation", Status: ConvergeFailed, Detail: fmt.Sprintf("marker id %q does not match registry id %q", markerID, sm.ID)})
			errs = append(errs, fmt.Sprintf("%s: marker id %q does not match registry id %q", sm.ID, markerID, sm.ID))
			continue
		}
		result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": registry validation", Status: ConvergeOK, Detail: "valid"})

		// b. Stale legacy transport guard (read-only fail-closed).
		// The legacy .captain-send-outbox and .command-envelope transport is removed.
		// If any stale records remain, report the exact paths and fail with
		// an actionable upgrade instruction. Never writes, migrates, or deletes.
		if guardErr := checkStaleLegacyRecords(parentHome, sm.ID); guardErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": legacy transport guard", Status: ConvergeFailed, Detail: guardErr.Error()})
			errs = append(errs, guardErr.Error())
		} else {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": legacy transport guard", Status: ConvergeOK, Detail: "ok"})
		}

		// c. Nudge retry.
		if nudgeErr := retryNudge(parentHome, sm, caps.Nudge); nudgeErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": nudge retry", Status: ConvergeFailed, Detail: nudgeErr.Error()})
			errs = append(errs, nudgeErr.Error())
		} else {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": nudge retry", Status: ConvergeOK, Detail: "ok"})
		}

		// d. Safe local fast-forward.
		before, after, ffReason, ffErr := safeFF(sm.Home, parentHome)
		if ffErr != nil {
			// State-only home (no git worktree): skip FF and log diagnostic.
			if _, stErr := os.Stat(filepath.Join(sm.Home, ".git")); os.IsNotExist(stErr) {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": safe fast-forward", Status: ConvergeSkipped, Detail: "state-only-home"})
				fmt.Printf("  %s: git fast-forward skipped (state-only home)\n", sm.ID)
			} else {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": safe fast-forward", Status: ConvergeFailed, Detail: ffErr.Error()})
				errs = append(errs, fmt.Sprintf("%s: safe ff failed: %v", sm.ID, ffErr))
			}
		} else if ffReason == SafeFFAlreadyCurrent {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": safe fast-forward", Status: ConvergeSkipped, Detail: "already-current"})
		} else if ffReason == SafeFFSuccess {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": safe fast-forward", Status: ConvergeOK, Detail: fmt.Sprintf("fast-forwarded %s → %s", before[:8], after[:8])})

			// g. Instruction surface tracking (only on FF).
			if hasSurfaceDiff(sm.Home, before, after) {
				printGitContentDiff(sm.Home, before, after)
				digest, digestErr := instructionSurfaceDigest(sm.Home, after)
				if digestErr != nil {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": instruction surface tracking", Status: ConvergeFailed, Detail: digestErr.Error()})
					errs = append(errs, fmt.Sprintf("%s: computing instruction digest: %v", sm.ID, digestErr))
					continue
				}
				msg := fmt.Sprintf("instruction surface changed in %s", after[:8])
				if wErr := writeNudgeMarker(parentHome, sm.ID, sm.Home, after, digest, msg); wErr != nil {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": instruction surface tracking", Status: ConvergeFailed, Detail: wErr.Error()})
					errs = append(errs, fmt.Sprintf("%s: writing nudge marker: %v", sm.ID, wErr))
				} else {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": instruction surface tracking", Status: ConvergeOK, Detail: "nudge written"})
					// If alive, immediately send.
					if err := sendNudge(parentHome, sm, caps.Nudge); err != nil {
						errs = append(errs, err.Error())
					}
				}
			} else {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": instruction surface tracking", Status: ConvergeSkipped, Detail: "no surface change"})
			}
		} else {
			// Should not happen: safeFF returned no error but unknown reason.
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": safe fast-forward", Status: ConvergeOK, Detail: "no change"})
		}

		// e. Inheritance push with generation tracking and mailbox notification.
		propRes, propErr := PropagateConfig(PropagateConfigRequest{
			ParentHome:  parentHome,
			CaptainHome: sm.Home,
			Mailbox:     sender,
		})
		if propErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": inheritance push", Status: ConvergeFailed, Detail: propErr.Error()})
			errs = append(errs, fmt.Sprintf("%s: config-push failed: %v", sm.ID, propErr))
		} else {
			detail := "ok"
			if propRes.Changed {
				detail = fmt.Sprintf("generation=%d", propRes.Generation)
			}
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": inheritance push", Status: ConvergeOK, Detail: detail})

			// Report config-reread requirement state.
			reqStep := sm.ID + ": config-reread requirement"
			if !propRes.Changed {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: reqStep, Status: ConvergeSkipped, Detail: "unchanged"})
			} else if propRes.RequirementState == RequirementFailed {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: reqStep, Status: ConvergeFailed, Detail: fmt.Sprintf("gen=%d %s", propRes.Generation, propRes.Detail)})
				errs = append(errs, fmt.Sprintf("%s: config-reread requirement failed", sm.ID))
			} else {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: reqStep, Status: ConvergeOK, Detail: fmt.Sprintf("gen=%d %s=%s", propRes.Generation, propRes.RequirementState, propRes.NotificationState)})
			}
		}

		// e2. Charter refresh — ensure .captain-charter.md is current.
		if err := RefreshCharter(sm.Home, parentHome); err != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": charter refresh", Status: ConvergeFailed, Detail: err.Error()})
			errs = append(errs, fmt.Sprintf("%s: charter refresh failed: %v", sm.ID, err))
		} else {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": charter refresh", Status: ConvergeOK, Detail: "ok"})
		}

		// f. Liveness check + strict-dead-only auto-recover.
		state, stateErr := checkAliveWithProbe(parentHome, sm, caps.Probe)
		if stateErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: stateErr.Error()})
			errs = append(errs, fmt.Sprintf("%s: alive check failed: %v", sm.ID, stateErr))
			continue
		}
		taskID := taskIDForCaptain(sm.ID)
		switch state {
		case CaptainAlive:
			// Liveness proven by observation: clear any armed relaunch guard.
			if cErr := clearRelaunchGuard(parentHome, taskID); cErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: fmt.Sprintf("clearing resolved relaunch guard failed: %v", cErr)})
				errs = append(errs, fmt.Sprintf("%s: clearing resolved relaunch guard failed: %v", sm.ID, cErr))
				continue
			}
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeOK, Detail: "alive"})
		case CaptainSeeded:
			// No launch evidence in task meta: never launched.
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeSkipped, Detail: "absent (seeded)"})
		case CaptainUnproven:
			// Non-dead evidence (no-agent, generic errors, unproven Alive=false)
			// is not authoritative absence; never relaunch.
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: "endpoint state unproven; strict-dead-only refuses relaunch"})
			errs = append(errs, fmt.Sprintf("%s: endpoint state unproven; strict-dead-only refuses relaunch", sm.ID))
			continue
		case CaptainDead:
			// Launched-but-dead (binding already validated by checkAliveWithProbe):
			// refuse a duplicate relaunch while the guard is armed, verify the
			// canonical integration, then relaunch.
			meta, mErr := mhome.ReadMeta(parentHome, taskID)
			if mErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: fmt.Sprintf("re-reading task meta for relaunch guard: %v", mErr)})
				errs = append(errs, fmt.Sprintf("%s: re-reading task meta for relaunch guard: %v", sm.ID, mErr))
				continue
			}
			refused, remaining, gErr := consultRelaunchGuard(parentHome, taskID, meta, time.Now())
			if gErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: gErr.Error()})
				errs = append(errs, fmt.Sprintf("%s: relaunch guard check failed: %v", sm.ID, gErr))
				continue
			}
			if refused {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: fmt.Sprintf("prior relaunch liveness remains unproven; duplicate launch refused (guard expires in %s)", remaining.Round(time.Second))})
				errs = append(errs, fmt.Sprintf("%s: duplicate relaunch refused (guard expires in %s)", sm.ID, remaining.Round(time.Second)))
				continue
			}
			// Launched-but-dead: verify the canonical Pi integration before recovery.
			if integrationErr := requireHealthyPiIntegration(sm.Home, caps.Integration); integrationErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: integrationErr.Error()})
				errs = append(errs, fmt.Sprintf("%s: auto-recover blocked: %v", sm.ID, integrationErr))
				continue
			}
			if lErr := Launch(sm.Home, parentHome, caps.Launch); lErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: fmt.Sprintf("dead agent — auto-recover failed: %v", lErr)})
				errs = append(errs, fmt.Sprintf("%s: auto-recover failed: %v", sm.ID, lErr))
			} else {
				proven, pErr := proveRelaunch(parentHome, sm, caps.Probe, proveSleep, time.Now)
				if pErr != nil {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: fmt.Sprintf("dead agent — auto-recovered but %v", pErr)})
					errs = append(errs, fmt.Sprintf("%s: auto-recover liveness proof failed: %v", sm.ID, pErr))
				} else if !proven {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeFailed, Detail: "dead agent — auto-recovered but post-launch liveness could not be proven; duplicate relaunch guarded"})
					errs = append(errs, fmt.Sprintf("%s: auto-recover relaunched but post-launch liveness could not be proven", sm.ID))
				} else {
					result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": liveness check", Status: ConvergeOK, Detail: "dead agent — auto-recovered"})
					fmt.Printf("  %s: auto-recovered (dead agent)\n", sm.ID)
				}
			}
		}

		// i. Config-reread pending reconciliation.
		if mbErr := ReconcileConfigRereadPending(parentHome, sm.Home); mbErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": config-reread pending reconciliation", Status: ConvergeFailed, Detail: mbErr.Error()})
			errs = append(errs, mbErr.Error())
		} else {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": config-reread pending reconciliation", Status: ConvergeOK, Detail: "ok"})
		}

		// g. Captain continuity reconciliation.
		if caps.Continuity == nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": continuity reconciliation", Status: ConvergeFailed, Detail: "captain continuity capability is required"})
			errs = append(errs, sm.ID+": captain continuity capability is required")
		} else {
			continuity, continuityErr := caps.Continuity.Reconcile(parentHome, CaptainEndpoint{ID: sm.ID, Home: sm.Home, Scope: sm.Scope, Project: sm.Project})
			if continuityErr != nil {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": continuity reconciliation", Status: ConvergeFailed, Detail: continuityErr.Error()})
				errs = append(errs, fmt.Sprintf("%s: continuity reconciliation failed: %v", sm.ID, continuityErr))
			} else {
				result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": continuity reconciliation", Status: ConvergeOK, Detail: fmt.Sprintf("accepted=%d notified=%d queued=%d", continuity.Accepted, continuity.Notified, continuity.Queued)})
			}
		}

		// h. Mailbox pending reconciliation (General → Captain).
		// For each pending mailbox envelope targeting this captain, checks for
		// a ProcessingAck in the captain's inbox. If ack exists and validates,
		// removes the sender's pending record. If no ack exists, retries the
		// NotificationRef (duplicate notification is idempotent).
		if caps.Messaging == nil {
			errs = append(errs, sm.ID+": captain messaging capability is required")
		} else if mbErr := caps.Messaging.ReconcilePending(parentHome, CaptainEndpoint{ID: sm.ID, Home: sm.Home, Scope: sm.Scope, Project: sm.Project}, sender); mbErr != nil {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": mailbox pending reconciliation", Status: ConvergeFailed, Detail: mbErr.Error()})
			errs = append(errs, mbErr.Error())
		} else {
			result.Steps = append(result.Steps, ConvergeStepResult{Name: sm.ID + ": mailbox pending reconciliation", Status: ConvergeOK, Detail: "ok"})
		}

		// Watcher status check and reporting.
		ws := WatcherAbsent
		if caps.Watcher != nil {
			ws = caps.Watcher.Status(sm.Home)
		}
		switch ws {
		case WatcherRunning:
			fmt.Printf("  %s: watcher running\n", sm.ID)
		case WatcherStopped:
			fmt.Printf("  %s: watcher stopped (beat stale)\n", sm.ID)
		case WatcherAbsent:
			fmt.Printf("  %s: watcher absent\n", sm.ID)
		}
	}

	if len(errs) > 0 {
		return &result, fmt.Errorf("converge completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return &result, nil
}

// --- Recover ---

// RecoverOutcome is the disposition of one captain during a recover sweep.
type RecoverOutcome string

const (
	// RecoverAlive: endpoint probed alive; no action taken.
	RecoverAlive RecoverOutcome = "alive"
	// RecoverSeeded: home present but never launched (no launch meta/window); no action.
	RecoverSeeded RecoverOutcome = "seeded"
	// RecoverRelaunched: endpoint was launched-but-dead and has been relaunched.
	RecoverRelaunched RecoverOutcome = "relaunched"
	// RecoverFailed: relaunch attempted and failed (e.g. unknown harness); see Error.
	RecoverFailed RecoverOutcome = "failed"
)

// RecoverEntry describes one captain's recover result.
type RecoverEntry struct {
	ID      string
	Home    string
	Outcome RecoverOutcome
	Error   string // non-empty when Outcome == RecoverFailed
}

// RecoverResult holds the aggregate outcome of a recover sweep.
type RecoverResult struct {
	Entries    []RecoverEntry
	Relaunched int
	Alive      int
	Seeded     int
	Failed     int
	Steps      []StepResult
}

// StepsString renders a human-readable per-step summary for the transaction CLI output.
func (r *RecoverResult) StepsString() string {
	if r == nil || len(r.Steps) == 0 {
		return "no recovery steps"
	}
	var b strings.Builder
	for _, s := range r.Steps {
		switch s.State {
		case StepOk:
			fmt.Fprintf(&b, "  %s: ok (%s)\n", s.Name, s.Detail)
		case StepFailed:
			fmt.Fprintf(&b, "  %s: FAILED (%s)\n", s.Name, s.Detail)
		case StepSkipped:
			fmt.Fprintf(&b, "  %s: skipped (%s)\n", s.Name, s.Detail)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Recover probes each registered captain and relaunches launched-but-dead endpoints.
// It fails closed on unknown/unverified harnesses (Launch returns an error which is
// recorded on the entry) and continues with the remaining captains. Seeded-but-never-
// launched captains are reported but not launched. The sweep holds the converge lock so
// it does not race with an in-flight converge.
func requireHealthyPiIntegration(captainHome string, integration IntegrationPort) error {
	snapshot, err := config.LoadPublishedSnapshot(captainHome)
	if err != nil {
		return fmt.Errorf("loading captain published snapshot for integration check: %w", err)
	}
	h, err := harness.ResolveCaptainFromSnapshot(snapshot.Config())
	if err != nil {
		return fmt.Errorf("resolving captain harness: %w", err)
	}
	if h != harness.Pi {
		return nil
	}
	if integration == nil {
		return fmt.Errorf("canonical Pi integration status capability is required")
	}
	status, err := integration.Status(captainHome, h)
	if err != nil {
		return fmt.Errorf("checking canonical Pi integration: %w", err)
	}
	if status.State != "installed" {
		return fmt.Errorf("canonical Pi integration is %s: %s; repair with: munsu integrate repair --harness pi --scope project", status.State, status.Message)
	}
	return nil
}

func Recover(parentHome string, registered []Info, capabilities RecoverCapabilities) (*RecoverResult, error) {
	res := &RecoverResult{}
	if len(registered) == 0 {
		return res, nil
	}

	release, err := convergeLockAcquire(parentHome)
	if err != nil {
		return res, fmt.Errorf("acquiring converge lock: %w", err)
	}
	defer release()

	for _, sm := range registered {
		entry := RecoverEntry{ID: sm.ID, Home: sm.Home}
		if sm.Home == "" {
			entry.Outcome = RecoverFailed
			entry.Error = "missing home path"
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		markerID, vErr := ValidateProvenance(sm.Home)
		if vErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("provenance validation failed: %v", vErr)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		if markerID != sm.ID {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("marker id %q does not match registry id %q", markerID, sm.ID)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		state, stateErr := checkAliveWithProbe(parentHome, sm, capabilities.Probe)
		if stateErr != nil {
			// Backend resolution failure or non-authoritative evidence (no-agent,
			// generic errors, unproven Alive=false): cannot prove liveness and
			// cannot prove authoritative absence — fail closed, never relaunch.
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("alive check failed: %v", stateErr)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		taskID := taskIDForCaptain(sm.ID)
		switch state {
		case CaptainAlive:
			// Liveness proven by observation: clear any armed relaunch guard.
			if cErr := clearRelaunchGuard(parentHome, taskID); cErr != nil {
				entry.Outcome = RecoverFailed
				entry.Error = fmt.Sprintf("clearing resolved relaunch guard failed: %v", cErr)
				res.Failed++
				res.Entries = append(res.Entries, entry)
				continue
			}
			entry.Outcome = RecoverAlive
			res.Alive++
			res.Entries = append(res.Entries, entry)
			continue
		case CaptainSeeded:
			// No launch evidence in task meta: never launched.
			entry.Outcome = RecoverSeeded
			res.Seeded++
			res.Entries = append(res.Entries, entry)
			continue
		case CaptainUnproven:
			entry.Outcome = RecoverFailed
			entry.Error = "endpoint evidence is not authoritatively absent; strict-dead-only refuses relaunch"
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		// CaptainDead: launched-but-dead (binding already validated by
		// checkAliveWithProbe). Refuse a duplicate relaunch while the guard is
		// armed, verify the bound harness integration, then relaunch.
		meta, mErr := mhome.ReadMeta(parentHome, taskID)
		if mErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("re-reading task meta for relaunch guard: %v", mErr)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		// Launched-but-dead: refuse a duplicate relaunch while the guard is armed.
		refused, remaining, gErr := consultRelaunchGuard(parentHome, taskID, meta, time.Now())
		if gErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = gErr.Error()
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		if refused {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("prior relaunch liveness remains unproven; duplicate launch refused (guard expires in %s)", remaining.Round(time.Second))
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		// Launched-but-dead: verify the bound harness integration before relaunch.
		if integrationErr := requireHealthyPiIntegration(sm.Home, capabilities.Integration); integrationErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = integrationErr.Error()
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		if lErr := Launch(sm.Home, parentHome, capabilities.Launch); lErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = lErr.Error()
			res.Failed++
		} else {
			proven, pErr := proveRelaunch(parentHome, sm, capabilities.Probe, proveSleep, time.Now)
			if pErr != nil {
				entry.Outcome = RecoverFailed
				entry.Error = fmt.Sprintf("relaunched but %v", pErr)
				res.Failed++
			} else if !proven {
				entry.Outcome = RecoverFailed
				entry.Error = "relaunched but post-launch liveness could not be proven; duplicate relaunch guarded"
				res.Failed++
			} else {
				entry.Outcome = RecoverRelaunched
				res.Relaunched++
			}
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

// ProbeLiveness reports each registered captain as alive/dead/seeded/unknown without
// mutating anything. Used by session-start to surface dead endpoints. Never relaunches.
// Returns entries in registry order.
func ProbeLiveness(parentHome string, registered []Info, probe ProbeEndpoint) []LivenessProbe {
	if len(registered) == 0 {
		return nil
	}
	probes := make([]LivenessProbe, 0, len(registered))
	for _, sm := range registered {
		p := LivenessProbe{ID: sm.ID, Home: sm.Home}
		if sm.Home == "" {
			p.Status = "unknown"
			probes = append(probes, p)
			continue
		}
		if _, err := ValidateProvenance(sm.Home); err != nil {
			p.Status = "unknown"
			probes = append(probes, p)
			continue
		}
		// CaptainStatus: alive | dead | seeded | unknown.
		state, err := checkAliveWithProbe(parentHome, sm, probe)
		if err != nil {
			p.Status = "unknown"
		} else {
			switch state {
			case CaptainAlive:
				p.Status = "alive"
			case CaptainDead:
				// Authoritative pane absence is the only dead report.
				p.Status = "dead"
			case CaptainUnproven:
				p.Status = "unknown"
			case CaptainSeeded:
				p.Status = "seeded"
			}
		}
		probes = append(probes, p)
	}
	return probes
}

// LivenessProbe is one captain's non-mutating liveness reading.
type LivenessProbe struct {
	ID     string
	Home   string
	Status string // alive | dead | seeded | unknown
}

// checkAliveWithProbe validates Captain endpoint metadata before probing and
// derives the strict endpoint state. Only CaptainDead (authoritative pane
// absence) authorizes relaunch; every other non-alive state is
// CaptainUnproven and fails closed. CaptainSeeded means no launch evidence
// exists in task meta (never launched).
func checkAliveWithProbe(parentHome string, sm Info, probe ProbeEndpoint) (CaptainEndpointState, error) {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := mhome.ReadMeta(parentHome, taskID)
	if err != nil {
		return CaptainSeeded, nil
	}
	if meta["kind"] != "captain" || meta["sm_id"] != sm.ID {
		return CaptainSeeded, nil
	}
	canonSM, err := canonicalCaptainHome(sm.Home)
	if err != nil {
		return CaptainUnproven, fmt.Errorf("canonicalizing captain home: %w", err)
	}
	if meta["home"] != canonSM || meta["window"] == "" {
		return CaptainSeeded, nil
	}
	if probe == nil {
		return CaptainUnproven, fmt.Errorf("captain probe endpoint capability is required")
	}
	result, err := probe.Probe(parentHome, meta)
	if err != nil {
		return CaptainUnproven, err
	}
	if result.Absent {
		// Authoritative pane absence: the sole relaunch authority.
		return CaptainDead, nil
	}
	if result.PaneAlive && result.AgentAlive && captainAgentStatusConfirmedLive(result.AgentStatus) {
		return CaptainAlive, nil
	}
	// Pane-present/no-agent, Starting/Unknown/Unresponsive/StaleIdentity/
	// Unresolved, and unproven plain Alive=false are NOT authoritative absence:
	// strict-dead-only fails closed instead of relaunching.
	return CaptainUnproven, fmt.Errorf("captain %s endpoint evidence is not authoritatively absent (pane=%t agent=%t status=%q): strict-dead-only refuses relaunch", sm.ID, result.PaneAlive, result.AgentAlive, result.AgentStatus)
}

// instructionSurfaceDigest returns a deterministic digest of the tracked instruction
// surface at commit. Git object IDs bind the digest to exact file content and paths.
func instructionSurfaceDigest(home, commit string) (string, error) {
	tree, err := gitRun("-C", home, "ls-tree", "-r", "--full-tree", commit, "--",
		"AGENTS.md", "bin/", ".agents/skills/")
	if err != nil {
		return "", err
	}
	return captainSHA256Content([]byte(tree)), nil
}

// hasSurfaceDiff reports whether the tracked instruction surface changed.
func hasSurfaceDiff(home, before, after string) bool {
	beforeDigest, err := instructionSurfaceDigest(home, before)
	if err != nil {
		return false
	}
	afterDigest, err := instructionSurfaceDigest(home, after)
	return err == nil && beforeDigest != afterDigest
}

// sendNudge sends a short re-read message to a captain via its
// session-backed endpoint. It reads task meta, validates endpoint
// identity, sends the message, removes the pending marker, and
// updates applied instruction identity only after success.
// On failure, the marker remains.
func sendNudge(parentHome string, sm Info, endpoint NudgeEndpoint) error {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := mhome.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("%s: no task meta — marker remains", sm.ID)
	}

	// Validate endpoint meta before use.
	if meta["kind"] != "captain" {
		return fmt.Errorf("%s: meta kind=%q, expected captain — marker remains", sm.ID, meta["kind"])
	}
	if meta["sm_id"] != sm.ID {
		return fmt.Errorf("%s: meta sm_id=%q does not match — marker remains", sm.ID, meta["sm_id"])
	}
	canonSM, err := canonicalCaptainHome(sm.Home)
	if err != nil {
		return fmt.Errorf("%s: cannot canonicalize home — marker remains: %v", sm.ID, err)
	}
	if meta["home"] != canonSM {
		return fmt.Errorf("%s: meta home=%q does not match canonical home %q — marker remains", sm.ID, meta["home"], canonSM)
	}

	windowID := meta["window"]
	if windowID == "" {
		return fmt.Errorf("%s: no window in meta — marker remains", sm.ID)
	}

	// Read pending marker to validate content before sending.
	marker, markerErr := readNudgeMarker(parentHome, sm.ID)
	if markerErr != nil || marker == nil {
		return fmt.Errorf("%s: no pending nudge marker — marker remains", sm.ID)
	}
	// Validate marker fields against registry and endpoint.
	if marker["id"] != sm.ID {
		return fmt.Errorf("%s: marker id=%q does not match registry id %q — marker remains", sm.ID, marker["id"], sm.ID)
	}
	canonMarkerHome, err := canonicalCaptainHome(marker["home"])
	if err != nil || canonMarkerHome != canonSM {
		return fmt.Errorf("%s: marker home=%q does not match canonical home %q — marker remains", sm.ID, marker["home"], canonSM)
	}
	if marker["commit"] == "" {
		return fmt.Errorf("%s: marker has empty commit — marker remains", sm.ID)
	}
	// Verify the marker binds to an exact commit and instruction surface.
	if _, err := gitRun("-C", sm.Home, "rev-parse", "--verify", marker["commit"]+"^{commit}"); err != nil {
		return fmt.Errorf("%s: marker commit %q is not a valid commit in captain repo — marker remains", sm.ID, marker["commit"])
	}
	expectedDigest, err := instructionSurfaceDigest(sm.Home, marker["commit"])
	if err != nil {
		return fmt.Errorf("%s: cannot compute marker instruction digest — marker remains: %v", sm.ID, err)
	}
	if marker["instructions"] != expectedDigest {
		return fmt.Errorf("%s: marker instruction digest does not match commit %s — marker remains", sm.ID, marker["commit"])
	}
	expectedMessage := fmt.Sprintf("instruction surface changed in %s", marker["commit"][:8])
	if marker["message"] != expectedMessage {
		return fmt.Errorf("%s: marker message %q does not match %q — marker remains", sm.ID, marker["message"], expectedMessage)
	}

	if endpoint == nil {
		return fmt.Errorf("captain nudge endpoint capability is required")
	}
	result, err := endpoint.Nudge(parentHome, meta, "/re-read-agents")
	if err != nil {
		return fmt.Errorf("%s: nudge failed — marker remains: %v", sm.ID, err)
	}
	if !result.Acknowledged {
		return fmt.Errorf("%s: send not acknowledged (status=%s) — marker remains", sm.ID, result.Status)
	}

	// After successful prompt submission, update durable meta with actual applied
	// commit and deterministic digest BEFORE removing home.
	meta["applied_commit"] = marker["commit"]
	meta["applied_digest"] = marker["instructions"]
	if metaErr := mhome.WriteMeta(parentHome, taskID, meta); metaErr != nil {
		return fmt.Errorf("%s: meta update failed after send (marker remains): %v", sm.ID, metaErr)
	}

	// Only remove marker after WriteMeta succeeded.
	removeNudgeMarker(parentHome, sm.ID)

	fmt.Printf("  %s: nudge sent (commit=%s, digest=%.12s...), marker cleared\n", sm.ID, marker["commit"][:8], marker["instructions"])
	return nil
}

// retryNudge checks for a pending parent-home nudge marker and attempts
// to resolve the endpoint and send the re-read message. On success,
// the marker is removed and applied instruction identity updated.
// On failure, the marker remains for the next converge cycle.
func retryNudge(parentHome string, sm Info, endpoint NudgeEndpoint) error {
	marker, err := readNudgeMarker(parentHome, sm.ID)
	if err != nil {
		return fmt.Errorf("%s: reading nudge marker: %v", sm.ID, err)
	}
	if marker == nil {
		return nil // no pending nudge
	}
	// Attempt to send. If successful, marker is removed by sendNudge.
	// On failure, marker remains.
	return sendNudge(parentHome, sm, endpoint)
}

// printGitContentDiff prints the content diff for key files between two commits.
func printGitContentDiff(home, before, after string) {
	if before == after {
		return
	}
	for _, file := range []string{"AGENTS.md", "bin/", ".agents/skills/"} {
		diff, err := gitRun("-C", home, "diff", "--no-color", before, after, "--", file)
		if err == nil && diff != "" {
			fmt.Printf("    diff in %s:\n", file)
			for _, line := range strings.Split(diff, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}
