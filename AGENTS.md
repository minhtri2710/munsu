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

The full munsu module map lives in the scout report at
`/Users/beowulf/.treehouse/firstmate-8bf1b0/2/firstmate/data/munsu-port-spec-mvp-sj/report.md`
and the port-mapping table at `docs/port-mapping.md`.

Current A0 layout:
- `cmd/munsu/main.go` — entrypoint
- `internal/cli/root.go` — cobra command tree (home implemented, rest stubs)
- `internal/home/` — home resolution and dir tree creation

Later waves fill:
- `internal/config/` — config read/write
- `internal/project/` — project registry
- `internal/worktree/` — treehouse wrapper
- `internal/harness/` — harness adapter
- `internal/session/` — session backend (tmux)
- `internal/task/` — task lifecycle
- `internal/delivery/` — PR merge, local merge, review-diff
- `internal/backlog/` — backlog adapter
- `internal/watcher/` — supervision watcher
- `internal/stow/` — knowledge sweep

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
