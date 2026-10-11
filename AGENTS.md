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
fails closed. The baseline header owns waiver admission and ratchet rules. It
merges the coverage profiles of all four test lanes: on the default lane alone,
branches covered only by the `integration` lane read as untested. When
it goes red, write a test that builds the state the guard refuses, or add a line
with a reason. Its rules are pinned by fixtures (`uncovered-guards.sh selftest`).

That job also checks documentation citations against the tree, compared both
ways with `.github/citations.allow` (`.github/scripts/citations.sh check`).
Classified file paths and Go identifiers in the covered set must resolve to an
existing file or a declaration this tree makes; the parser recognizes funcs,
methods, types, consts, vars and struct fields. The covered set is `README.md`,
`AGENTS.md` and `CLAUDE.md` (the last two are the same file here). Line numbers are ignored. The tool reports
classified-but-unjudged shapes as `unchecked` and does not extract some fenced
or malformed spans; the implementation comment at
`.github/scripts/citations/main.go` owns those boundaries. Rules are pinned by
fixtures (`citations.sh selftest`), and waiver rows follow the reason rule in the
`.github/citations.allow` header.

That job also enforces the flake ledger: a test CI caught being flaky on
`main` has a row there with a deadline, and an `open` row past its deadline
turns `invariants` red on every PR until someone fixes the test. The ledger
workflow and pull-request re-derivation rules are documented authoritatively in
`.github/flake-ledger.md`; never close a row because the test has been green for
a while.

### Working the guard registers

The machine-enforced rules live in the header of the script that enforces them
(`uncovered-guards.sh`, `citations.sh`, `deadcode.sh`). These only a reader
enforces; they apply to every waiver, mutant and unreachable function:

- Do not assert `err != nil` at a guard site: it is green for an error raised by an earlier guard. Assert what this guard refuses.
- Prove a "cannot be tested without a tautology" waiver by deleting the guard and reading the error the test observes. A message an earlier guard also emits passes without entering the branch.
- A deleted call site's recorded reason is a claim. Verify it against the code before deleting the callee.
- A shared name is not evidence. Resolve the package before calling a function dead or a guard re-homed.
- A waiver states its premise ("unreachable because X") and what makes it wrong ("wrong the moment X stops holding"). Where a test can pin the premise, cite it as `Premise pinned by <TestName>`; the guards lane resolves the name.
- An unreachable function is dead (delete it), not a finding (keep it with the reason written out), or unreachable because of a production bug (open an issue, mark the reason `OPEN-BUG`, do not delete it and do not file it as accepted debt).
- Where no lane runs a refusal branch or platform behaviour, state beside the code "no lane runs this; X covers it", never in a form that reads like proof.

## Refusals and the Human's words

A new refusal or fence, or the widening of one, ships only with the Human's words recorded
in the same change, in the `domain.Words` shape: grantor, channel and verbatim
quote. A refusal is a guard, check, gate or OS fence that refuses an action munsu
allowed before. Fixing a refusal that misfires on an action it was never meant to refuse
is exempt; removing a refusal the Human asked for needs words. Without words,
present the refusal to the Lead as unshipped.

The OS write fence is the primary write boundary. The shell write-target grammar
(`shellWriteTargets`, `internal/cli/shell_write_safety.go`) is frozen: fixing a
defect in a construct it already models is allowed, adding a shell construct is
not. Reopening it needs evidence of a write path the OS fence cannot cover, plus
new words. Deleting the lexer needs words and a proven fence or replacement on
every platform it guards.

## Delivery rules

Delivery mode: no-mistakes (push through the gate, never to `origin` directly).
Do not enable `required_pull_request_reviews` on `main` while one account authors
every PR: GitHub does not let an author approve their own PR, so every merge
deadlocks and `enforce_admins` leaves no bypass.
The required checks on `main` are load-bearing: an event raised by `GITHUB_TOKEN`
starts no workflow run, so a PR opened with that token never gets the required
checks and stays unmergeable. Relaxing the required checks would hand such a PR a
merge path.

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
- Delivery mode is derived from the resolved review and forge steps (`taskauthority.DeliveryModeForSteps`), never selected. A configured step that is not Ready refuses before any task mutation.
- Init auto-detect logic lives in `internal/cli/init.go` (`autoDetectConfig`) and respects `--reconfigure` flag.
- Captain launch lives in `internal/fleet/captain_captain.go`; it resolves the verified adapter through `internal/harness` and fails closed for unknown harnesses. Test launch argument generation without depending on PATH.
- Delivery acceptance has one owner in `internal/domain`. Provider orchestration and CLI-compatible delivery operations live in `internal/fleet/delivery_*.go`.
- There are no Task Authority Store adapters or migration commands, and schemas stay at `v1`.
- AFK supervision lives in `internal/orchestrator/afk_*.go`; focused tests stay beside those files.
- Wake delivery is the deep module in `internal/orchestrator/wakedelivery_deliver.go` (`DeliverWake`), with receipt and lifecycle mechanics in adjacent `turnend_*.go`, `lifecycle_*.go`, and wake files.

Rank hierarchy: General (fleet orchestrator) → Captain (`internal/fleet`, CLI `munsu captain`) → Soldier (task worker). Runtime: `MUNSU_ROLE=general|captain|soldier`. Labels: `captain-<id>-<hometag>`, windows `mu-captain-<id>`, marker `.munsu-captain-home`.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
