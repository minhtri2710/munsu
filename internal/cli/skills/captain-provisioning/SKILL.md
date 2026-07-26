---
name: captain-provisioning
description: Manage the full captain lifecycle — seed, launch, retire, handoff, config-push, recover, update, migrate — following the idle-by-default charter contract. Use when provisioning, inspecting, or retiring a persistent domain supervisor (captain) from the fleet orchestrator side.
---

# captain-provisioning — captain lifecycle operations

Root virtue: **idempotency** (repeating the same lifecycle operation produces the same state).

## Lifecycle overview

| Phase | CLI verb | Purpose |
|-------|----------|---------|
| Seed | `munsu captain seed <id> <home-path>` | Create home + directory tree + charter |
| Launch | `munsu captain launch <captain-home>` | Start pi agent in captain home |
| Retire | `munsu captain retire <captain-home>` | Kill process, optionally remove home |
| Handoff | `munsu captain handoff <captain-home> <item-keys...>` | Two-phase atomic task handover |
| Config-push | `munsu captain config-push <captain-home>` | Sync inheritable config from parent |
| Recover | `munsu captain recover <captain-id>` | Run the full recovery transaction for one captain: provenance, config-validation, integration-status, charter-refresh, config-push, launch-readiness, relaunch-pane, watcher-ensure, legacy transport guard, terminal-reconcile, nudge-retry |
| Update | `munsu captain update <captain-home>` | Safe local fast-forward of a captain clone, returning a typed outcome |
| Migrate | `munsu captain migrate <captain-home> <id> [--repo <path>]` | Transactional migrate from state-only home to managed git worktree (does not retire or relaunch a live captain) |

Source: `cmd/munsu/main.go` registers `newCaptainCmd()` which adds all verbs. Implementation in `internal/captain/captain.go`.

---

## 1. Seed (`munsu captain seed <id> <home-path>`)

Creates a new captain home with the standard directory tree and writes a charter (AGENTS.md).

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
| `config/` | Harness pin, dispatch profile, backlog backend |
| `projects/` | Per-project subdirectories for worktree isolation |

### Lease concept

The `AGENTS.md` charter **is** the lease: it defines what the general may do autonomously. The current seed writes a minimal placeholder. In production, the charter should encode the idle-by-default contract (see section 6).

```sh
munsu captain seed my-monitor /var/munsu/captains/my-monitor
# Seeded captain my-monitor at /var/munsu/captains/my-monitor
```

---

## 2. Launch (`munsu captain launch <captain-home>`)

Starts a pi agent process in the general's home with its AGENTS.md as the launch prompt.

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

- `internal/captain/captain.go` — `Launch()` function
- `internal/harness/harness.go` — `Captain()` harness resolution, `Detect()` env detection
- `internal/captain/captain_test.go` — tests for harness pinning chain, fallback to soldier-harness, fallback to Detect

---

## 3. Retire (`munsu captain retire <captain-home> [--force]`)

Tears down a running captain: kills the process, clears parent meta, and unregisters
from `data/captains.md`. Home directory is retained.

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

## 4. Handoff (`munsu captain handoff <captain-home> <item-keys...>`)

Atomically transfers backlog items from the parent home to a captain.

### Two-phase protocol

**Phase 1 — Copy all.** For each item key:

1. Copy `<parent-home>/state/<key>.meta` to `<captain-home>/state/<key>.meta`.
2. If a `.status` file exists for the key, copy it too.
3. If **any** copy fails, the function returns immediately with an error. No originals are removed.

**Phase 2 — Remove originals.** Only when all copies in phase 1 succeeded:

1. Remove `<parent-home>/state/<key>.meta`.
2. If a `.status` was copied, remove `<parent-home>/state/<key>.status`.
3. Print: `handed-off <key>` for each item.

Keys without a meta file are silently skipped (warning to stderr).

```sh
munsu captain handoff /var/munsu/captains/my-monitor task-001 task-002
```

### Source references

