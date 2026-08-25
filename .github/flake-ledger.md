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

The `Flake ledger` workflow runs `.github/scripts/flake-sweep.sh` over what CI
already ran, per **attempt**, and goes red with the exact diff on its run
summary. It prepares no branch for you: GitHub Actions is not permitted to open
pull requests in this repository, so the lane stops at reporting and a person
applies that diff by running `.github/scripts/flake-sweep.sh sync --owner <ISSUE_ID>`
locally. The `--owner` value must not be empty, whitespace-only, or `TBD`,
and otherwise must match the anchored `BEO-<number>` format; name the real issue
that will fix the flake. Applying it is enforced, not trusted:
`.github/scripts/flake-sweep.sh applied` turns `invariants` red on every open PR
unless the PR's own ledger answers a fresh observation of the main CI evidence. It
re-derives directly rather than trusting a prior sweep verdict, so CI reruns and
asynchronous workflow timing cannot certify stale evidence. For
twelve days in August 2026 nothing did -- run 32153420356 reported a race-lane
flake with no row and no check a merge waited on could see it. It is not a
sampler either: it never re-runs anything and
it computes no failure rate. Four pushes to `main` on 2026-08-14 13:40-13:41 produced three red
`integration` lanes; two of those runs read `success` today,
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

By being fixed, by hand. Rows are never dropped automatically by age. While the
run a row cites is still inside the sweep's window, the row is mandatory:
deleting it there is red, and if the test is still flaky the sweep re-files it
on the next main run. Once that evidence has aged out of the window, nothing
can re-derive the row, so once the fix has landed (`state` reads `fixed:<sha>`)
a person can remove the row and it will not come back.

A row is **never** closed because "we have not seen it fail in a while". BEO-82
re-ran a red lane 14 times on the red SHA and saw zero failures; a run of greens
is not evidence of a fix. The ledger only ever accuses -- absence from it proves
nothing about a test, and a green streak proves nothing about an entry.

`fixed:` is not a silencer. If the sweep observes the test flaking again after
the fix was declared, the row goes back to `open` with a fresh deadline,
keeping the issue that owns the fix -- marking a test fixed can never be a way
to make its re-flakes silent.

## Columns

| column | meaning |
| --- | --- |
| `test` | Go test name, top level (a subtest is filed under its parent) |
| `lane` | `build`, `race` or `integration` |
| `first_seen` | `<sha>@<run_id>/<attempt>` -- the attempt is the part a later rerun cannot invalidate |
| `last_seen` | the newest observation the sweep has seen, same `<sha>@<run_id>/<attempt>` format as `first_seen`; bot-maintained, and the sweep is red while it lags the evidence |
| `deadline` | `YYYY-MM-DD`, 14 days from filing by default |
| `owner_issue` | the issue that will fix it. `TBD` is refused: filing the flake and filing the work are the same act |
| `state` | `open`, or `fixed:<sha>` naming the commit that landed the fix |

`fixed:` is the one state no deadline is compared against, so it is the one
state that has to be checkable: the ref must be a commit id, and it must be a
commit reachable from `main`. `fixed:i-never-fixed-it` used to read as `0 open,
0 overdue` for the cost of one word -- cheaper than the escape this file already
admits to, and permanent.

`.github/scripts/flake-ledger.sh check` enforces the shape and the deadlines
with no network access at all, next to the ADR-number and gofmt rules, and
`flake-ledger.sh selftest` runs beside it with one fixture per rule -- the rules
themselves are guards, and a guard nothing tests stops protecting silently.
`flake-sweep.sh selftest` runs in that same job for the same reason: it is
hermetic too, and when it ran only in the ledger workflow, a PR could take a rule
out of the sweep and stay green.
`.github/scripts/flake-sweep.sh check` compares this table against the API in
both directions, the way `deadcode.sh check` compares `.github/deadcode.allow`
against the tree: observed but unfiled is red, filed but no longer observable is
red too. `flake-sweep.sh verify-fixed` is the half that can check a `fixed:` ref
against `main`. Both need the API, so both live in the `Flake ledger`
workflow, which fires after a merge and never on a pull request.

`.github/scripts/flake-sweep.sh applied` is the one rule that runs on the
pull-request path *and* reads the API. It re-derives `check` and `verify-fixed` directly against *this* checkout from
one fresh per-attempt observation stream, passing only if this checkout answers
it. No prior sweep verdict is trusted, so CI reruns and asynchronous workflow
timing cannot certify stale evidence. While main CI for a new commit is still
running, its attempt records do not exist yet, so a flake it will reveal is not
observable by anyone; a merge can still land ahead of it, and the ledger then
appoints the next merger. There is no exemption for "the PR edits this file" --
re-deriving means the only way past the refusal is the fix.

The rows between the markers below are machine-maintained. File new rows with
`.github/scripts/flake-sweep.sh sync --owner <ISSUE_ID>` as described above. Edit
`deadline`, `owner_issue` and `state` by hand; the sweep keeps all three on a row
that already exists, so a hand edit survives the next sweep -- with the single
exception named above, where a `fixed:` row seen flaking again is reopened and has
its `state` and `deadline` rewritten (never its `owner_issue`). Leave `test`,
`lane`, `first_seen` and `last_seen` to the sweep, and if you disagree with a row,
argue with it in the owning issue rather than deleting it -- the sweep will re-add
it on the next `main` run.

`owner_issue` is on that list because one `sync --owner` gives *every* row it files
the same issue, and re-running `sync` with a different one changes nothing -- the
preserve-on-re-run rule that protects `BEO-79` from being clobbered is the same
rule that refuses to move a row. So when a sweep files two flakes that belong to
two issues, the second one is moved here, by hand, and stays moved.

<!-- flake-ledger:begin -->
| test | lane | first_seen | last_seen | deadline | owner_issue | state |
| --- | --- | --- | --- | --- | --- | --- |
| TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets | integration | 869319d8@31805867146/1 | 869319d8@31805867146/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestRegistryBindingScaleBound | integration | 665c301e@31805801020/1 | 665c301e@31805801020/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestRegistryBindingScaleBound | race | 282f6cfb@31680287927/1 | 282f6cfb@31680287927/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
| TestStoreWriteAckRefusesUnusableAckRecords | race | e31d83a1@32094709771/1 | e31d83a1@32094709771/1 | 2026-09-01 | BEO-115 | fixed:8eafcd37 |
| TestTmux_Alive_UnknownWindow | integration | 6c408310@31805831782/1 | 6c408310@31805831782/1 | 2026-08-30 | BEO-79 | fixed:7e295ea |
<!-- flake-ledger:end -->
