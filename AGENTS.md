# munsu — project agent memory

This file is the conventions file for soldiers working on munsu.
(Not the orchestrator operating manual — that's scaffolded by `munsu init`.)

## Build / test / lint

```sh
go build ./...       # build all packages
go vet ./...         # static analysis
go test ./...        # default-tag suite (skips //go:build integration files)
```

CI (`.github/workflows/ci.yml`) also runs a `-race` lane and tag lanes for
`integration`, `e2e` and `lifecycle_integration`. Lanes derive their package
lists from the tag itself (`.github/scripts/build-tags.sh packages <tag>`), so
adding a tagged file to a new package needs no workflow edit. Adding a *new*
tag does: every tag must be classified in `.github/build-tags.manifest`, and
the `invariants` job fails until it is. Write constraints as `//go:build` only
— a lone legacy `// +build` line is invisible to that derivation, so the job
fails it and asks you to run `gofmt`. The same job also fails on any file
`gofmt -l .` lists, so run `gofmt -w` before pushing. Run the lane commands
from `ci.yml` for a full local matrix.

That job also fails on any function the `munsu` binary cannot reach, compared
both ways against `.github/deadcode.allow` (`.github/scripts/deadcode.sh check`,
needs `golang.org/x/tools/cmd/deadcode`). A guard with no call site passes its
own tests and protects nothing — five of those reached `main` before this lane
existed, so wire it up, delete it, or waive it with a real reason.

A separate `guards` job fails on any *refusal branch* no test has ever entered,
compared both ways against `.github/uncovered-guards.baseline`
(`.github/scripts/uncovered-guards.sh check`). The guard set is derived from the
tree (`.github/scripts/guardsites`), never declared, so a new guard with no test
fails closed — the file lists waivers only and is only allowed to shrink. It
merges the coverage profiles of all four test lanes: on the default lane alone,
105 branches the `integration` lane is the only cover for read as untested. When
it goes red, write a test that builds the state the guard refuses, or add a line
with a reason. Its rules are pinned by fixtures (`uncovered-guards.sh selftest`).

Working either register — writing a waiver, scoring a mutant, triaging an
unreachable function — is governed by `docs/adr/0017-guard-burn-down-working-rules-and-their-enforcement-addresses.md`,
which also states which of those rules a machine checks and which only a reader does.

That job also checks documentation citations against the tree, compared both
ways with `.github/citations.allow` (`.github/scripts/citations.sh check`).
Classified file paths and Go identifiers in the covered set must resolve to an
existing file or a declaration this tree makes; the parser recognizes funcs,
methods, types, consts, vars and struct fields. The covered set is `docs/**`
except `docs/plans/`, plus `AGENTS.md`, `CLAUDE.md` and `README.md` (the first
two are the same file here). Line numbers are ignored. The tool reports
classified-but-unjudged shapes as `unchecked` and does not extract some fenced
or malformed spans; the implementation comment at
`.github/scripts/citations/main.go` owns those boundaries. Rules are pinned by
fixtures (`citations.sh selftest`), and waiver rows follow ADR-0017 §4 as
described in `.github/citations.allow`.

That job also enforces `.github/flake-ledger.md`: a test CI caught being flaky
on `main` has a row there with a deadline, and an `open` row past its deadline
turns `invariants` red on every PR until someone fixes the test. Rows are
derived by `.github/scripts/flake-sweep.sh` from per-attempt Actions data (a
rerun overwrites a run's conclusion, so run-level history is not evidence) and
are applied by hand from the diff the `Flake ledger` workflow prints on its run
summary; the deadline half (`.github/scripts/flake-ledger.sh`) reads nothing but
the committed file. Never close a row because the test has
been green for a while — refusing that inference is why the file exists.

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
- Delivery mode auto-selection lives in `internal/fleet/spawn_spawn.go` (`ResolveDeliveryMode`) — precedence: --mode flag, project registry, config/default-mode, auto (no-mistakes on PATH).
- Init auto-detect logic lives in `internal/cli/init.go` (`autoDetectConfig`) and respects `--reconfigure` flag.
- Captain launch lives in `internal/fleet/captain_captain.go`; it resolves the verified adapter through `internal/harness` and fails closed for unknown harnesses. Test launch argument generation without depending on PATH.
- Delivery acceptance rules have one owner in `internal/domain/domain.go` (`PR.CanMerge`, `Review.IsApproving`); delivery invariant operations run through `internal/taskauthority` named ops, with provider orchestration and CLI-compatible delivery operations in `internal/fleet/delivery_*.go`.
- Task lifecycle, dispatch, binding, delivery and transfer authority lives in `internal/taskauthority` (one canonical `Canonical` surface over `internal/home` durable mechanics; every mutation is a `domain.Operation` with typed intent checked against a `domain.Precondition` and committed as an atomic journaled change-set under a scoped fenced lock). `internal/home` retains no task lifecycle, dispatch or binding authority (ADR-0008 §2). There are no Task Authority Store adapters or migration commands.
- AFK supervision lives in `internal/orchestrator/afk_*.go`; focused tests stay beside those files. Task state lives in `internal/taskauthority` canonical Task documents; `.meta`/`.status` remain `internal/home` primitives written by fleet/CLI projection writers; `internal/home` retains generic storage mechanics (writer identity, leases, mailbox).
- Wake delivery is the deep module in `internal/orchestrator/wakedelivery_deliver.go` (`DeliverWake`, which closes its own terminal handoff — ADR-0015), with receipt and lifecycle mechanics in adjacent `turnend_*.go`, `lifecycle_*.go`, and wake files.

Rank hierarchy: General (fleet orchestrator) → Captain (`internal/fleet`, CLI `munsu captain`) → Soldier (task worker). Runtime: `MUNSU_ROLE=general|captain|soldier`. Labels: `captain-<id>-<hometag>`, windows `mu-captain-<id>`, marker `.munsu-captain-home`, registry `data/captains.md`. See `docs/architecture.md` "Rank hierarchy and identity".

Go 1.26.5 (use `go 1.26` in `go.mod`).

## Delegation via herdr + agy

When pi (root) needs a second agent for implementation, use agy as a dedicated thread:

1. **Context pack**: send goal + constraints + file list, not full history
2. **Open question**: let agy explore independently -- no pre-solve
3. **Ownership**: one task, one scope, one implementer at a time
4. **agy does not know about herdr**: it should feel like a direct user request

```sh
.agents/scripts/delegate-herdr.sh "<prompt>" [timeout-secs]
```

For headless subprocess (no herdr): `antigravity-delegate` skill via delegate.sh.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
