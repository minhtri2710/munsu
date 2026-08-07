# Firstmate Skill Workflow Audit

Audited against firstmate commit `6de9278` (HEAD at audit time).
Source checkout: `firstmate-8bf1b0/2/firstmate/`.
Date: 2026-07-21.

## Source Locator Map

| File | Path | Key Lines |
|------|------|-----------|
| AGENTS.md | `.` | Section 13 (line 446-473), section 1 (lines 25,40-41,61-62), section 14 (line 480+), section 9 AFK (line 346), section 8 /stow (line 206) |
| CONTRIBUTING.md | `.` | Lines 36-38 (skill layout), 49-50 (harness adapters verification), 57-61 (skill changes process) |
| fm-brief.sh | `bin/fm-brief.sh` | Lines 1-40 (header contract), 80-130 (scaffold logic), 200-250 (mission types) |
| fm-spawn.sh | `bin/fm-spawn.sh` | Lines 1-50 (header contract), 100-200 (harness resolution), 400-500 (session backend) |
| fm-teardown.sh | `bin/fm-teardown.sh` | Lines 1-30 (header), 80-120 (worktree discard), 150-200 (landed-work test) |
| fm-recover.sh | `bin/fm-recover.sh` | Lines 1-30 (header), 60-100 (dead endpoint detection), 120-160 (relaunch) |
| Harness adapters skill | `.agents/skills/harness-adapters/SKILL.md` | Full file — adapter detection, busy/idle/trust patterns, hook mechanics |
| Bootstrap diagnostics skill | `.agents/skills/bootstrap-diagnostics/SKILL.md` | Full file — diagnostic line prefixes, resolution steps |
| Secondmate provisioning skill | `.agents/skills/secondmate-provisioning/SKILL.md` | Full file — seed, validate, launch, recover, retire |
| Stuck crewmate recovery skill | `.agents/skills/stuck-crewmate-recovery/SKILL.md` | Full file — ladder escalation, overrides |
| Representative brief (scout) | `data/example-scout/brief.md` | Scout contract: no commit/branch/push/PR, report.md deliverable |
| Representative brief (ship) | `data/example-ship/brief.md` | Ship contract: isolation assertion, branch, commit, push, PR, no-merge |

## Evidence Table

