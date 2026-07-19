---
name: second-provisioning
description: Manage the full second lifecycle — seed, launch, retire, handoff, config-push — following the idle-by-default charter contract. Use when provisioning, inspecting, or retiring a persistent domain supervisor (second) from the fleet orchestrator side.
---

# second-provisioning — second lifecycle operations

Root virtue: **idempotency** (repeating the same lifecycle operation produces the same state).

## Lifecycle overview

| Phase | CLI verb | Purpose |
|-------|----------|---------|
| Seed | `munsu second seed <id> <home-path>` | Create home + directory tree + charter |
| Launch | `munsu second launch <second-home>` | Start pi agent in second home |
| Retire | `munsu second retire <second-home>` | Kill process, optionally remove home |
| Handoff | `munsu second handoff <second-home> <item-keys...>` | Two-phase atomic task handover |
| Config-push | `munsu second config-push <second-home>` | Sync inheritable config from parent |

Source: `cmd/munsu/main.go` registers `newSecondCmd()` which adds all verbs. Implementation in `internal/second/second.go`.

---

## 1. Seed (`munsu second seed <id> <home-path>`)

Creates a new second home with the standard directory tree and writes a charter (AGENTS.md).

### Actions

1. Creates the home directory at `<home-path>`.
2. Creates subdirectories: `state/`, `data/`, `config/`, `projects/`.
3. Writes `AGENTS.md` (the charter — currently a minimal placeholder string).
4. Prints confirmation: `Seeded second <id> at <home-path>`.

### Directories

| Dir | Purpose |
|-----|---------|
| `state/` | Runtime state (lock file, meta, status, config-push log) |
| `data/` | Persistent domain data (seconds registry, learnings) |
| `config/` | Harness pin, dispatch profile, backlog backend |
| `projects/` | Per-project subdirectories for worktree isolation |

### Lease concept

The `AGENTS.md` charter **is** the lease: it defines what the second may do autonomously. The current seed writes a minimal placeholder. In production, the charter should encode the idle-by-default contract (see section 6).

```sh
munsu second seed my-monitor /var/munsu/seconds/my-monitor
# Seeded second my-monitor at /var/munsu/seconds/my-monitor
```

---

## 2. Launch (`munsu second launch <second-home>`)

Starts a pi agent process in the second's home with its AGENTS.md as the launch prompt.

### Harness resolution chain

The harness is resolved through `harness.Second()` (see `internal/harness/harness.go`):

1. `config/second-harness` file value (first-class second harness pin)
2. `config/crew-harness` file value (fallback — shared with crews)
3. `Detect()` (auto-detect from environment markers)

A value of `"default"` in either config file is treated as unset.

### Launch mechanics

- Currently **only the "pi" harness is supported** for second launch.
- Reads optional `model` from parent home config (`config/model`) for the `--model` flag.
- Resolves `pi` on PATH.
- Changes working directory to the second home.
- Runs: `pi [--model <model>] -- <second-home> "$(cat <second-home>/AGENTS.md)"`
- Prints: `Launched second <id> (pid <N>) in <home>`

```sh
# After seeding:
munsu second launch /var/munsu/seconds/my-monitor
# Launched second my-monitor (pid 73314) in /var/munsu/seconds/my-monitor
```

### Source references

- `internal/second/second.go` — `Launch()` function
- `internal/harness/harness.go` — `Second()` harness resolution, `Detect()` env detection
- `internal/second/second_test.go` — tests for harness pinning chain, fallback to crew-harness, fallback to Detect

---

## 3. Retire (`munsu second retire <second-home>`)

Tears down a running second. The CLI verb always passes `removeHome=false`.

### Safety checks

1. Reads PID from `<second-home>/state/.lock` file.
2. If PID is valid (> 0), calls `os.FindProcess(pid)` then `proc.Kill()`.
3. If no lock file or PID is 0, skips process kill.

### Home removal

The CLI currently always retains the home (`removeHome=false`). The `Retire()` function accepts a `removeHome` boolean for callers that want full cleanup.

