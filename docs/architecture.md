# munsu architecture

munsu is a standalone Go CLI that ports firstmate's crew orchestration
capabilities to any project directory, without requiring a firstmate checkout.

## Module layout

```
cmd/munsu/main.go          Entrypoint — cobra command execution
internal/
  cli/                     Cobra command tree (root.go, init.go)
  home/                    Home directory resolution
  config/                  Flat key-file configuration
  project/                 Project registry (data/projects.md)
  worktree/                Treehouse worktree pool CLI wrapper
  harness/                 Agent harness detection + crew/secondmate resolution
  session/                 Session backend interface (tmux, herdr)
  task/                    Task meta read/write (state/<id>.meta + status)
  backlog/                 Task backlog via tasks-axi or manual fallback
  brief/                   Task brief scaffolding (ship/scout templates)
  crewstate/               Crewmate state reading (meta + pane liveness)
  teardown/                Crewmate teardown with safety checks
  delivery/                Review-diff, pr-check, pr-merge, merge-local
  fleet/                   Fleet sync, snapshot, view, bearings
  secondmate/              Secondmate lifecycle (seed, launch, retire, handoff)
  selfupdate/              Fast-forward self-update
  supervision/             Event-driven watcher loop (watch)
  waker/                   Wake queue (enqueue, drain, guard)
  lifecycle/               Timing/lock invariants (wake queue, watcher beat, lock)
  bootstrap/               Toolchain diagnostics and setup sweeps
  agentsmd/                AGENTS.md creation and update
  stow/                    Knowledge sweep (learnings, captain preferences)
  afk/                     Away-mode supervision daemon
  hometag/                 Home-tag namespace isolation helpers
  ghurl/                   GitHub URL parsing utility
```

Each package follows a deep-module pattern: small public surface area,
implementation detail behind. Cobra command wiring stays in `internal/cli/`;
business logic lives in the domain packages (`internal/backlog/`,
`internal/session/`, `internal/delivery/`, etc.).

## Package responsibilities

| Package | Responsibility |
|---------|---------------|
| `home` | Resolve `MUNSU_HOME` / `--home` / `~/.munsu` default; create directory tree |
| `config` | Read/write flat key files under `config/`; env override fallback |
| `project` | Parse `data/projects.md` registry; ad-hoc cwd detection |
| `worktree` | CLI wrapper around treehouse: get, return, status, isolation assertion |
| `harness` | Detect agent harness (env markers + process ancestry); resolve crew/secondmate adapters |
| `session` | `Backend` interface with tmux and herdr adapters |
| `task` | Meta read/write (`state/<id>.meta`), status append (`state/<id>.status`) |
| `backlog` | Task backlog via tasks-axi CLI or manual `data/backlog.md` fallback |
| `brief` | Scaffold ship/scout brief templates at `data/<id>/brief.md` |
| `crewstate` | Read crewmate state: meta + no-mistakes run-step + pane liveness + status log |
| `teardown` | Crewmate teardown with dirty/remote/report safety gates |
| `delivery` | Review-diff, pr-check, pr-merge, merge-local (no-mistakes axi helpers) |
| `fleet` | Sync, snapshot, view, bearings for project fleet |
| `secondmate` | Full persistent-domain-supervisor lifecycle |
| `selfupdate` | Fast-forward-only self-update binary |
| `supervision` | Event-driven watcher loop with singleton lock |
| `waker` | Durable wake queue (enqueue, drain, guard) |
| `lifecycle` | Wake queue parse/append/drain, watcher beat I/O, session lock |
| `bootstrap` | Toolchain diagnostics: MISSING, NEEDS_GH_AUTH, TANGLE, etc. |
| `agentsmd` | AGENTS.md creation, CLAUDE.md symlink, self-governance section |
| `stow` | Knowledge sweep with inspect-then-update dedup |
| `afk` | Away-mode sub-supervisor daemon |
| `hometag` | Namespace isolation for session backends |
| `ghurl` | GitHub URL parsing (owner, repo, PR number) |

## Home directory data model

Default home: `~/.munsu` (overridable via `MUNSU_HOME` env or `--home` flag).