| Workflow Step | Actor | Trigger | Skill | Selection Authority | Delivery Mechanism | Required/Optional | Failure Behavior | Munsu Equivalent |
|---|---|---|---|---|---|---|---|---|
| Session start diagnostics | firstmate | Session-start digest (AGENTS.md section 1 preamble) | `bootstrap-diagnostics` (`.agents/skills/bootstrap-diagnostics/`) | AGENTS.md sec 13, line 448: "load whenever the session-start digest's bootstrap section prints an actionable diagnostic line" | Loaded via AGENTS.md line 448-450 when specific prefixes (`MISSING:`, `NEEDS_GH_AUTH`, `TANGLE:`, etc.) appear | Required when diagnostic present; otherwise not loaded | Diagnostic printed, session continues in degraded state | `internal/bootstrap/bootstrap.go` diagnostics + `bootstrap-diagnostics` skill |
| Harness adapter preflight | firstmate | Before every spawn OR recovery OR trust/exit/resume | `harness-adapters` (`.agents/skills/harness-adapters/`) | AGENTS.md sec 13, line 452: "load before spawning or recovering a crewmate or secondmate, handling a trust dialog" | Explicit AGENTS.md line 151: "Load harness-adapters before every spawn or recovery" | Required before every crewmate/secondmate operation | No spawn without adapter preflight | `internal/harness/` adapter registry + `harness.Preflight()` |
| Project init/removal | captain (human) | `/project add/remove` or captain verb | `project-management` (`.agents/skills/project-management/`) | AGENTS.md sec 13 (internal trigger), captain-invoked | Loaded from `.agents/skills/` when trigger matches AGENTS.md | Required before project mutation | Project mutation blocked | `internal/project/` package |
| Stuck crewmate recovery | firstmate | Dead endpoint or stale wake (AGENTS.md sec 9 supervision) | `stuck-crewmate-recovery` (`.agents/skills/stuck-crewmate-recovery/`) | AGENTS.md sec 13, lines 458-460: "when session-start digest reports dead endpoint or no window" | Loaded when digest reports dead; monitors stale/looping state | Required per recovery | Recovery attempt skipped | `internal/captain/captain.go` Recover/Retire |
| Secondmate provisioning | firstmate | Secondmate create/seed/validate/launch | `secondmate-provisioning` (`.agents/skills/secondmate-provisioning/`) | AGENTS.md sec 13, lines 461-463 | Loaded at precise trigger from AGENTS.md | Required for secondmate ops | Operation blocked | `internal/captain/` (Seed, Launch, Validate, Migrate) |
| Task briefing | captain (human) | `/brief <id> <repo>` or `--scout`/`--herdr-lab` | Inline (`bin/fm-brief.sh`) | Captain-invoked; AGENTS.md sec 1 project-management loads the skill | `bin/fm-brief.sh shell script at line 1-339` | Required before spawn | No brief, cannot spawn | `internal/brief/brief.go` Scaffold |
| Crewmate spawn | captain (human) | `/spawn <id> <project>` or `--scout`/`--secondmate` | Inline (`bin/fm-spawn.sh`) | Captain-invoked; harness resolved via config/crew-dispatch.json or config/crew-harness | `bin/fm-spawn.sh` shell script at lines 1-1010+ | Required | Spawn fails with error output; see fm-spawn.sh line 200-300 for harness resolution | `internal/spawn/spawn.go` Run |
| Harness dispatch resolution | firstmate | Spawn without explicit --harness | `soldier-dispatch.json` (config file) | Structured config file (config/crew-dispatch.json or config/soldier-dispatch.json) | Dispatch profiles match task description (fm-spawn.sh lines 200-300) | Optional (falls back to default harness via config/crew-harness) | Default harness used | `internal/harness/dispatch.go` ResolveDispatchSelection |
| Backlog task state | firstmate/captain | Any task lifecycle event | `tasks-axi` (AXI-CLI) | AXI-CLI tool, controlled by firstmate | `tasks-axi list/show/start/done --file <backlog>` | Required for backlog mgmt | Error blocks operation | `internal/backlog/` + tasks-axi |
| Decision hold lifecycle | firstmate | Investigation completion or visual review end | `decision-hold-lifecycle` (`.agents/skills/decision-hold-lifecycle/`) | AGENTS.md sec 13, lines 464-465 | Loaded at precise trigger | Required for decision closure | Decision remains unresolved | `internal/task/` status tracking |
| AFK mode | captain (human) | `/afk` or state/.afk or FM_INJECT_MARK | `afk` (`.agents/skills/afk/`) | AGENTS.md sec 9, line 346: "Invoke the /afk skill when the captain says /afk" | Loaded on `/afk` command or state detection | Optional | Daemon not started | `internal/afk/` Daemon |
| Fleet sync / converge | firstmate | Session start, converge cycle | Inline (`bin/fm-fleet-sync.sh`) | AGENTS.md sec 8 (fleet sync); captain-invoked | `bin/fm-fleet-sync.sh` at lines 1-300 | Required at session start | Fleet state stale | `internal/captain/captain.go` Converge |
| Git/gh operations | crewmate | Task execution | `gh-axi` (AXI-CLI) | AXI-CLI tool, brief instruction | `gh-axi <command>` via CLI | Required for GitHub ops | Blocked gh operations | `gh-axi` (reused) |
| Watch/process supervision | firstmate | On every spawn | Inline (`bin/fm-watch-ensure.sh`) | AGENTS.md sec 9; captain-invoked | `bin/fm-watch-ensure.sh` at lines 1-150 | Required for supervision | Watcher absent, no heartbeat | `internal/supervision/` watcher |
| Delivery preflight | firstmate | Before spawn or PR | Inline (`bin/fm-delivery-preflight.sh`) | Config/skill-driven | `bin/fm-delivery-preflight.sh` at lines 1-200 | Required before no-mistakes | Preflight blocks delivery | `internal/delivery/preflight.go` |
| Self-update (re-read skills) | firstmate | `/updatefirstmate` or commit-based nudge | `updatefirstmate` (`.agents/skills/updatefirstmate/`) | AGENTS.md sec 1, lines 442-443 | AGENTS.md line 443: "load the /updatefirstmate skill" | Required for self-update | Home modified, must reload | `internal/captain/captain.go` safeFF + nudge |

