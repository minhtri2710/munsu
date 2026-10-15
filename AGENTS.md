# munsu — project agent memory

This file is the conventions file for soldiers working on munsu.
(Not the orchestrator operating manual — that's scaffolded by `munsu init`.)

## Build / test / lint

```sh
go build ./...       # build all root-module packages
go vet ./...         # static analysis
go test ./...        # default-tag suite (skips //go:build integration files)
```

These root-module commands do not reach Go tools under `.github/scripts`;
the `invariants` job dynamically discovers every directory containing tracked
Go files there and, for each one, compiles package and test sources, runs
`go vet`, and runs its unit tests. It also requires at least one test to
execute, so deleted or rotted tests fail the lane. Do not maintain a separate
tool list; see the
`Go tools under .github must vet and test` step in `.github/workflows/ci.yml`.

CI (`.github/workflows/ci.yml`) also runs a `-race` lane and an `integration`
tag lane. Tag lanes derive their package
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
fails closed. The baseline header owns waiver admission and ratchet rules. It
merges the coverage profiles of the default and `integration` lanes: on the default lane alone,
branches covered only by the `integration` lane read as untested. When
it goes red, write a test that builds the state the guard refuses, or add a line
with a reason. Its rules are pinned by fixtures (`uncovered-guards.sh selftest`).

The `guards` job also prints a skip ledger, report only: the tests the two coverage
lanes skipped, compared both ways with `.github/skips.allow` (`.github/scripts/skip-ledger.sh check`).
Unlisted skips and stale rows print and never fail the job; the job fails only when a
lane's `go test -json` stream is missing. Each row names a test a lane skips on purpose
and why; the reason column is documentation, matching is on package and test.

That job also enforces the flake ledger: a test CI caught being flaky on
`main` has a row there with a deadline, and an `open` row past its deadline
turns `invariants` red on every PR until someone fixes the test. A person
applies the rows `.github/workflows/flake-ledger.yml` reports after merge; the
rules live in `.github/flake-ledger.md`. Never close a row because the test has been green for
a while.

### Working the guard registers

The machine-enforced rules live in the header of the script that enforces them
(`uncovered-guards.sh`, `deadcode.sh`). These only a reader
enforces; they apply to every waiver, mutant and unreachable function:

- Do not assert `err != nil` at a guard site: it is green for an error raised by an earlier guard. Assert what this guard refuses.
- Prove a "cannot be tested without a tautology" waiver by deleting the guard and reading the error the test observes. A message an earlier guard also emits passes without entering the branch.
- A deleted call site's recorded reason is a claim. Verify it against the code before deleting the callee.
- A shared name is not evidence. Resolve the package before calling a function dead or a guard re-homed.
- A waiver states its premise ("unreachable because X") and what makes it wrong ("wrong the moment X stops holding"). Where a test can pin the premise, cite it as `Premise pinned by <TestName>`; the guards lane resolves the name.
- An unreachable function is dead (delete it), not a finding (keep it with the reason written out), or unreachable because of a production bug (open an issue, mark the reason `OPEN-BUG`, do not delete it and do not file it as accepted debt).
- Where no lane runs a refusal branch or platform behaviour, state beside the code "no lane runs this; X covers it", never in a form that reads like proof.
- Test each refusal with two rows: one that blocks the refused case and one that allows the nearest valid case, in one shared table per group. Do not add a test file per refusal.

## Refusals and the Human's words

A new refusal or fence, or the widening of one, ships only with the Human's words recorded
in the same change, in the `domain.Words` shape: grantor, channel and verbatim
quote. A refusal is a guard, check, gate or OS fence that refuses an action munsu
allowed before. Fixing a refusal that misfires on an action it was never meant to refuse
is exempt; removing a refusal the Human asked for needs words. Without words,
present the refusal to the Lead as unshipped.

The OS write fence is the primary write boundary, and it exists on darwin only.
Where the host has none, a soldier or reviewer launch refuses unless the project
sets fence-unsupported to allow, and the seat is then recorded and reported as
unfenced. A project's fence-writable-roots lists extra directories only its
soldiers may write; `ValidateWritableRoots` is the one validator, run at config
set and at launch. A captain runs under its own fence (`RoleCaptain`) as the
child of `munsu captain seat`, which stays outside the fence and runs the
commands the captain sends over its seat socket (`CaptainSeatRuns`), so its
spawns, probes and pane commands never nest in its sandbox; on a host with
no fence the captain launches unfenced and its meta records why. The shell write-target grammar
(`shellWriteTargets`, `internal/cli/shell_write_safety.go`) is frozen: fixing a
defect in a construct it already models is allowed, adding a shell construct is
not. Reopening it needs evidence of a write path the OS fence cannot cover, plus
new words. Deleting the lexer needs words and a proven fence or replacement on
every platform it guards.

## Delivery rules

Delivery: push the feature branch to `origin` and open a PR with `gh`; the required checks on `main` gate the merge, and the Human merges.
Do not enable `required_pull_request_reviews` on `main` while one account authors
every PR: GitHub does not let an author approve their own PR, so every merge
deadlocks and `enforce_admins` leaves no bypass.
The required checks on `main` are load-bearing: a PR that `.github/workflows/auto-update-branch.yml`
updated with its `GITHUB_TOKEN` merge commit and whose sweep was cancelled before
it released the parked CI run carries no checks, and stays unmergeable only
because the five required checks never report on it. Relaxing the required checks
would give that PR a merge path.

## Package map

Commands live in `internal/cli` (`munsu --help`); each package owns one thing:

- `cli`: composition root, Cobra commands, output contract, self-update, stow.
- `fleet`: Captain and Soldier lifecycle, spawn, delivery orchestration, project registry.
- `orchestrator`: watcher, AFK, wake delivery, turn-end and lifecycle coordination.
- `taskauthority`: canonical Task documents and every lifecycle, dispatch, binding, delivery and transfer rule.
- `home`: home resolution and domain-neutral durable mechanics (journaled commit, locks, leases, mailbox).
- `backend`: session adapters, endpoint observation, worktree provider.
- `domain`: typed identities, operations, preconditions and shared rules (`PR.CanMerge`, `ReviewVerdict.Approves`).
- `harness`: harness detection, verified adapters, dispatch profiles, quota ordering.
- `config`: typed settings, project overlays, tool entries, resolved snapshots.
- `fence`: darwin OS write fence for launched seats.
- `bootstrap`: toolchain diagnostics and native harness integration.
- `testutil`: test helpers only.

Dependency direction is enforced by `internal/testutil/architecture_policy_test.go`. Skills: `munsu skill list`.

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
- Delivery mode is derived from the resolved forge step (`taskauthority.DeliveryModeForForge`), never selected. A configured forge step that is not Ready refuses before any task mutation.
- Init auto-detect logic lives in `internal/cli/init.go` (`autoDetectConfig`) and respects `--reconfigure` flag.
- Captain launch lives in `internal/fleet/captain_captain.go`; it resolves the verified adapter through `internal/harness` and fails closed for unknown harnesses. Test launch argument generation without depending on PATH.
- Delivery acceptance has one owner in `internal/domain`. Provider orchestration and CLI-compatible delivery operations live in `internal/fleet/delivery_*.go`.
- There are no Task Authority Store adapters or migration commands, and schemas stay at `v1`.
- AFK supervision lives in `internal/orchestrator/afk_*.go`; focused tests stay beside those files.
- Wake delivery to the General's pane (in a captain home, its Captain's own pane; `ResolveTargetWithSource`) is `DispatchWake` in `internal/orchestrator/wake_dispatch.go`; report wakes derive from status lines in `internal/home/report_wakes.go` and settle in `internal/orchestrator/lifecycle_lease.go`, with turn-end and lifecycle mechanics in adjacent `turnend_*.go`, `lifecycle_*.go`, and wake files.

Rank hierarchy: General (fleet orchestrator) → Captain (`internal/fleet`, CLI `munsu captain`) → Soldier (task worker). Runtime: `MUNSU_ROLE=general|captain|soldier`. Labels: `captain-<id>-<hometag>`, windows `mu-captain-<id>`, marker `.munsu-captain-home`.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