```
~/.munsu/
  state/                   Task state + lock files
    <id>.meta              Task metadata (JSON key-value pairs)
    <id>.status            Status log (one line per append, "state: message")
    .lock                  Flock-based singleton lock (watcher)
    .last-watcher-beat     Liveness timestamp + PID
    .wake-queue            Durable wake event queue
  data/
    backlog.md             Manual backlog file (markdown format)
    projects.md            Project registry (one entry per line)
    <id>/                  Per-task briefs
      brief.md
  config/                  Flat key files
    backend                Default session backend (tmux|herdr)
    crew-harness           Override for crew harness
    secondmate-harness     Override for secondmate harness
    backlog-backend        Override for backlog backend (manual)
    crew-dispatch.json     Dispatch profile (model/effort per harness)
  projects/                Cloned project repositories
```

### State files

Task meta (`state/<id>.meta`): newline-delimited `key: value` pairs written
by `task.WriteMeta` and read by `task.ReadMeta`. Metadata keys include
`window`, `worktree`, `project`, `harness`, `model`, `effort`, `kind`, `mode`,
`yolo`, `pr`, `head_sha`, `created`, `updated`.

Task status (`state/<id>.status`): one `state: message` line per append.
States include `working`, `blocked`, `done`, `failed`, `needs-decision`.
Used by `crewstate.Read` to reconstruct the current task status.

### Configuration

Flat key files under `config/`. Read via `config.Get(homeDir, key)`, write
via `config.Set(homeDir, key, value)`. Environment overrides use the pattern
`MUNSU_<KEY>_OVERRIDE` (e.g. `MUNSU_BACKEND_OVERRIDE`). Resolution order is
flag > env override > config file > default.

## Key interfaces

### Session backend (`internal/session`)

```go
type Backend interface {
    NewWindow(session, name string) (windowID string, err error)
    SendKeys(windowID, text string) error
    Capture(windowID string, lines int) (string, error)
    Alive(windowID string) bool
    Teardown(windowID string) error
}
```

Two implementations: `tmux` (default) and `herdr` (enabled via `HERDR_ENV=1`).
The backend is selected through a resolution chain: `--backend` flag >
`config/backend` file > `HERDR_ENV` env var > `tmux` default. Unknown
backend names are rejected at `session.Resolve` time with a clear error.

Backend resolution: `session.Resolve(homeDir, backendOverride)` returns a
`(Backend, name, error)` tuple. The caller receives the resolved adapter
and its display name, then uses the adapter for all session operations.

Other backends (zellij, cmux, orca) were evaluated but not implemented:
- **zellij** — stable CLI but high implementation cost. Niche terminal multiplexer.
- **cmux** — requires running macOS GUI app and socket control setup. macOS-only, niche.
- **orca** — firstmate-specific terminal app.

### Task lifecycle

| Phase | Command | Domain package |
|-------|---------|---------------|
| Create | `munsu backlog add` / `munsu task add` | `backlog` / `task` |
| Brief | `munsu brief` | `brief` |
| Spawn | `munsu spawn` | `cli` (wires worktree + harness + session) |
| Supervise | `munsu watch` (bg: `munsu watch-arm`) | `supervision` |
| Interact | `munsu send`, `munsu peek`, `munsu crew-state` | `cli`, `crewstate` |
| Promote | `munsu promote` | `task` |
| Deliver | `munsu review-diff`, `munsu pr-check`, `munsu pr-merge`, `munsu merge-local` | `delivery` |
| Teardown | `munsu teardown` | `teardown` |

### Spawn flow

`munsu spawn <id> <project>` orchestrates six steps in sequence:

1. **Tangle check** — verifies the project checkout is not on a non-default
   branch in the primary checkout (skipped with `--yolo`)
2. **Worktree acquisition** — calls `worktree.Get` to obtain an isolated
   treehouse pool worktree
3. **Harness detection** — determines the calling agent harness (pi,
   claude-code, codex, etc.) via env markers and process ancestry
4. **Launch template resolution** — maps detected harness to a model/effort
   template from `config/crew-dispatch.json`
5. **Session creation** — resolves the backend (tmux/herdr) and opens a new
   terminal window for the crewmate
