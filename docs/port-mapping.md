# Port mapping: firstmate concepts -> munsu commands / Go packages

This document maps firstmate scripts and concepts to their munsu equivalents.
All commands are fully implemented unless marked otherwise.

The table distinguishes **NOMINAL** presence (the munsu CLI has the subcommand/package)
from **NATIVE** integration (opt-in harness hooks/artifacts installed via
`munsu integrate install --harness <X>`). Native integration is only available
for harnesses with a verified adapter; unverified harnesses show "planned/unsupported".

> **agy (Antigravity):** munsu detects `agy` as a verified harness adapter (NOMINAL command presence).
> See `internal/harness/harness.go` entry `Agy` and `docs/skills/harness-adapters.md` for launch template.
> NATIVE integration (PreToolUse safety-check, turn-end Stop guard, session nudge) is **supported** via agy's
> first-class hook system (`.agents/hooks.json` / `~/.gemini/config/hooks.json` / plugin dir) -- install with
> `munsu integrate install --harness agy`. Contract + unit verified (headless `--print` does not exercise tool calls).
>
> **ExtraArgs shell-quoting (F10.1):** `munsu spawn` shell-quotes `ExtraArgs` values to prevent word-splitting when the harness backend (tmux/herdr) interprets the command string. This matches firstmate's `printf %q` behavior.

| firstmate concept / script | munsu command | munsu Go package | Status |
|---|---|---|---|
| `FM_HOME` / `~/.firstmate` | `munsu home` | `internal/home` | **implemented** |
| Task meta + status protocol | `munsu task add/show/status` | `internal/task` | **implemented** (list: delegated to tasks-axi) |
| `bin/fm-send.sh` | `munsu send` | `internal/cli` | **implemented** |
| `bin/fm-spawn.sh` | `munsu spawn` | `internal/cli` | **implemented** |
| `bin/fm-brief.sh` | `munsu brief` | `internal/brief` | **implemented** |
| `bin/fm-teardown.sh` | `munsu teardown` | `internal/teardown` | **implemented** |
| `bin/fm-peek.sh` | `munsu peek` | `internal/cli` | **implemented** |
| `bin/fm-soldier-state.sh` | `munsu soldier-state` | `internal/soldierstate` | **implemented** |
| `bin/fm-promote.sh` | `munsu promote` | `internal/task` | **implemented** |
| `bin/fm-harness.sh` | `munsu harness detect/soldier/captain` | `internal/harness` | **implemented** |
| `bin/fm-project-mode.sh` | `munsu project mode` | `internal/project` | **implemented** |
| `bin/fm-fleet-sync.sh` | `munsu fleet sync` | `internal/fleet` | **implemented** |
| `bin/fm-fleet-snapshot.sh` | `munsu fleet snapshot` | `internal/fleet` | **implemented** |
| `bin/fm-fleet-view.sh` | `munsu fleet view` | `internal/fleet` | **implemented** |
| `bin/fm-bearings-snapshot.sh` | `munsu fleet bearings` | `internal/fleet` | **implemented** |
| `bin/fm-bootstrap.sh` | `munsu bootstrap` | `internal/bootstrap` | **implemented** |
| `bin/fm-update.sh` | `munsu update` | `internal/selfupdate` | **implemented** |
| `bin/fm-session-start.sh` | `munsu session-start` | `internal/session` | **implemented** |
| `bin/fm-watch.sh` | `munsu watch` | `internal/supervision` | **implemented** |
| `bin/fm-watch-arm.sh` | `munsu watch-arm` | `internal/cli` | **implemented** |
| `bin/fm-wake-drain.sh` | `munsu wake-drain` | `internal/waker` | **implemented** |
| `bin/fm-guard.sh` | `munsu guard` | `internal/cli` | **implemented** |
| Stow skill (`.agents/skills/stow`) | `munsu stow` | `internal/stow` | **implemented** |
| `bin/fm-ensure-agents-md.sh` | `munsu ensure-agents-md` | `internal/agentsmd` | **implemented** |
| Project registry | `munsu project add/list/show/rm` | `internal/project` | **implemented** |
| Backlog (tasks-axi + manual fallback) | `munsu backlog` | `internal/backlog` | **implemented** |
| `bin/fm-review-diff.sh` | `munsu delivery review-diff` | `internal/delivery` | **implemented** |
| PR check/merge | `munsu delivery pr-check` / `munsu delivery pr-merge` | `internal/delivery` | **implemented** |
| Local merge | `munsu delivery merge-local` | `internal/delivery` | **implemented** |
| Worktree pool (treehouse) | `munsu worktree get/return/status` | `internal/worktree` | **implemented** |
| Config | `munsu config get/set` | `internal/config` | **implemented** |
| Session backend (tmux + herdr) | `--backend` flag | `internal/session` | **implemented** (future backends: experimental -- see docs) |
| Dispatch profiles | `config/soldier-dispatch.json` | `internal/harness` | **implemented** |
| Home init / init | `munsu init` | `internal/cli` | **implemented** |
| AFK away-mode supervision | munsu afk | internal/afk | **implemented** (Go-native, full lifecycle -- see `docs/skills/afk.md`) |
| Self-update | `munsu update` | `internal/selfupdate` | **implemented** |
| Captain lifecycle | `munsu captain seed/launch/retire/list/handoff/config-push` | `internal/captain` | **implemented** |
| Native harness integration | `munsu integrate install/repair/status` | `internal/integrate` | **implemented** (Pi, Claude, Grok, Codex, OpenCode, agy adapters verified by contract + unit tests; only Pi is runtime-verifiable -- the other harnesses are not installed locally or headless mode does not exercise tool calls) |

