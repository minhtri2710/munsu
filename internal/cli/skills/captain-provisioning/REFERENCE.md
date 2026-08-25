# captain-provisioning reference — per-verb operation details

Companion to `SKILL.md` (router). Load this file when you need the exact steps,
safety checks, or source references for a specific lifecycle verb. For the full
migration runbook see [MIGRATION.md](MIGRATION.md).

## Contents

- [Seed](#seed) — home + directory tree + charter
- [Launch](#launch) — harness resolution chain, launch mechanics
- [Retire](#retire) — safety checks, preflight requirements, `--force`
- [Handoff](#handoff) — two-phase atomic task handover
- [Config-push](#config-push) — inheritable config sync
- [Idle-by-default charter](#idle-by-default-charter) — mandatory contract
- [Recover](#recover) — recovery steps and launch behavior
- [Update](#update) — typed outcomes
- [Migrate](#migrate) — transactional worktree migration

---

## Seed

`munsu captain seed <id> <home-path>` creates a new captain home with the standard
directory tree and writes a charter (AGENTS.md).

### Actions

1. Creates the home directory at `<home-path>`.
2. Creates subdirectories: `state/`, `data/`, `config/`, `projects/`.
3. Writes `AGENTS.md` (default charter when empty, requiring parent home for return-channel path).
4. Writes the provenance marker (`.munsu-captain-home`).
5. When a parent home is known (CLI always passes General home): registers the captain and runs `ConfigPush` so inheritable config + `data/projects.md` are present immediately.
6. Prints confirmation: `Seeded captain <id> at <home-path>`.

### Directories

| Dir | Purpose |
|-----|---------|
| `state/` | Runtime state (lock file, meta, status, config-push log) |
| `data/` | Persistent domain data (captains registry, learnings) |
| `config/` | Harness pin, dispatch profile |
| `projects/` | Per-project subdirectories for worktree isolation |

### Lease concept

The `AGENTS.md` charter **is** the lease: it defines what the general may do autonomously. The current seed writes a minimal placeholder. In production, the charter should encode the idle-by-default contract (see below).

```sh
munsu captain seed my-monitor /var/munsu/captains/my-monitor
# Seeded captain my-monitor at /var/munsu/captains/my-monitor
```

---

## Launch

`munsu captain launch <captain-home>` starts a pi agent process in the general's
home with its AGENTS.md as the launch prompt.

### Harness resolution chain

The harness is resolved through `harness.Captain()` (see `internal/harness/harness.go`):

1. `config/captain-harness` file value (first-class captain harness pin)
2. `config/soldier-harness` file value (fallback — shared with soldiers)
3. `Detect()` (auto-detect from environment markers)

A value of `"default"` in either config file is treated as unset.

### Launch mechanics

- Currently **only the "pi" harness is supported** for captain launch.
- Reads optional `model` from parent home config (`config/model`) for the `--model` flag.
- Resolves `pi` on PATH.
- Changes working directory to the general home.
- Runs: `pi [--model <model>] -- <captain-home> "$(cat <captain-home>/AGENTS.md)"`
- Prints: `Launched captain <id> (pid <N>) in <home>`

```sh
# After seeding:
munsu captain launch /var/munsu/captains/my-monitor
# Launched captain my-monitor (pid 73314) in /var/munsu/captains/my-monitor
```

### Source references

- `internal/fleet/captain_captain.go` — `Launch()` function
- `internal/harness/harness.go` — `Captain()` harness resolution, `Detect()` env detection
- `internal/fleet/captain_captain_test.go` — tests for harness pinning chain, fallback to soldier-harness, fallback to Detect

---

## Retire

`munsu captain retire <captain-home> [--force]` tears down a running captain: kills
the process, clears parent meta, and unregisters from `data/captains.md`. Home
directory is retained.

### Safety checks

1. Validates the captain home exists and has a valid provenance marker.
2. Reads PID from `<captain-home>/state/.lock` file.
3. If PID is valid (> 0), calls `os.FindProcess(pid)` then `proc.Kill()`.
4. If no lock file or PID is 0, skips process kill.
5. Clears parent meta and unregisters from `data/captains.md`.

### Preflight requirements

Before retiring a live captain, verify:

- **Captain fleet state** — `munsu captain list` shows the captain is registered.
- **Soldier-state** — `munsu soldier-state <id>` confirms no in-flight soldiers.
- **Pane liveness** — Confirm the captain's session pane is not actively processing.
- **Backlog truth** — Check `data/captains.md` and `state/` for pending work.

Retire **refuses** if the captain home has in-flight soldiers (kind `ship|scout`).

### --force

`--force` bypasses the in-flight soldier check. Use only after proving blockers
are stale or done — do not force-retire a captain with live work.

```sh
munsu captain retire /var/munsu/captains/my-monitor --force
```

---

## Handoff

`munsu captain handoff <captain-home> <task-ids...>` atomically transfers queued
tasks from the parent home to a captain.

### Two-phase protocol

**Phase 1 — Copy all.** For each item key:

1. Resolve the key's platform durable stem, then copy `<parent-home>/state/<durable-stem>.meta` to `<captain-home>/state/<durable-stem>.meta`.
2. If a `.status` file exists for the key, copy the matching `<durable-stem>.status` too.
3. If **any** copy fails, the function returns immediately with an error. No originals are removed.

**Phase 2 — Remove originals.** Only when all copies in phase 1 succeeded:

1. Remove `<parent-home>/state/<durable-stem>.meta`.
2. If a `.status` was copied, remove `<parent-home>/state/<durable-stem>.status`.
3. Print: `handed-off <key>` for each item.

Keys without a meta file are silently skipped (warning to stderr).

```sh
munsu captain handoff /var/munsu/captains/my-monitor task-001 task-002
```

### Source references

- `internal/fleet/captain_captain.go` — `Handoff()` two-phase protocol
- `internal/cli/root.go` — handoff command accepts variadic item keys

---

## Config-push

`munsu captain config-push <captain-home>` syncs inheritable configuration from the
parent (General) home to the captain. Also used automatically on seed (with parent)
and pre-launch.

### Inheritable config list

Default list (order matters):

```
soldier-harness
soldier-dispatch.json
```

Override via `MUNSU_INHERITABLE_CONFIG` env (colon-separated).

### Also pushed (always, not env-listed)

| Path | Behavior |
|------|----------|
| `data/general-shared.md` | Copy read-only (`0444`); mirror-delete if absent on parent |
| `data/projects.md` | Byte-copy of General project registry; absolute path descriptions stay valid without cloning into captain `projects/`; mirror-delete if absent on parent |

### Operations

1. **Mirror deletions**: for each inheritable file that exists in the captain's `config/` but is absent from the parent's `config/`, delete it from the captain.
2. **Copy present files**: for each inheritable file in the parent, copy it to the captain's `config/`.
3. **Push shared + projects registry**: `data/general-shared.md` and `data/projects.md` as above.
4. **Safety**: refuses symlink escape outside the captain home; refuses writes to git-tracked destinations.
5. **Logging**: all actions are appended to `<captain-home>/state/config-push.log` with UTC timestamps in the format `<ts>\t<action>\t<name>`.

```sh
munsu captain config-push /var/munsu/captains/my-monitor
#   pushed soldier-harness
#   pushed soldier-dispatch.json
#   pushed projects.md
```

### Source references

- `internal/fleet/captain_captain.go` — `ConfigPush()`, `getInheritableList()`, `isInheritable()`
- `internal/cli/root.go` — config-push command

---

## Idle-by-default charter

Every captain charter (AGENTS.md) **must** encode the idle-by-default contract. This is mandatory — not optional.

### Contract

- **Reconcile only own in-flight work.** A captain examines its own `state/` directory for meta files and status files. It does not look at other homes, the parent fleet state, or sibling captains.
- **Never self-initiate surveys, audits, or proactive scans.** The general waits for handoff items or explicit directives. An empty queue is a healthy state — no action required.
- **Empty queue = healthy.** Silence is the expected baseline. The general should log only when it acts, not when it has nothing to do.

### Example charter header

```markdown
# Charter: <captain-id>

## Domain
<description of the persistent concern this captain owns>

## Contract
- I reconcile only in-flight work in my own state/ directory.
- I never self-initiate surveys, audits, or scans.
- An empty queue is healthy.
```

### Source references

- `internal/fleet/captain_captain.go` — `Seed()` writes a minimal placeholder charter; production charters must add this contract.

---

## Recover

`munsu captain recover <captain-id>` runs the full recovery transaction for one
captain. Each step reports `ok`/`failed`/`skipped` so partial failures do not block
the whole recovery.

### Recovery steps

1. **Provenance** — validate captain registration and home structure.
2. **Config-validation** — check that inheritable config exists.
3. **Integration-status** — verify the captain's harness integration.
4. **Charter-refresh** — re-read AGENTS.md and refresh instructions.
5. **Config-push** — sync inheritable config from parent.
6. **Launch-readiness** — verify the launch path is viable.
7. **Relaunch-pane** — restart the captain's session pane and prove post-launch liveness; refuses a duplicate relaunch while the relaunch guard is armed.
8. **Watcher-ensure** — ensure the watcher is running.
9. **Legacy transport guard** — check for legacy transport artifacts.
10. **Terminal-reconcile** — reconcile pending terminal receipts.
11. **Nudge-retry** — retry any unreachable nudge markers.

### Launch behavior

`recover` preserves a healthy live Captain; observing liveness also clears any
armed relaunch guard. Recovery is strict-dead-only: only authoritative pane
absence (`backend.ErrPaneNotFound`) qualifies as dead, and only that evidence
permits the `relaunch-pane` step to launch. Pane-present/no-agent, transitional
or unproven agent statuses (`Starting`, `Unknown`, `Unresponsive`,
`StaleIdentity`, `Unresolved`), generic probe errors, and unproven plain
`Alive=false` are not authoritative absence: the step fails closed without
launching. If a previously launched Captain is dead, the `relaunch-pane` step
starts it again and then polls the captain endpoint to prove post-launch
liveness (up to ~10s). A relaunch whose liveness cannot be
proven arms a 5-minute relaunch guard (`relaunch_liveness=unproven` plus
`relaunch_guard_until` in the captain task meta); while the guard is armed,
recovery refuses a duplicate relaunch until it expires. If the Captain was
seeded but never launched, that step is skipped and the Captain remains
stopped; run `munsu captain launch <captain-home>` once to start it.

```sh
munsu captain recover my-captain
# Healthy endpoint: no launch action; any armed relaunch guard is cleared.
# Launched but dead: relaunched and liveness proven; otherwise the step fails
# and duplicate relaunches are refused while the guard is armed.
# Seeded only: run `munsu captain launch <home>` after recovery.
```

---

## Update

`munsu captain update <captain-home>` performs a safe local fast-forward of a
captain clone, returning a typed outcome:

- `already-current` — already at the latest default-branch commit.
- `fast-forwarded` — successfully advanced.
- `state-only-skipped` — state-only home (no git worktree) skipped.
- `dirty` — uncommitted changes exist; cannot advance.
- `diverged` — local branch has diverged from remote; cannot advance.
- `offline` — worktree path is not reachable.
- `wrong-remote` — remote does not match the parent's remote.
- `wrong-branch` — not on the default branch.
- `invalid-provenance` — home does not have a valid provenance marker.

```sh
munsu captain update /var/munsu/captains/my-monitor
# outcome: fast-forwarded
```

---

## Migrate

`munsu captain migrate <captain-home> <id> [--repo <path>]` migrates a captain home
to a managed git worktree.

### Without --repo

Writes a provenance marker to a legacy state-only home (simple).

### With --repo

Performs a transactional migration from state-only home to a managed git
worktree, preserving operational dirs (`state/`, `config/`, `data/`, `projects/`).

- **Atomic:** on failure the original home is restored and a rollback marker
  (`.migration-rollback`) is written.
- **On success:** the old home is backed up at `<home-path>.backup-<timestamp>`.
- **Does NOT retire or relaunch:** `migrate --repo` only creates the worktree
  and migrates operational directories. The captain process is untouched.
  Retire the live captain before migrating, then launch from the new worktree.

```sh
munsu captain migrate /var/munsu/captains/my-monitor my-captain --repo /path/to/repo
# Migrated captain my-captain to managed worktree at /path/to/repo/.treehouse/...
# Old home backed up at /var/munsu/captains/my-monitor.backup-<ts>
# Captain was not retired. Run retire before migrate if the captain is live.
```
