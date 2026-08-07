// Package soldier implements the Soldier launch prompt, charter, envelope,
// and skill manifest — the full verifiable launch context contract.
package fleet

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// CharterVersion is the current version identifier embedded in every generated
// .soldier-charter.md file so agents and operators can verify the charter revision.
const CharterVersion = "soldier-charter-v1"

// EnvelopeVersion is the current launch envelope format version.
const EnvelopeVersion = "soldier-envelope-v1"

// CharterName is the runtime-owned charter file name in the worktree.
const CharterName = ".soldier-charter.md"

// BriefName is the runtime-owned brief file name in the worktree.
const BriefName = ".soldier-brief.md"

// EnvelopeName is the structured launch envelope file name in the worktree.
const EnvelopeName = ".soldier-envelope.json"

// LaunchScriptName is the name of the harness launch script written to the worktree.
const LaunchScriptName = ".soldier-launch.sh"

// ManifestName is the versioned launch artifact manifest file name.
const ManifestName = ".soldier-manifest.json"

// LaunchArtifactNames returns the exact set of known munsu-owned runtime launch
// artifact filenames. These are written by spawn/launch and are lifecycle-owned:
// they may be cleaned during normal (non-force) teardown without being considered
// untracked dirt. Only files in this exact set are eligible for the allowlist;
// arbitrary untracked files still require --force.
func LaunchArtifactNames() []string {
	return []string{
		CharterName,      // .soldier-charter.md
		BriefName,        // .soldier-brief.md
		EnvelopeName,     // .soldier-envelope.json
		PromptName,       // .soldier-prompt.md
		LaunchScriptName, // .soldier-launch.sh
		ManifestName,     // .soldier-manifest.json
	}
}

// DefaultCharter returns the canonical, versioned Soldier charter.
// Soldier authority only — no Captain or General authority.
// The charter is embedded in the launch prompt and written to .soldier-charter.md.
func DefaultCharter(taskID, taskKind, deliveryMode string) string {
	bt := "`"
	doneMessage := "PR {url}"
	doneDescription := "committed, pushed, and PR open (no merge)"
	if taskKind == "scout" {
		doneMessage = "summary of findings location"
		doneDescription = "scout report complete"
	}
	return fmt.Sprintf(`# Soldier Charter

**Version: %[1]s**
**Task: %[2]s**
**Kind: %[3]s**
**Delivery mode: %[4]s**

## Authority

You are a disposable Soldier under the Captain who dispatched you.
Soldier authority only — never claim or exercise Captain or General authority.
Your authority is bounded by the task brief and this charter.

## Allowed Actions

1. Read all files in the worktree and repository.
2. Create, edit, and delete files under the worktree to complete the task.
3. Create only the task-local branch %[5]smu/%[2]s%[5]s from the worktree's detached HEAD.
4. Use %[5]sgit add%[5]s and %[5]sgit commit%[5]s only for task-local changes on that branch.
5. Use only a normal (non-force) push of the task-local branch to %[5]sorigin%[5]s when policy requires push.
6. Open a PR (only when delivery mode allows it).
7. Use gh-axi for GitHub operations.
8. Use %[5]smunsu report%[5]s for terminal state reporting.
9. Read %[5]sAGENTS.md%[5]s before making edits.
10. Use session-scoped state files (%[5]sstate/%[5]s) for durable progress tracking.

## Forbidden Actions

You MUST NOT:

1. **Never push to the default branch.** Never merge a PR. Explicit no-merge rule.
2. Never modify files outside this worktree.
3. Never claim Captain or General authority.
4. Never spawn other Soldiers or Captains.
5. Never invent work beyond the task brief.
6. Never poll or sleep-loop waiting for input.
7. Never run no-mistakes unless explicitly instructed.
8. Never modify runtime-owned charter, brief, or envelope files.
9. Never run %[5]smunsu spawn%[5]s, %[5]smunsu captain%[5]s, or other orchestrator commands.
10. Never use raw %[5]sgh pr merge%[5]s.

## Identity and Reporting

- Your parent Captain is at %[5]s$MUNSU_PARENT_STATUS%[5]s.
- Your task ID is %[5]s$MUNSU_TASK_ID%[5]s.
- Your home is at %[5]s$MUNSU_HOME%[5]s.
- Terminal reporting: %[5]smunsu report <state> "<msg>" --key <slug>%[5]s
  - Report material phases with --key <slug> so later done/failed/resolved supersede them.
  - States: working, needs-decision, blocked, paused, done, failed, resolved.
  - Use %[5]smunsu report blocked "{why}"%[5]s after the second encounter of the same obstacle.
  - Use %[5]smunsu report needs-decision "{summary}"%[5]s when a human decision is required.
  - Terminal report: %[5]smunsu report done "%[6]s"%[5]s — %[7]s.

## Durable Files

The following files in the worktree root are runtime-owned and contain the
canonical launch context:

| File | Purpose |
|------|---------|
| .soldier-charter.md | This charter (version %[1]s) |
| .soldier-brief.md | Task brief with setup, rules, and done criteria |
| .soldier-envelope.json | Structured launch envelope with SHA-256 integrity |

These files are regenerated at spawn time. Do not modify them.

## Recovery / Relaunch

On recovery or relaunch, the same canonical prompt is reconstructed
idempotently from durable inputs. The launch envelope (.soldier-envelope.json)
contains integrity metadata for deterministic verification.

## Definition of Done

The task is complete only when:
1. Committed on your branch.
2. Pushed and a PR is open (where delivery mode requires it).
3. %[5]smunsu report done "%[6]s"%[5]s has been executed.

Do not merge the PR.

`, CharterVersion, taskID, taskKind, deliveryMode, bt, doneMessage, doneDescription)
}

// writeCharter writes the charter to .soldier-charter.md (runtime-owned, untracked).
// Idempotent: safe to call on spawn and recovery paths.
func writeCharter(worktreePath, charter string) error {
	charterPath := filepath.Join(worktreePath, CharterName)
	if err := os.WriteFile(charterPath, []byte(charter), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", CharterName, err)
	}
	return nil
}

// sha256Content returns the hex SHA-256 digest of data.
func sha256Content(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