## Structural differences from firstmate

| Aspect | firstmate | munsu |
|---|---|---|
| Entrypoint | Agent harness reads `AGENTS.md` in clone | `munsu` CLI binary on `PATH` |
| Home | Repo root (default) or `FM_HOME` | `~/.munsu` (default) or `MUNSU_HOME` / `--home` |
| Language | Bash (bin/fm-*.sh) | Go (github.com/spf13/cobra) |
| Dispatcher | None (harness calls bin/fm-*.sh directly) | Single `munsu` entrypoint dispatching subcommands |
| Runtime dep on firstmate | N/A (is firstmate) | No (standalone) |
| Orchestrator manual | Repo's AGENTS.md | Scaffolded by `munsu init` (Wave C2) |

## Firstmate-only intentional gaps

These firstmate capabilities are deliberately not ported to munsu -- they are
firstmate-specific infrastructure that do not belong in a standalone CLI port:

| Capability | firstmate scripts | Rationale |
|---|---|---|
| X/Twitter integration | `fm-x-*.sh` (6 scripts) | Social-media interaction; firstmate-specific, not part of soldier lifecycle. |
| Turn-end guards | `fm-turnend-guard.sh` | **Munsu has NATIVE integration** (opt-in via `munsu integrate install --harness <X>`) for six harnesses:<br>  - **Pi** (verified: in-process session-start, wake-followup, turnend-guard, pretool-check, scope-gate)<br>  - **Claude** (verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **Grok** (verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **Codex** (verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **OpenCode** (verified: session-start nudge, turn-end guard, pretool-check, session.idle watch-arm)<br>  - **agy** (verified: PreToolUse safety-check, turn-end Stop guard, PreInvocation nudge via `.agents/hooks.json`)<br>All non-Pi adapters are contract + unit verified (harnesses not installed locally, or headless mode does not exercise tool calls); only Pi is runtime-verified. Legacy pull-based watcher diagnostics remain available via `munsu watch` / `munsu wake-drain` / `munsu guard`. |
| Composer mode | `fm-composer-lib.sh` | Multi-agent composition; munsu spawns 1:1 soldiers. |
| Codex command policies | `fm-arm-command-policy.mjs` | Codex-specific ARM/CD gating; per-harness policies not in munsu's generic model. |
| Classification / Gate-Refuse library | `fm-classify-lib.sh`, `fm-gate-refuse-lib.sh` | Logic is inline in munsu (`--kind`, cobra validation) -- no extracted library needed. |
| Transition library | `fm-transition-lib.sh` | State transitions encoded in cobra command relationships rather than a library. |
| Supervision instructions | `fm-supervision-instructions.sh` | Watcher emits structured wake reasons instead of instruction templates. |
| Herdr lab | `fm-herdr-lab.sh` | Developer tooling for Herdr experimentation; not a production capability. |
| `captain retire --remove-home` | `fm-teardown.sh` | Deliberate safety choice -- home directory persists by default. |

> **Design principle:** Munsu ports the soldier lifecycle model, not every firstmate
> script. Capabilities that are firstmate-specific (social, Codex-only, or experimental
> tooling) are intentionally excluded. Gaps that affect core lifecycle parity are
> tracked in the wave roadmap.


## Live harness validation (2026-07-19)

Environment probe (PATH):

| Binary | Present | Live spawn/hook runtime | Status |
|--------|---------|-------------------------|--------|
| pi | yes | yes (prior lifecycle + spawn ready) | **live-verified** |
| agy | yes | nested-pane ready patterns often absent without pre-auth; fail-closed after 60s | **installed; live spawn deferred** (contract/unit verified) |
| claude | no | n/a | **deferred — not installed** |
| codex | no | n/a | **deferred — not installed** |
| grok | no | n/a | **deferred — not installed** |
| opencode | no | n/a | **deferred — not installed** |

Policy: do not claim live-verified without a real pane reaching harness-ready and completing a munsu lifecycle step.
