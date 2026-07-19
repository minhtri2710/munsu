# munsu — project agent memory

This file is the conventions file for soldiers working on munsu.
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

Skills are documented in `README.md` ("Available skills" table).

Embedded skills live under `internal/cli/skills/` and are accessed via `//go:embed`.
Runtime: `munsu skill list|show <name>` reads from the embed; `munsu init --skill`
installs `munsu-ops` to the chosen destination and points at embedded auxiliaries.

- Follow Go conventions (`gofmt`, `go vet` clean).
- Use cobra for CLI commands.
- Deep modules, shallow interfaces: small public API, implementation detail behind.
- Surgical changes: touch only what the task requires. Clean up only your own mess.
- Match existing style in the file you're editing.
- Test home resolution and other core logic in `internal/*/` packages.
- `munsu doctor` in `internal/cli/doctor_cmd.go` reuses `internal/bootstrap/bootstrap.go` diagnostics; fix strings live in `internal/bootstrap/fixes.go`.
- Delivery mode auto-selection in `internal/spawn/spawn.go` (`ResolveDeliveryMode`) — precedence: --mode flag, project registry, config/default-mode, auto (no-mistakes on PATH).
- Init auto-detect logic lives in `internal/cli/init.go` (`autoDetectConfig`) and respects `--reconfigure` flag.
- Captain launch (`internal/captain/captain.go`) resolves harness via `harness.Captain()` chain, looks up the adapter from the harness registry (`harness.GetAdapter`), and builds args from `adapter.LaunchTemplate` (ModelFlag, ExtraArgs). Unknown/unverified harnesses fail closed. Test launch path generation with `buildLaunchArgs` to avoid PATH dependency.
- Delivery domain types live in `internal/delivery/domain.go` (PR, CheckRun, Review, PRStatus, ReviewState, CheckStatus) with `PR.CanMerge()` and `Review.IsApproving()` business rules. Pipeline interface and adapters (GHAxiAdapter, NoMistakesAdapter, GitLocalAdapter, CompositeAdapter) in `internal/delivery/pipeline.go`. Existing CLI function signatures are backward-compatible.
- AFK daemon (`internal/afk/`): `Daemon.Start` runs foreground (lock→flag→clearStale→runLoop→signal→flush→cleanup); `lock.go` identity lock; `sentinel.go` inject mark; `triage.go` `OneCycle` produces `Digest` from wake queue; `digester.go` accumulates Digests over a window and flushes `state/.afk-digest` (includes `SafeTarget`/`TargetVerdict`); `wedge.go` detects stale beat and repeated-wake conditions; `stale.go` clears session-scoped `.seen-*`/`.subsuper-*` artifacts; `target.go` resolves general pane via `config/general-pane`; `safety.go` `IsSafeInjectTarget` captures and classifies composer row for inject safety.

Rank hierarchy: General (fleet orchestrator) → Captain (`internal/captain`, CLI `munsu captain`) → Soldier (task worker). Runtime: `MUNSU_ROLE=general|captain|soldier`. Labels: `captain-<id>-<hometag>`, windows `mu-captain-<id>`, marker `.munsu-captain-home`, registry `data/captains.md`. See `docs/architecture.md` "Rank hierarchy and identity".

Go 1.26.5 (use `go 1.26` in `go.mod`).

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
