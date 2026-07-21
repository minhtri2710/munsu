# Firstmate Skill Workflow Audit

Audited against firstmate commit at `firstmate-8bf1b0/2/firstmate`.
Date: 2026-06-15.

## Evidence Table

| Workflow Step | Actor | Trigger | Skill | Selection Authority | Delivery Mechanism | Required/Optional | Failure Behavior | Munsu Equivalent |
|---|---|---|---|---|---|---|---|---|
| Session start diagnostics | firstmate | Session start digest | `bootstrap-diagnostics` | Agent-only (section 13), triggered by actionable diagnostic lines | Loaded via AGENTS.md when specific diagnostic prefixes appear | Required when diagnostic line present; otherwise not loaded | Diagnostic printed, session continues with degraded state | `internal/bootstrap/bootstrap.go` diagnostics |
| Spawn/recovery trust handshake | firstmate | Spawn or recovery of crewmate | `harness-adapters` | Agent-only (section 13), triggered before any spawn/recovery/trust/exit | Explicit AGENTS.md instruction: "Load before every spawn or recovery" | Required before every spawn | No spawn without adapter preflight | `internal/harness/` adapter registry + preflight |
| Harness adapter invocation | firstmate | Any harness-specific operation | `harness-adapters` | Agent-only, loaded at precise trigger points | Inline AGENTS.md section, loaded once per session | Required per-trigger | Agent proceeds without adapter knowledge | `harness.Preflight()` + `harness.GetAdapter()` |
| Project init/removal | captain (human) | Captain invokes project command | `project-management` | Agent-only, loaded at precise trigger | Loaded from `.agents/skills/` when trigger matches | Required before project mutation | Project mutation blocked | `internal/project/` package |
| Stuck crewmate recovery | firstmate | Dead endpoint or stale wake | `stuck-crewmate-recovery` | Agent-only (section 13), triggered by stale/looping state | Loaded when digest reports dead endpoint | Required per-recovery | Recovery attempt skipped | `internal/captain/captain.go` Recover/Retire |
| Secondmate provisioning | firstmate | Secondmate create/seed/validate/launch | `secondmate-provisioning` | Agent-only (section 13) | Loaded at precise trigger | Required for secondmate operations | Secondmate operation blocked | `internal/captain/` (Seed, Launch, Validate) |
| Task briefing | captain (human) | `/brief <id> <repo>` | Inline (fm-brief.sh) | Captain-invoked | `bin/fm-brief.sh` shell script | Required before spawn | No brief, cannot spawn | `internal/brief/brief.go` Scaffold |
| Crewmate spawn | captain (human) | `/spawn <id> <project>` | Inline (fm-spawn.sh) | Captain-invoked, harness resolved via dispatch | `bin/fm-spawn.sh` shell script | Required | Spawn fails with error output | `internal/spawn/spawn.go` Run |
| Backlog task state | firstmate/captain | Task lifecycle events | `tasks-axi` | AXI-CLI tool, controlled by firstmate | `tasks-axi list/show/start/done --file backlog.md` | Required for backlog management | Tasks-axi error blocks operation | `internal/backlog/` + `tasks-axi` |
| Decision hold lifecycle | firstmate | Investigation/visual review completion | `decision-hold-lifecycle` | Agent-only (section 13) | Loaded at precise trigger | Required for decision closure | Decision remains unresolved | `internal/task/` status tracking |
| AFK mode | captain (human) | `/afk` or state marker | `afk` | Captain-invoked | Loaded on `/afk` command or state detection | Optional | Daemon not started | `internal/afk/` daemon |
| Fleet sync | firstmate | Session start / converge cycle | Inline | Captain or human | `bin/fm-fleet-sync.sh` | Required at session start | Fleet state stale | `internal/captain/captain.go` Converge |
| Harness dispatch resolution | firstmate | Spawn without explicit --harness | `soldier-dispatch.json` | Config file (structured) | Dispatch config profiles match task description | Optional (falls back to default harness) | Default harness used | `internal/harness/dispatch.go` ResolveDispatchSelection |
| Git operations | crewmate | Task execution | `gh-axi` | AXI-CLI tool | `gh-axi` CLI commands | Required for GitHub operations | Blocked gh operations | `gh-axi` (reused) |
| Watch/process supervision | firstmate | Ensure watcher on spawn | Inline | Captain-invoked | `bin/fm-watch-ensure.sh` | Required for supervision | Watcher absent, no supervision | `internal/supervision/` watcher |
| Delivery preflight | firstmate/captain | Before spawn or PR | Inline | Config/skill-driven | `bin/fm-delivery-preflight.sh` | Required before no-mistakes | Preflight blocks delivery | `internal/delivery/preflight.go` |

## Key Findings for Munsu Soldier Skill Routing

### 1. Agent-only vs Captain-invoked

Firstmate distinguishes **agent-only** skills (loaded automatically at precise triggers per section 13 of AGENTS.md) from **captain-invoked** skills (loaded by explicit command). Munsu soldier skill routing should follow the same pattern: required skills are injected at spawn time (analogous to agent-only), while optional skills are available but not forced.

### 2. Harness-adapters loading pattern

Firstmate loads `harness-adapters` before every spawn, recovery, trust handshake, and harness-specific operation. The munsu equivalent (`internal/harness/` adapter registry + Preflight) runs the same validation. The critical insight: **adapter knowledge must be available before any harness CLI invocation**, exactly as `soldier.BuildLaunchArgs` does.

### 3. Skill selection authority

Firstmate's selection authority for agent-only skills is deterministic: the trigger text appears in AGENTS.md section 13. For dispatch profiles, structured JSON config (`config/crew-dispatch.json` / `soldier-dispatch.json`) is authoritative. **Never pane-prose keyword guessing** — structured config wins.

### 4. Skill delivery mechanism

Firstmate delivers skill content by loading from `.agents/skills/` when the trigger fires. For munsu soldier skills, the equivalent is:
- **Required skills**: Content inlined into the launch prompt (via `soldier.BuildLaunchPrompt` skill section)
- **Optional skills**: Listed in envelope, content loaded on demand
- This follows firstmate's pattern of loading skill content at the point of need.

### 5. Required/Optional failure behavior

Firstmate distinguishes:
- Agent-only skills: absence blocks the operation (required)
- Captain-invoked skills: human can proceed without
- Dispatch config: missing → fallback to default harness

Munsu soldier follows the same: required skills missing → fail-closed before session allocation; optional skills missing → durable diagnostic, proceed.

### 6. Section 13 agent-only skill triggers

All firstmate's section 13 skills fire on **exact trigger conditions** written in AGENTS.md, never on ambiguous keyword matching. Munsu soldier skill routing uses **explicit envelope declarations** plus **versioned task-kind/repo/mode/lifecycle policy** — never stale-pane keyword guessing. This matches firstmate's precision.

### 7. Verified native invocation

Firstmate uses native CLI tool invocation (`tasks-axi`, `gh-axi`, `quota-axi`) as the primary delivery mechanism for AXI tools. When a CLI is unavailable, the skill content is read from `.agents/skills/` directly. Munsu soldier uses the same pattern: verified harness adapters (native CLI) when supported, otherwise inline canonical required instructions or verified durable-loading strategy.