- `internal/captain/captain.go` — `Handoff()` two-phase protocol
- `internal/cli/root.go` — handoff command accepts variadic item keys

---

## 5. Config-push (`munsu captain config-push <captain-home>`)

Syncs inheritable configuration from the parent (General) home to the captain.
Also used automatically on seed (with parent) and pre-launch.

### Inheritable config list

Default list (order matters):

```
soldier-harness
soldier-dispatch.json
backlog-backend
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

- `internal/captain/captain.go` — `ConfigPush()`, `getInheritableList()`, `isInheritable()`
- `internal/cli/root.go` — config-push command

---

## 6. Idle-by-default charter

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

- `internal/captain/captain.go` — `Seed()` writes a minimal placeholder charter; production charters must add this contract.

---

## 7. Recover (`munsu captain recover <captain-id>`)

Runs the full recovery transaction for one captain. Each step reports
`ok`/`failed`/`skipped` so partial failures do not block the whole recovery.

### Recovery steps

1. **Provenance** — validate captain registration and home structure.
2. **Config-validation** — check that inheritable config exists.
3. **Integration-status** — verify the captain's harness integration.
4. **Charter-refresh** — re-read AGENTS.md and refresh instructions.
5. **Config-push** — sync inheritable config from parent.
6. **Launch-readiness** — verify the launch path is viable.
7. **Relaunch-pane** — restart the captain's session pane.
8. **Watcher-ensure** — ensure the watcher is running.
9. **Legacy transport guard** — check for legacy transport artifacts.
10. **Terminal-reconcile** — reconcile pending terminal receipts.
11. **Nudge-retry** — retry any unreachable nudge markers.

### Seeded but not launched

`recover` leaves a recovered captain **seeded but not launched** — the home
structure, charter, and config are restored, but the captain process is not
automatically started. Run `munsu captain launch <captain-home>` to start it.

```sh
munsu captain recover my-captain
# Recover captain my-captain: provenance ok, config-validation ok, ...
# Captain is seeded but not launched. Run `munsu captain launch <home>` to start.
```

---

## 8. Update (`munsu captain update <captain-home>`)

Performs a safe local fast-forward of a captain clone, returning a typed
outcome:

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

## 9. Migrate (`munsu captain migrate <captain-home> <id> [--repo <path>]`)

Migrates a captain home to a managed git worktree.

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

---

## 10. Full lifecycle migration sequence

When moving a captain to a new home or upgrading its infrastructure:

1. **Update** — `munsu captain update <captain-home>` to ensure the captain is
   on the latest instruction surface.
2. **Verify idle** — Confirm no in-flight soldiers via `munsu captain list`,
   `munsu soldier-state <id>`, pane liveness, and backlog.
3. **Retire** — `munsu captain retire <captain-home>` to stop the captain
   process and clear parent meta. Use `--force` only after proving blockers
   are stale/done.
4. **Migrate** — `munsu captain migrate <captain-home> <id> --repo <path>`
   to create the managed worktree. This does NOT retire or relaunch the live
   captain, so verify the retire step was already completed.
5. **Validate worktree and backup** — Confirm the migrated home exists at
   the new worktree path and the old home is backed up.
6. **Launch** — `munsu captain launch <captain-home>` to start the captain
   process in the new home.
7. **Recover** — `munsu captain recover <captain-id>` to run the full recovery
   transaction (recover leaves the captain **seeded but not launched**).
8. **Converge / Guard** — `munsu captain converge` then `munsu guard` to
   reconcile state and verify fleet health.

---

## See also

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill (spawn, supervise, teardown soldiers)
- `internal/captain/captain.go` — authoritative Go implementation of all lifecycle operations
- `internal/cli/root.go` — CLI verb registration for `munsu captain *`
- `internal/harness/harness.go` — harness resolution chain for captain launch
- `docs/self-hosting.md` — self-hosting manual for munsu
- `munsu skill show captain-provisioning` — bundled lifecycle contract; use `munsu captain --help` for the current command surface.
