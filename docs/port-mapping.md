# Port mapping: munsu commands / Go packages

This document maps munsu commands and Go packages to the capabilities they implement.
All commands are fully implemented unless marked otherwise.

The table distinguishes **NOMINAL** presence (the munsu CLI has the subcommand/package)
from **NATIVE** integration (opt-in harness hooks/artifacts installed via
`munsu integrate install --harness <X>`). Native integration is only available
for harnesses with a verified adapter; unverified harnesses show "planned/unsupported".

> **agy (Antigravity):** munsu detects `agy` as a verified harness adapter (NOMINAL command presence).
> See `internal/harness/harness.go` entry `Agy` and `docs/skills/harness-adapters.md` for launch template.
> NATIVE integration (PreToolUse safety-check, turn-end Stop guard, session nudge) is **supported** via agy's
> first-class hook system (`.agents/hooks.json` / `~/.gemini/config/hooks.json` / plugin dir) -- install with
> `munsu integrate install --harness agy`. Contract + unit verified.
> **Live validation (2026-07-19):** `agy` binary v1.1.4 at `/opt/homebrew/bin/agy`. Pre-auth confirmed — `agy models`
> lists Gemini/Claude models; `agy --print "say hello"` returns response headless (TTY-free). The harness is
> **pre-auth ready and functional for headless --print mode**. Live spawn verification (real pane reaching
> harness-ready through tmux/herdr) remains deferred until a full lifecycle step completes.
>
> **ExtraArgs shell-quoting (F10.1):** `munsu spawn` shell-quotes `ExtraArgs` values to prevent word-splitting when the harness backend (tmux/herdr) interprets the command string.

