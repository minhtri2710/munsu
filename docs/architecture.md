# munsu architecture

munsu is a standalone Go CLI for soldier orchestration capabilities, usable from
any project directory without requiring a specific checkout.

## Module layout

```text
cmd/munsu/main.go       Entrypoint — Cobra command execution
internal/
  cli/                  Composition root, Cobra commands, self-update, stow, AGENTS.md helpers
  fleet/                Captain, spawn, backlog, delivery, soldier state, project registry
  orchestrator/         Watcher, AFK, wake delivery, lifecycle and turn-end coordination
  home/                 Home resolution plus durable task/meta/status storage mechanics
  backend/              Session adapters, endpoint observation, worktree and home-tag mechanics
  domain/               Pure business rules and value types, including delivery acceptance
  harness/              Harness detection, verified adapters and dispatch profiles
  bootstrap/            Toolchain diagnostics and native harness integration
  config/               Flat key-file configuration
  configmigration/      Configuration migration mechanics
  testutil/             Shared test helpers
```

Each module follows a deep-module pattern: a small interface hides implementation
detail. `internal/cli` is the composition root; business lifecycle rules remain
in their authoritative module rather than in command wiring.

## Module responsibilities

| Module | Responsibility |
|---|---|
| `cli` | Wire Cobra commands to modules; own CLI-local self-update, stow, AGENTS.md and output helpers |
| `fleet` | Orchestrate Captain lifecycle, Soldier spawn/state, backlog, project registry and delivery operations |
| `orchestrator` | Coordinate supervision, AFK, wakes, turn-end obligations and cross-process lifecycle |
| `home` | Resolve the munsu home and implement durable task aggregate, meta, status and binding storage |
| `backend` | Provide tmux/herdr/zellij/cmux/orca adapters, endpoint observation, worktree and home-tag mechanics |
| `domain` | Own pure business rules and value types such as `PR.CanMerge` and `Review.IsApproving` |
| `harness` | Detect and verify harnesses; resolve launch templates and dispatch profiles |
| `bootstrap` | Diagnose toolchain readiness and install, repair or inspect native harness integration |
| `config` | Read and write flat config files with environment override fallback |
| `configmigration` | Plan and apply configuration schema migration |

The command-to-module mapping is maintained in
[`docs/port-mapping.md`](port-mapping.md).

## Home directory data model

Default home: `~/.munsu` (overridable via `MUNSU_HOME` or `--home`).

```text
~/.munsu/
  state/                   Task state, projections, locks, wakes and receipts
    <id>.meta              Task metadata projection
    <id>.status            Append-only status/event projection
  data/
    backlog.md             Manual backlog fallback
    projects.md            Project registry
    captains.md            Captain registry
    <id>/brief.md          Per-task brief
  config/                  Harness, backend, dispatch and project settings
  projects/                Project worktrees
  captains/                Captain homes
```

### Task state

Task meta and status files are durable projections implemented by
`internal/home`. Status is append-only and is not the sole current-state
authority: consumers fold the stream and combine it with structured task,
run-step and endpoint evidence. Task lifecycle authority is being separated from
filesystem mechanics under ADR-0007; until that cutover, callers must use the
existing semantic helpers rather than invent another projection writer.

### Configuration

Flat key files live under `config/`. `internal/config` resolves values in this
order: explicit flag, environment override, config file, default. Dispatch
profiles live in `config/soldier-dispatch.json` and are interpreted by
`internal/harness` and the spawn orchestration in `internal/fleet`.

## Key interfaces and seams

### Session backend (`internal/backend`)

`internal/backend` hides terminal-specific behavior behind session and endpoint
operations. Verified implementations include tmux and herdr; zellij, cmux and
orca are explicit experimental adapters. Resolution rejects unknown backends
rather than silently selecting an unverified implementation.

### Delivery acceptance (`internal/domain`)

`internal/domain/domain.go` is the single owner of `PR`, `Review`, `CheckRun`,
`PR.CanMerge`, and `Review.IsApproving`. `internal/fleet/delivery_*.go` owns
provider interaction, identity capture, authorization and delivery orchestration.

### Captain lifecycle (`internal/fleet`)

`internal/fleet/captain_captain.go` owns seed, launch, retire, handoff,
config-push, update and migration. `captain_recover.go` owns the recovery
transaction. CLI adapters in `internal/cli` compose verified harness, backend and
integration capabilities into those operations.

### Supervision and wake delivery (`internal/orchestrator`)

`internal/orchestrator` owns watcher and AFK coordination plus the wake-delivery
module. `DeliverWake` handles Soldier-side status, receipt, event and wake
coordination; `ReconcilePending` handles Captain-side relay, acknowledgement and
obligation closure. Durable filesystem primitives remain behind `internal/home`
and adjacent orchestrator lifecycle files.

## Task lifecycle

| Phase | Command | Authoritative module |
|---|---|---|
| Create / backlog | `munsu task add`, `munsu backlog` | `internal/home`, `internal/fleet` |
| Brief | `munsu brief` | `internal/fleet` |
| Spawn | `munsu spawn` | `internal/fleet`, composed by `internal/cli` |
| Supervise | `munsu watch`, `munsu watch-arm`, `munsu afk` | `internal/orchestrator` |
| Interact | `munsu send`, `munsu peek`, `munsu soldier-state` | `internal/cli`, `internal/fleet`, `internal/home` |
| Deliver | `munsu delivery ...` | `internal/fleet`, with acceptance rules in `internal/domain` |
| Teardown | `munsu teardown` | `internal/cli`, `internal/fleet`, `internal/orchestrator` |

## Rank hierarchy and identity

General orchestrates the fleet. A Captain is a persistent domain supervisor
implemented by `internal/fleet`; a Soldier executes one bounded task. Runtime
identity is declared through `MUNSU_ROLE=general|captain|soldier`. Captain labels
use `captain-<id>-<hometag>`, windows use `mu-captain-<id>`, provenance is stored
in `.munsu-captain-home`, and registration lives in `data/captains.md`.

## Architectural enforcement

`internal/testutil/architecture_policy_test.go` enforces the allowed package topology
and forbidden import directions. When adding or moving a module, update that gate
in the same change and run:

```sh
go build ./...
go vet ./...
go test ./...
```