6. **Meta write** — persists task metadata (window, worktree, harness, model,
   project, kind, mode) to `state/<id>.meta`

### Supervision / watcher

`munsu watch` runs an event-driven poll loop every 5 seconds:
- Touches a liveness beat at `state/.last-watcher-beat`
- Scans all task meta files for pane liveness (via session backend) and status
  log staleness (>5 min idle)
- Detects stale streaks (3 consecutive stale polls triggers deep inspection)
- Emits wake reasons (stale, signal, done) into the wake queue
- Singleton-safe via flock at `state/.lock`

`munsu watch-arm` launches the watcher as a background child process.
`munsu wake-drain` consumes all queued wake records.
`munsu guard` checks watcher liveness and reports project tangles.

Stale absorption: tasks running in the no-mistakes pipeline (run-step
`running`, `fixing`, `ci`, `fix_review`, `awaiting_approval`) suppress stale
wakes even if the session pane appears idle.

### Backlog

`munsu backlog` subcommands (add, list, show, start, done, block, ready,
unblock) use a two-tier dispatch:
1. **tasks-axi CLI** (when available and compatible, >= 0.1.1)
2. **Manual fallback** — markdown file at `data/backlog.md` with markers
   `[ ]` (queued), `[-]` (in-flight), `[!]` (blocked), `[x]` (done)

Non-default home paths force the manual backend to prevent data leaks
between home scopes.

### Delivery pipeline

Three modes controlled by `--mode` on `munsu spawn`:
- **no-mistakes** (default) — automated code review, lint, test, PR via
  the no-mistakes daemon pipeline
- **direct-PR** — push and open PR directly, skip automated review
- **local-only** — merge to local default branch without a remote

Delivery subcommands: `munsu review-diff` (diff summary), `munsu pr-check`
(record PR + write merge-poll script), `munsu pr-merge` (merge via gh-axi),
`munsu merge-local` (fast-forward for local-only mode).

## Harness resolution

`harness.Detect()` checks environment markers first (`PI_AGENT_ACTIVE`,
`CLAUDE_CODE`, `CODEBOX_AGENT_SESSION`, `GEMINI_AGENT`, `OPENAI_AGENT`,
`ANTIGRAVITY_AGENT`, `ANTHROPIC_API_KEY`, etc.), then falls back to
process ancestry inspection.

`harness.Crew(homeDir)` — fallback chain: `crew-dispatch.json` default >
`config/crew-harness` > detected harness.
`harness.Secondmate(homeDir)` — fallback: `config/secondmate-harness` >
`config/crew-harness` > detected harness.

## Key design decisions

- **Deep modules, shallow interfaces** — each `internal/` package exposes a
  small public API (often 1-3 functions) with implementation detail behind.
- **Cobra-only dependency** — no framework, no DI container. Cobra is the sole
  Go dependency. This keeps the binary small and upgrades trivial.
- **No bash runtime** — the compiled binary has zero runtime dependencies
  on scripts or external tools beyond the agent harness.
- **Firstmate concept port, not fork** — munsu implements the same behavioral
  model as firstmate (crew orchestration, watcher, backlog, delivery) but as
  a standalone Go CLI with an explicit `--home` / `MUNSU_HOME` relocation
  model instead of firstmate's repo-root home.

## Mapping to firstmate

See `docs/port-mapping.md` for the full command-by-command mapping.

Key structural differences from firstmate:

| Aspect | firstmate | munsu |
|--------|-----------|-------|
| Entrypoint | Agent harness reads `AGENTS.md` in clone | `munsu` CLI binary on `PATH` |
| Home | Repo root (default) or `FM_HOME` | `~/.munsu` or `MUNSU_HOME` / `--home` |
| Language | Bash (`bin/fm-*.sh`) | Go (cobra) |
| Dispatcher | None (scripts called directly) | Single entrypoint with subcommands |
| Runtime dep on firstmate | N/A (is firstmate) | None (standalone) |

## Build and test

```sh
go build ./...
go vet ./...
go test ./...        # 345+ tests across 26 packages
```

Cobra is the only Go dependency. Zero runtime dependencies beyond the compiled
binary — no bash scripts, no external toolchain beyond what the agent harness
provides.
