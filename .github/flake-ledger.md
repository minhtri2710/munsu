# Flake ledger

Every test this repo has caught being flaky on `main`, and the date by which it
has to stop being flaky.

An entry here is not a waiver. Nothing is skipped, no timeout is loosened, and
the test keeps running in its normal lane exactly as before. This file is the
accounting, and its only power is the deadline: when a deadline passes with the
entry still `open`, the `invariants` lane goes red on **every** open PR,
including PRs that have nothing to do with the flake. Whoever needs to merge
next has to deal with it. That is on purpose -- a flake has no natural owner, so
the mechanism appoints one.

## How a row gets here

`.github/scripts/flake-sweep.sh` reads what CI already ran, per **attempt**, and
opens a PR adding what it found. It is not a sampler: it never re-runs anything
and it computes no failure rate. Four pushes to `main` on 2026-08-14 13:40-13:41
produced three red `integration` lanes; two of those runs read `success` today,
because a rerun overwrites a run's conclusion. `gh run list` says that cluster
was 2 red out of 4. It was 3, and `/actions/runs/<id>/attempts/<n>/jobs` still
says so. That erasure -- not a shortage of signal -- is what this exists for.

A red enters the ledger only when one of three questions answers yes, none of
which is a threshold:

- **the reruns disagree** -- one attempt of the same run failed, another passed.
  Same commit, two verdicts.
- **the PR head was green** -- the test failed on the `main` run of a merge
  commit while the same lane passed on that PR's own head. Immediate; no waiting
  for the next merge, which has a p90 of 7.3 hours and a max of 12.8.
- **it healed itself** -- green again on the next `main` commit, whose diff did
  not touch the test's package. A real regression does not do that.

A red that is still red on the next `main` commit is a regression, not a flake,
and does not belong here.

## How a row leaves

By being fixed. `state` goes from `open` to `fixed:<ref>` when the fix lands,
and the row is dropped once its evidence ages out of the sweep's 30-day window.

A row is **never** closed because "we have not seen it fail in a while". BEO-82
re-ran a red lane 14 times on the red SHA and saw zero failures; a run of greens
is not evidence of a fix. The ledger only ever accuses -- absence from it proves
nothing about a test, and a green streak proves nothing about an entry.

## Columns

| column | meaning |
| --- | --- |
| `test` | Go test name, top level (a subtest is filed under its parent) |
| `lane` | `build`, `race` or `integration` |
| `first_seen` | `<sha>@<run_id>/<attempt>` -- the attempt is the part a later rerun cannot invalidate |
| `deadline` | `YYYY-MM-DD`, 14 days from filing by default |
| `owner_issue` | the issue that will fix it. `TBD` is refused: filing the flake and filing the work are the same act |
| `state` | `open`, or `fixed:<ref>` once the fix has landed |

`.github/scripts/flake-ledger.sh check` enforces the shape and the deadlines
with no network access at all, next to the ADR-number and gofmt rules.
`.github/scripts/flake-sweep.sh check` compares this table against the API in
both directions, the way `deadcode.sh check` compares `.github/deadcode.allow`
against the tree: observed but unfiled is red, filed but no longer observable is
red too.

The rows between the markers below are machine-maintained. Edit `owner_issue`,
`deadline` and `state` by hand; leave `test`, `lane` and `first_seen` to the
sweep, and if you disagree with a row, argue with it in the owning issue rather
than deleting it -- the sweep will re-add it on the next `main` run.

<!-- flake-ledger:begin -->
| test | lane | first_seen | deadline | owner_issue | state |
| --- | --- | --- | --- | --- | --- |
| TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets | integration | 869319d8@31805867146/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestRegistryBindingScaleBound | integration | 665c301e@31805801020/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestRegistryBindingScaleBound | race | 282f6cfb@31680287927/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestTmux_Alive_UnknownWindow | integration | 6c408310@31805831782/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
<!-- flake-ledger:end -->
