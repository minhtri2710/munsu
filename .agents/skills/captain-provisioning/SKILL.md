---
name: captain-provisioning
description: Manage the full captain lifecycle — seed, launch, retire, handoff, config-push, recover, update, migrate — following the idle-by-default charter contract. Use when provisioning, inspecting, or retiring a persistent domain supervisor (captain) from the fleet orchestrator side.
---

# captain-provisioning — captain lifecycle operations

Root virtue: **idempotency** (repeating the same lifecycle operation produces the same state).

## When to use

Provision, inspect, or retire a persistent domain supervisor (captain) from the fleet
orchestrator side: seed a home, launch a captain, hand off backlog items, sync
inheritable config, recover a captain, update its clone, or migrate it to a managed
worktree. Start with the overview, then load only the phase detail you need.

## Lifecycle overview

| Phase | CLI verb | Purpose |
|-------|----------|---------|
| Seed | `munsu captain seed <id> <home-path>` | Create home + directory tree + charter |
| Launch | `munsu captain launch <captain-home>` | Start pi agent in captain home |
| Retire | `munsu captain retire <captain-home>` | Kill process, optionally remove home |
| Handoff | `munsu captain handoff <captain-home> <item-keys...>` | Two-phase atomic task handover |
| Config-push | `munsu captain config-push <captain-home>` | Sync inheritable config from parent |
| Recover | `munsu captain recover <captain-id>` | Run the full recovery transaction for one captain |
| Update | `munsu captain update <captain-home>` | Safe local fast-forward of a captain clone, returning a typed outcome |
| Migrate | `munsu captain migrate <captain-home> <id> [--repo <path>]` | Transactional migrate from state-only home to managed git worktree (does not retire or relaunch a live captain) |

Source: `cmd/munsu/main.go` registers the CLI; `internal/cli/root.go` wires the captain verbs to the implementation in `internal/fleet/captain_captain.go` and `internal/fleet/captain_recover.go`.

## Safety & idempotency contract

- **Repeated operations are idempotent** — rerunning the same verb converges to the same state.
- **Retire refuses** while the captain home has in-flight soldiers (kind `ship|scout`); `--force` bypasses the check, use it only after proving blockers are stale or done.
- **Recover preserves a healthy captain**, relaunches a launched-but-dead captain, and leaves a seeded-only captain stopped (run `munsu captain launch <captain-home>` once to start it).
- **Migrate never retires or relaunches** — retire a live captain before migrating, then launch from the new worktree.
- **Config-push refuses** symlink escapes outside the captain home and writes to git-tracked destinations.
- **Every charter must encode the idle-by-default contract** — a captain reconciles only its own in-flight work, never self-initiates surveys, and treats an empty queue as healthy.

## Phase detail (load on demand)

- [REFERENCE.md](REFERENCE.md) — per-verb operations: seed, launch, retire, handoff,
  config-push, idle-by-default charter, recover (step list + launch behavior), update
  outcomes, migrate. Jump straight to a phase via anchors, e.g.
  [recover](REFERENCE.md#recover), [migrate](REFERENCE.md#migrate).
- [MIGRATION.md](MIGRATION.md) — full lifecycle migration sequence
  (Update -> Verify idle -> Retire -> Migrate -> Validate -> Recover -> Launch -> Converge/Guard).

## Validation / stop boundaries

- **Before retiring a live captain**, verify captain fleet state (`munsu captain list`),
  soldier state (`munsu soldier-state <id>`), pane liveness, and backlog truth — see
  [retire preflight](REFERENCE.md#retire). Never force-retire a captain with live work.
- **After `recover` on a seeded-only captain**, run `munsu captain launch <captain-home>`
  once; recovery intentionally leaves it stopped.
- **After migrating with `--repo`**, confirm the home exists at the new worktree path and
  the old home is backed up before continuing the sequence.
- Stop and check at each destructive step; partial failure reports (`ok`/`failed`/`skipped`)
  do not block the rest of a recovery transaction.

## See also

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill (spawn, supervise, teardown soldiers)
- `internal/fleet/captain_captain.go` and `internal/fleet/captain_recover.go` — authoritative Go lifecycle implementation
- `internal/cli/root.go` — CLI verb registration for `munsu captain *`
- `internal/harness/harness.go` — harness resolution chain for captain launch
- `munsu skill show captain-provisioning` — bundled lifecycle contract; use `munsu captain --help` for the current command surface.