| Capability | munsu command | munsu Go package | Status |
|---|---|---|---|
| Home directory | `munsu home` | `internal/home` | **implemented** |
| Task lifecycle (canonical aggregate: Create/Start/Block/Unblock/Complete/Reopen/Supersede/Retire/Promote) | `munsu task add/start/done/block/unblock/reopen`, `munsu promote`, retirement flows | `internal/taskauthority` (canonical `Canonical` over `internal/home` durable mechanics), `internal/cli`/`internal/fleet` (composition) | **implemented** |
| Task meta + status records (post-commit projections written after authoritative commits; `.status` append-only, ADR-0007 §7) | `munsu task add/show/status`; current via `soldier-state` | `internal/taskauthority` (authoritative source), `internal/home` (generic `.meta`/`.status` primitives), `internal/fleet`/`internal/cli` (projection writers) | **implemented** |
| Dispatch control (holds/interpretation/decision) | `munsu decision-hold hold/complete/verify/resolve/list`, spawn and supervision flows | `internal/taskauthority` | **implemented** |
| Worktree/endpoint binding + spawn confirmation | `munsu spawn` | `internal/taskauthority` (`BindWorktree`, launch operations), `internal/fleet` | **implemented** |
| Delivery invariants (prepare/complete, merge attempt, issue-link reconcile, attestation, git authorization) | `munsu delivery pr-merge/merge-status` | `internal/taskauthority` (invariant ops), `internal/fleet` (orchestration) | **implemented** |
| Handoff receipt (`ReceiveTransfer`) | `munsu captain handoff` | `internal/fleet` (journaled saga), `internal/taskauthority` (`ReserveTransfer`/`CommitTransfer`/`ReceiveTransfer`/`ActivateTransfer`) | **implemented** |
| Send message to soldier | `munsu send` | `internal/cli`, `internal/fleet`, `internal/home` (durable mailbox) | **implemented** (typed mailbox envelope/pending/ack; reconciliation through CLI-composed lifecycle ports) |
| Spawn soldier | `munsu spawn` | `internal/cli`, `internal/fleet` | **implemented** |
| Brief soldier | `munsu brief` | `internal/fleet` | **implemented** |
| Teardown soldier context | `munsu teardown` | `internal/orchestrator` | **implemented** |
| Peek at soldier output | `munsu peek` | `internal/cli` | **implemented** |
| Soldier state query | `munsu soldier-state` | `internal/fleet` | **implemented** |
| Promote soldier task | `munsu promote` | `internal/taskauthority` (`Canonical.Promote`), `internal/cli` | **implemented** |
| Harness detection/verification | `munsu harness detect/soldier/captain` | `internal/harness` | **implemented** |
| Project mode | `munsu project mode` | `internal/fleet` | **implemented** |
| Fleet sync | `munsu fleet sync` | `internal/fleet` | **implemented** |
| Fleet snapshot | `munsu fleet snapshot` | `internal/fleet` | **implemented** |
| Fleet view | `munsu fleet view` | `internal/fleet` | **implemented** |
| Fleet bearings | `munsu fleet bearings` | `internal/fleet` | **implemented** |
| Bootstrap diagnostics | `munsu bootstrap` | `internal/bootstrap` | **implemented** |
| Self-update | `munsu update` | `internal/cli` | **implemented** |
| Session start | `munsu session-start` | `internal/cli`, `internal/orchestrator` | **implemented** |
| Watch soldier | `munsu watch` | `internal/orchestrator` | **implemented** |
| Arm watcher | `munsu watch-arm` | `internal/cli` | **implemented** |
| Wake claim / resolve / ack | `munsu wake claim` / `munsu wake resolve` / `munsu wake ack` | `internal/orchestrator` | **implemented** |
| Guard supervision | `munsu guard` | `internal/cli` | **implemented** |
| Stow skill | `munsu stow` | `internal/cli` | **implemented** |
| Ensure AGENTS.md | `munsu ensure-agents-md` | `internal/cli` | **implemented** |
| Project registry | `munsu project add/list/show/rm` | `internal/fleet` | **implemented** |
| Review diff | `munsu delivery review-diff` | `internal/fleet` | **implemented** |
| PR merge and terminal reconciliation | `munsu delivery pr-merge` | `internal/fleet`, `internal/taskauthority` (invariants) | **implemented**: terminal reconciliation is mutation-free; current GitHub/GitLab adapters intentionally fail closed for OPEN mutation because they cannot atomically enforce mergeability plus the exact authorized head and base |
| Worktree pool (treehouse) | `munsu worktree get/return/status` | `internal/backend`, `internal/cli` | **implemented** |
| Config | `munsu config get/set` | `internal/config` | **implemented** |
| Session backend (tmux + herdr + zellij + cmux + orca) | `--backend` flag | `internal/backend` | **implemented** (cmux/orca experimental, alongside zellij). Structured `munsu backend capabilities` currently exposes only `tmux` and `herdr`. |
| Endpoint observation (BEO-16/P1a) | typed orthogonal probe | `internal/backend` | **implemented**: `Lifecycle`/`Responsiveness`/`Freshness`/`Activity`/`Source`/`Incarnation`/`Detail`; `Backend.Alive` and the typed `EndpointObservation.Alive()` seam both removed — every adapter exposes a structured `CheckAlive`/`CheckAgentAlive`. Adapters never fabricate freshness/incarnation. Fleet splits the authority: `authorizeAbsence` (negative — narrow dead/current exact absence revalidated under current generation/revision/fence) vs `authorizeLive` (positive — requires explicit acquisition evidence tied to the incarnation; P1a adapters cannot attest, so a probe with no acquisition record is never `Live()`; no dispose/relaunch on ambiguous). Retirement closes the probe→dispose TOCTOU window with an authoritative `CurrentLocked` re-read/compare-and-fence after the probe and immediately before Dispose/worktree-return/projection removal, PLUS a durable canonical `CleanupClaim` committed atomically with the retire: while the claim is active, Reopen/BindEndpoint/acquisition fail closed (the post-unlock interval between a fence and the external action is protected by the durable claim, not the released lock); the claim is reconciled by CompleteCleanup (normal), AbortCleanup (operator escape hatch, `task cleanup-abort`; TERMINAL — never resumed after abort, so an old teardown retry cannot reactivate a historical claim on a reopened generation), and idempotent BeginCleanup (crash resume only; exact-fenced on retired phase/generation/evidence identity); delivery mutations and every Begin/Complete/Abort path are claim-fenced even on idempotency paths; conflict → `cleanup-pending`, nothing released. Native busy/event transport is P1b (Herdr `proposed`, not claimed current). |
| Dispatch profiles | `config/soldier-dispatch.json` | `internal/harness` | **implemented** |
| Home init | `munsu init` | `internal/cli` | **implemented** |
| AFK away-mode supervision | munsu afk | internal/orchestrator | **implemented** (Go-native, full lifecycle -- see `docs/skills/afk.md`) |
| Captain lifecycle | `munsu captain seed/launch/retire/list/handoff/config-push` | `internal/fleet` with `internal/cli` adapters | **implemented** |
| Native harness integration | `munsu integrate install/repair/status` | `internal/bootstrap` | **implemented** (Pi, Claude, Grok, Codex, OpenCode, agy adapters verified by contract + unit tests; Pi + agy runtime-verifiable locally; Claude/Grok/Codex/OpenCode deferred until installed) |

