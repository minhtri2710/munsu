---
name: captain-provisioning
description: Manage the full captain lifecycle — seed, launch, retire, handoff, config-push — following the idle-by-default charter contract. Use when provisioning, inspecting, or retiring a persistent domain supervisor (captain) from the fleet orchestrator side.
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

Source: `cmd/munsu/main.go` registers `newCaptainCmd()` which adds all verbs. Implementation in `internal/captain/captain.go`.

---

## 1. Seed (`munsu captain seed <id> <home-path>`)

Creates a new captain home with the standard directory tree and writes a charter (AGENTS.md).

### Actions

1. Creates the home directory at `<home-path>`.
2. Creates subdirectories: `state/`, `data/`, `config/`, `projects/`.
3. Writes `AGENTS.md` (the charter — currently a minimal placeholder string).
4. Prints confirmation: `Seeded captain <id> at <home-path>`.

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

## 3. Retire (`munsu captain retire <captain-home>`)

Tears down a running captain. The CLI verb always passes `removeHome=false`.

### Safety checks

1. Reads PID from `<captain-home>/state/.lock` file.
2. If PID is valid (> 0), calls `os.FindProcess(pid)` then `proc.Kill()`.
3. If no lock file or PID is 0, skips process kill.

### Home removal

The CLI currently always retains the home (`removeHome=false`). The `Retire()` function accepts a `removeHome` boolean for callers that want full cleanup.

```sh
munsu captain retire /var/munsu/captains/my-monitor
# Retired captain at /var/munsu/captains/my-monitor (home retained)
```

### --force flow

The `Retire()` Go function accepts `removeHome bool`. A hypothetical `--force` flag would pass `true`, removing the entire captain home directory via `os.RemoveAll()`. This is not yet exposed in the CLI.

### Source references

- `internal/captain/captain.go` — `Retire()` reads lock file, finds/kills process, optionally removes home
- `internal/cli/root.go` — retire command passes `false` for removeHome

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

Syncs inheritable configuration from the parent home to the general.

### Inheritable config list

Default list (order matters):

```
soldier-harness
soldier-dispatch.json
backlog-backend
```

Override via `MUNSU_INHERITABLE_CONFIG` env (colon-separated).

### Operations

1. **Mirror deletions**: for each inheritable file that exists in the general's `config/` but is absent from the parent's `config/`, delete it from the general.
2. **Copy present files**: for each inheritable file in the parent, copy it to the general's `config/`.
3. **Gitignore warning**: after each copy, runs `git check-ignore -q <dst>`. If the file is tracked (exit code 1), prints: `WARNING: <name> is tracked in captain git — add it to .gitignore`.
4. **Logging**: all actions are appended to `<captain-home>/state/config-push.log` with UTC timestamps in the format `<ts>  <action>  <name>`.

```sh
munsu captain config-push /var/munsu/captains/my-monitor
#   pushed soldier-harness
#   pushed soldier-dispatch.json
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

## See also

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill (spawn, supervise, teardown soldiers)
- `internal/captain/captain.go` — authoritative Go implementation of all lifecycle operations
- `internal/cli/root.go` — CLI verb registration for `munsu captain *`
- `internal/harness/harness.go` — harness resolution chain for captain launch
- `docs/port-mapping.md` — module map and port assignments
