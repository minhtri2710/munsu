# munsu — project agent memory

This file is the conventions file for crewmates working on munsu.
(Not the orchestrator operating manual — that's scaffolded by `munsu init`.)

## Build / test / lint

```sh
go build ./...       # build all packages
go vet ./...         # static analysis
go test ./...        # run all tests
```

Delivery mode: no-mistakes (push through the gate, never to `origin` directly).

## Module map

The full munsu module map lives in the port-mapping table at `docs/port-mapping.md`.

Current layout:
- `cmd/munsu/main.go` — entrypoint
- `internal/cli/root.go` — cobra command tree
- `internal/cli/init.go` — init command (home tree + starter config)
- `internal/home/` — home resolution and dir tree creation
- `internal/config/` — config read/write (flat key files + MUNSU_*_OVERRIDE env)
- `internal/project/` — project registry (data/projects.md parsing, ad-hoc cwd detection)
- `internal/worktree/` — treehouse CLI wrapper: get/return/status + isolation assertion
- `internal/harness/` — harness detection (env markers + process ancestry), crew/secondmate resolution, launch templates, dispatch profiles
- `internal/session/` — session backend (Backend interface + tmux/herdr adapters; unknown names rejected)
- `internal/task/` — task meta read/write (state/<id>.meta + status file), status validation, promote
- `internal/brief/` — task brief scaffolding (ship/scout templates at data/<id>/brief.md)
- `internal/teardown/` — crewmate teardown with safety checks (dirty/remote/report gate)
- `internal/crewstate/` — crewmate state reading (meta + pane liveness + status log)
- `internal/lifecycle/` — timing/lock invariants (wake queue, watcher beat, session lock): single definition of paths/constants, queue parse/append/drain, beat I/O, lock acquire/release/is-locked
- `internal/cli/backlog.go` — backlog command wiring to tasks-axi (inline in root.go)
Current layout (continued):
- `internal/delivery/` — review-diff, pr-check, pr-merge, merge-local, no-mistakes axi helpers

Later waves fill:
- `internal/stow/` — knowledge sweep
- Operator skill: `.agents/skills/munsu-ops` (fleet orchestration operator guide, commands mapped by lifecycle)
## Coding rules

- Follow Go conventions (`gofmt`, `go vet` clean).
- Use cobra for CLI commands.
- Deep modules, shallow interfaces: small public API, implementation detail behind.
- Surgical changes: touch only what the task requires. Clean up only your own mess.
- Match existing style in the file you're editing.
- Test home resolution and other core logic in `internal/*/` packages.
- Stub commands return `not yet implemented` (exit 1) until implemented.

## Go version

Go 1.26.5 (use `go 1.26` in `go.mod`).

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