```sh
munsu second retire /var/munsu/seconds/my-monitor
# Retired second at /var/munsu/seconds/my-monitor (home retained)
```

### --force flow

The `Retire()` Go function accepts `removeHome bool`. A hypothetical `--force` flag would pass `true`, removing the entire second home directory via `os.RemoveAll()`. This is not yet exposed in the CLI.

### Source references

- `internal/second/second.go` — `Retire()` reads lock file, finds/kills process, optionally removes home
- `internal/cli/root.go` — retire command passes `false` for removeHome

---

## 4. Handoff (`munsu second handoff <second-home> <item-keys...>`)

Atomically transfers backlog items from the parent home to a second.

### Two-phase protocol

**Phase 1 — Copy all.** For each item key:

1. Copy `<parent-home>/state/<key>.meta` to `<second-home>/state/<key>.meta`.
2. If a `.status` file exists for the key, copy it too.
3. If **any** copy fails, the function returns immediately with an error. No originals are removed.

**Phase 2 — Remove originals.** Only when all copies in phase 1 succeeded:

1. Remove `<parent-home>/state/<key>.meta`.
2. If a `.status` was copied, remove `<parent-home>/state/<key>.status`.
3. Print: `handed-off <key>` for each item.

Keys without a meta file are silently skipped (warning to stderr).

```sh
munsu second handoff /var/munsu/seconds/my-monitor task-001 task-002
```

### Source references

- `internal/second/second.go` — `Handoff()` two-phase protocol
- `internal/cli/root.go` — handoff command accepts variadic item keys

---

## 5. Config-push (`munsu second config-push <second-home>`)

Syncs inheritable configuration from the parent home to the second.

### Inheritable config list

Default list (order matters):

```
crew-harness
crew-dispatch.json
backlog-backend
```

Override via `MUNSU_INHERITABLE_CONFIG` env (colon-separated).

### Operations

1. **Mirror deletions**: for each inheritable file that exists in the second's `config/` but is absent from the parent's `config/`, delete it from the second.
2. **Copy present files**: for each inheritable file in the parent, copy it to the second's `config/`.
3. **Gitignore warning**: after each copy, runs `git check-ignore -q <dst>`. If the file is tracked (exit code 1), prints: `WARNING: <name> is tracked in second git — add it to .gitignore`.
4. **Logging**: all actions are appended to `<second-home>/state/config-push.log` with UTC timestamps in the format `<ts>  <action>  <name>`.

```sh
munsu second config-push /var/munsu/seconds/my-monitor
#   pushed crew-harness
#   pushed crew-dispatch.json
```

### Source references

- `internal/second/second.go` — `ConfigPush()`, `getInheritableList()`, `isInheritable()`
- `internal/cli/root.go` — config-push command

---

## 6. Idle-by-default charter

Every second charter (AGENTS.md) **must** encode the idle-by-default contract. This is mandatory — not optional.

### Contract

- **Reconcile only own in-flight work.** A second examines its own `state/` directory for meta files and status files. It does not look at other homes, the parent fleet state, or sibling seconds.
- **Never self-initiate surveys, audits, or proactive scans.** The second waits for handoff items or explicit directives. An empty queue is a healthy state — no action required.
- **Empty queue = healthy.** Silence is the expected baseline. The second should log only when it acts, not when it has nothing to do.

### Example charter header

```markdown
# Charter: <second-id>

## Domain
<description of the persistent concern this second owns>

## Contract
- I reconcile only in-flight work in my own state/ directory.
- I never self-initiate surveys, audits, or scans.
- An empty queue is healthy.
```

### Source references

- `internal/second/second.go` — `Seed()` writes a minimal placeholder charter; production charters must add this contract.

---

## See also

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill (spawn, supervise, teardown crews)
- `internal/second/second.go` — authoritative Go implementation of all lifecycle operations
- `internal/cli/root.go` — CLI verb registration for `munsu second *`
- `internal/harness/harness.go` — harness resolution chain for second launch
- `docs/port-mapping.md` — module map and port assignments