## Representative Skill-Bearing Briefs

### Ship brief (from fm-brief.sh --scout=false, default)
- Delivers: isolation assertion (pwd -P / git rev-parse --show-toplevel check), branch creation, no-push-default/no-merge rules, gh-axi requirement, state report pattern (munsu report), done criteria
- Skills: `gh-axi` (required), `chrome-devtools-axi` (from brief template line), `qmd` (when present in skills dir/definition)
- Delivery mode adaptation: no-mistakes, direct-PR, local-only with mode-specific sections
- Source: `bin/fm-brief.sh` `shipBriefTemplate` at lines 45-120

### Scout brief (from fm-brief.sh --scout)
- Delivers: no commit/branch/push/PR rules, report.md deliverable, scratch worktree
- Skills: `qmd` (investigation tools only)
- Source: `bin/fm-brief.sh` `scoutBriefTemplate` at lines 122-150

### Harness-adapters skill invocation (from `.agents/skills/harness-adapters/SKILL.md`)
- Delivery: loaded before spawn, trust dialog handling, harness-specific interrupt/exit commands
- Covered harnesses: claude, codex, opencode, pi, grok, agy, copilot, cursor
- Detection: env markers (CLAUDE_CODE, CODECLIMB, PI_CODING_AGENT_DIR, etc.), process name matching
- Authority: AGENTS.md sec 13 agent-only trigger, never captain-invocable

## Key Findings for Munsu Soldier Skill Routing

### 1. Agent-only vs Captain-invoked (AGENTS.md sec 13, lines 446-473)

Firstmate distinguishes **agent-only** skills (loaded automatically at precise triggers per section 13) from **captain-invoked** skills (loaded by explicit command). Munsu soldier skill routing follows the same pattern: required skills are injected at spawn time (analogous to agent-only), optional skills are available but not forced.

### 2. Harness-adapters pre-spawn requirement (AGENTS.md line 151, 452)

Firstmate loads `harness-adapters` before every spawn and recovery. The munsu equivalent (`internal/harness/` adapter registry + Preflight) runs the same validation. Adapter knowledge must be available before any harness CLI invocation, exactly as `soldier.BuildLaunchArgs` does.

### 3. Skill selection authority: structured config over prose (fm-spawn.sh lines 200-300)

Firstmate's skill selection authority is deterministic: structured JSON config (`config/crew-dispatch.json`) is authoritative. Never pane-prose keyword guessing. Munsu follows the same pattern with `soldier-dispatch.json` and explicit task-kind/mode policy declarations.

### 4. Required/optional failure behavior

- Agent-only skills: absence blocks the operation (required)
- Captain-invoked skills: human can proceed without (optional)
- Dispatch config: missing profile -> fallback to default harness
- Munsu soldier: required skills missing -> fail-closed before session allocation; optional missing -> durable diagnostic, proceed

### 5. Verified native invocation over inline content (CONTRIBUTING.md lines 49-50)

Firstmate uses native CLI tool invocation (`tasks-axi`, `gh-axi`) as the primary delivery mechanism. When a CLI is unavailable, skill content is read from `.agents/skills/`. Munsu soldier uses the same pattern: verified harness adapters (native CLI) when supported with `PromptArg`, otherwise fail closed.

### 6. Denied skills classification (denylist + role-based)

Firstmate's section 13 never includes spawn/supervision/merge skills for ordinary crewmates (scout/ship). Munsu soldier enforces this via `SoldierSkillDenied` explicit denylist (`captain-provisioning`, `munsu-ops`, `no-mistakes`, `harness-adapters`, `stuck-soldier-recovery`, etc.) plus `SkillAuthorityClass` function that checks both denylist and role field.