## Architecture overview

Munsu is a compiled Go CLI (built with `github.com/spf13/cobra`) replacing what was
originally a collection of shell scripts invoked directly by the agent harness. The key
architectural shifts:

| Aspect | Predecessor approach | munsu |
|---|---|---|
| Entrypoint | Agent harness reads `AGENTS.md` in clone | `munsu` CLI binary on `PATH` |
| Home | Repo root (default) or environment variable | `~/.munsu` (default) or `MUNSU_HOME` / `--home` |
| Language | Bash scripts | Go (`github.com/spf13/cobra`) |
| Dispatcher | Harness calls scripts directly by path | Single `munsu` entrypoint dispatching subcommands |
| Runtime dependency | Full checkout required | Standalone binary |
| Orchestrator manual | Repo's AGENTS.md | Scaffolded by `munsu init` |

## Scope boundaries

These capabilities are deliberately excluded from munsu -- they are
platform-specific infrastructure that do not belong in a standalone soldier-orchestrator CLI:

| Capability | Rationale |
|---|---|
| Social media integration | Social-platform interaction; not part of soldier lifecycle. |
| Turn-end guards | **Munsu has NATIVE integration** (opt-in via `munsu integrate install --harness <X>`) for six harnesses:<br>  - **Pi** (live-verified: in-process session-start, wake-followup, turnend-guard, pretool-check, scope-gate)<br>  - **Claude** (contract+unit verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **Grok** (contract+unit verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **Codex** (contract+unit verified: session-start nudge, turn-end Stop guard, pretool-check)<br>  - **OpenCode** (contract+unit verified: session-start nudge, turn-end guard, pretool-check, session.idle watch-arm)<br>  - **agy** (contract+unit verified: PreToolUse safety-check, turn-end Stop guard, PreInvocation nudge via `.agents/hooks.json`)<br>All non-Pi adapters are contract + unit verified (harnesses not installed locally, or headless mode does not exercise tool calls); only Pi is live-verified (runtime). Agy pre-auth confirmed locally; full pane lifecycle deferred. Pull-based watcher diagnostics remain available via `munsu watch` / `munsu guard`. |
| Composer mode | Multi-agent composition; munsu spawns 1:1 soldiers. |
| Command policies | Per-harness ARM/CD gating; not in munsu's generic model. |
| Classification / Gate-Refuse library | Logic is inline in munsu (`--kind`, cobra validation) -- no extracted library needed. |
| Transition library | State transitions encoded in cobra command relationships rather than a library. |
| Supervision instruction templates | Watcher emits structured wake reasons instead of instruction templates. |
| Herdr lab | Developer tooling for Herdr experimentation; not a production capability. |
| `captain retire --remove-home` | Deliberate safety choice -- home directory persists by default. |

> **Design principle:** Munsu ports the soldier lifecycle model, not every
> script from its predecessor. Capabilities that are platform-specific (social,
> single-harness-only, or experimental tooling) are intentionally excluded.
> Gaps that affect core lifecycle parity are tracked in the wave roadmap.

## Live harness validation (2026-07-19)

Environment probe (PATH):

| Binary | Present | Live spawn/hook runtime | Status |
|--------|---------|-------------------------|--------|
| pi | yes | yes (prior lifecycle + spawn ready) | **live-verified** |
| agy | yes | pre-auth confirmed (`agy models` lists models; `--print` works headless); full pane lifecycle deferred | **installed, pre-auth ready; live spawn deferred** (contract/unit verified) |
| claude | no | n/a | **deferred — not installed** |
| codex | no | n/a | **deferred — not installed** |
| grok | no | n/a | **deferred — not installed** |
| opencode | no | n/a | **deferred — not installed** |

Policy: do not claim live-verified without a real pane reaching harness-ready and completing a munsu lifecycle step.
