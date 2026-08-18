# 0017. The Guard Burn-Down's Working Rules, and Which of Them Anything Enforces

* **Status:** Accepted — see [Removal condition](#removal-condition), which is per section rather than for the whole document
* **Date:** 2026-08-18
* **Extends:** ADR-0016 (a constraint nothing checks is *owned*, and says so in the same breath as it is stated)
* **Triggered by:** BEO-63 layer 2 ← the BEO-67 deadcode batches and the BEO-69 refusal-coverage batches

## Context

Two lanes already derive their subject from the source tree and register only waivers:
`.github/scripts/deadcode.sh` against `.github/deadcode.allow`, and
`.github/scripts/uncovered-guards.sh` against `.github/uncovered-guards.baseline`. Both are
required checks. Both are documented in `CLAUDE.md`. That is layer 1, and it is not what
this ADR is about.

Layer 2 is the set of rules the *burn-down work itself* runs on: how a mutant is built and
scored, what a test at a guard site is allowed to assert, what a waiver line has to say,
and what to do with a function no caller reaches. Every rule below caught at least one real
defect across five batches. None of them is written down anywhere in this repository. They
have been carried from batch to batch inside dispatch comments, re-typed by hand each time
a different agent picked up the work.

At `22009ed` that is 92 lines in `.github/deadcode.allow` and 404 in
`.github/uncovered-guards.baseline` still to triage — many more runs in which the habit can
simply not be re-typed. ADR-0016 §2 named this shape for a different constraint: a rule
that lives in one orchestrator's memory disappears on the first run that orchestrator is
not part of.

Recording them is the whole of this ADR. It changes no code, no workflow, and neither
register. Where a rule needs machinery that does not exist, the machinery is named as work
and not built here.

### What is actually in the tree, and what is not

Three factual corrections to the brief this ADR was written from, because they change
enforcement addresses from *present* to *pending*:

* **`.github/scripts/mutation-check.py` is not on `main`.** It exists only on the branch
  behind PR #511 (`agent/qa-test-engineer/4ea97665-ta`), together with the one case file
  `.github/mutation-cases/beo-69-taskauthority.tsv`. Neither is referenced by
  `.github/workflows/ci.yml` on `main` or on that branch.
* **The waiver figure of 348 is the post-#511 count.** `.github/uncovered-guards.baseline`
  holds 404 entries at `22009ed`; #511 covers 56 and waives 9.
* **Five in-tree citations of "ADR-0009" mean the layer-1 design, and ADR-0009 is
  something else.** `.github/scripts/deadcode.sh:10`, `:11`,
  `.github/workflows/ci.yml:249`, `:256` and `.github/deadcode.allow:54` all cite ADR-0009
  for the derived-not-declared design and the `EnsureNotPrimary` case;
  `docs/adr/0009-checkout-identity-single-owner.md` is about checkout identity
  classification. The layer-1 record was never committed — it is a Multica comment on
  BEO-63 — and the number it was drafted under was taken by another ADR. The
  `ADR references must point at a file that exists` invariant cannot see this: it matches
  `adr/NNNN-slug.md` path form, and these are bare `ADR-0009` prose. This is the ADR-0006
  hole BEO-94 closed, reappearing in the one shape the fix does not cover.

## Decision

### 1. Layer 2 is declared, not derived. It fails open, and must never be sold otherwise.

Layer 1 inverted the register deliberately: the guard set comes out of the tree and only
waivers are listed, so a guard nobody registers still fails the lane. Layer 2 has no such
inversion. `mutation-check.py` reads a hand-written TSV of `<go file> <test> <anchor>`
rows. A refusal branch whose row nobody writes is not checked, is not reported, and shows
up nowhere.

That is acceptable for a batch tool and disqualifying for a gate. §2's rules are therefore
properties of a script somebody chooses to run — not of a check that runs. Deriving the
case list is the missing infrastructure, named in [Work this creates](#work-this-creates)
and not attempted here.

### 2. Rules the mutation script enforces mechanically

**Address: `.github/scripts/mutation-check.py`, pending #511. Not a required check, not in
`ci.yml`, run by hand on a case file per batch.**

**2.1 — The operator is `(COND) && false`, never bare `false` and never `false && (COND)`.**
Go short-circuits `&&`, so `false && (cond)` never evaluates `cond`. Where the condition
has a side effect the mutant changes more than the guard: `internal/home/wake_lease.go:184`
is `if !scanner.Scan()`, and under `false && (!scanner.Scan())` the scanner never advances,
so the loop below reads different data and the test may fail for a reason unrelated to the
guard. A kill that is not attributable is not a kill. `(COND) && false` evaluates the
condition exactly as production does, keeps the side effect, and still makes the branch
unreachable. Bare `false` is wrong in the other direction: it leaves every variable the
condition names unused and the mutant BUILD-FAILs instead of running.

**2.2 — BUILD-FAIL is its own class, scored before KILLED and SURVIVED.** A mutant that
never compiled proves nothing in either direction. Folding it into KILLED is self-scoring:
the run reports proof it did not obtain. `mutation-check.py` counts it separately and fails
the run on it, exactly as it fails on a survivor.

**2.3 — The harness passes `-timeout` to `go test`.** Without it a hung mutant sits on
`go test`'s 10-minute default, then exits non-zero, and a plain returncode check scores
that as a kill. A guard whose removal turns a bounded operation into an unbounded one is
precisely the case where that matters. TIMEOUT is a fourth class, failing like the other
two. The default is `--timeout 5m`.

**2.4 — `-j` groups by package, not by file.** A Go package is one compilation unit, so
two mutants in different files of the same package compile into one test binary and neither
verdict is attributable to its own guard. The script groups cases by package and refuses
`-j > 1` outright when every selected case lives in a single package. Grouping by file —
what it did before the #511 fixup — is that mistake with extra steps.

These four are the only rules in this document that a machine already checks end to end,
and only for the cases somebody listed.

### 3. Rules only a reader enforces

**Address: none. Each is stated here because stating it is the whole of the enforcement.**

**3.1 — Do not assert on `err != nil` at a guard site.** It is green for any error,
including one raised by a guard that runs *before* the one under test. The assertion must
name what this guard refuses.

This rule has a partial machine address and it is worth being exact about how partial: a
test that only checks `err != nil` SURVIVES its mutant, so §2 catches it — but only for a
branch somebody put in the case file, and only after somebody runs the script. Absent that,
nothing sees it. §2 is the reason the rule is checkable at all and not a reason to call it
checked.

**3.2 — Prove a tautology by removing the guard, not by reading the code.** A test that
asserts a message an *earlier* guard also emits passes without ever entering the branch
under test (the BEO-87 shape). The working counter-proof is mechanical: delete the guard
the waiver cites, run the test, and read the error actually observed. Both findings on #511
came out of exactly that. The second one cuts the other way and is the more instructive:
three cleanup waivers argued the branch "cannot be tested without a tautology" because the
fence and the apply check emit near-identical prose. Removing the fence showed the two
messages are distinct — `cleanup claim fence mismatch` against
`stores a cleanup claim of a different identity …; refusing to overwrite`. The waivers
survived, on unreachability alone, with the tautology argument struck out. *"Cannot be
tested without a tautology" would waive anything; the check is what decides whether it
applies here.*

**3.3 — Verify the recorded reason of a deleted call site before deleting the callee.**
BEO-112 passed four review rounds because a commit message misstated the premise and every
later round read the message instead of the code. The finding, as it was eventually
written into `.github/deadcode.allow` and cleared by #509: *"#417 deleted its only call
site (`ReconcileCaptainHook`) as 'legacy read compatibility', but `DeliverWake` still
writes those receipts today, and only self-acks when the parent is NOT a captain home.
Nothing else writes the ack."* Four functions were unreachable, and the reason they were
unreachable was a live bug, not the compatibility cleanup the message claimed. A recorded
reason is a claim; this rule is the instruction to treat it as one.

**3.4 — A shared name is not evidence; package resolution is.** Name collisions lie in
both directions, and this repo has one instance of each. BEO-112: a shared name made a dead
function look alive. BEO-114: a shared name made a guard look re-homed while the new owner
was empty — `internal/orchestrator/supervision_process_windows.go:7` is
`func configureWatcherProcess(*exec.Cmd) {}`, against
`supervision_process_unix.go:10` setting `Setpgid: true`, with two live call sites at
`supervision_watcher.go:157` and `captain_watcher.go:80`. Grepping the name finds a
definition and a caller and says nothing at all.

### 4. A waiver line has three parts, not two

**Address for parts (a) and (b): none — the reason column is self-declared and both lane
scripts check only that it is non-empty. Address for part (c): `go test ./...` in the
"Build and test" required check.**

Every line in `.github/uncovered-guards.baseline` and `.github/deadcode.allow` states:

* **(a) the premise** — not "this branch is unreachable" but "unreachable **because** X";
* **(b) the invalidating condition** — "and this line is wrong the moment X stops holding";
* **(c) the name of a test that pins the premise** — a test that builds the state which
  *would* enter the waived branch and asserts the refusal carries the earlier guard's
  message.

Part (c) is not in the rule set this ADR was asked to record, and it is the only part of a
waiver anything executes. #511's waivers already carry it
(`Premise pinned by TestPremiseCleanupFenceRejectsAForeignClaimBeforeApply`, and eight
more): when the earlier guard moves or softens, the premise test goes red and the waiver
has to be re-argued instead of quietly becoming wrong. Recording the two-part form would
record a weaker rule than the one the batches actually earned.

The link between a line and its named test is not checked. Deleting
`TestPremiseNoAggregateWithABlankOwnerReachesApply` leaves three baseline lines citing it
and every lane green. Closing that is small and named as work below.

Both lane scripts already state their own limit in their headers — *"the reason column is
self-declared … reading the diff is what catches that, not the script"* — in the same words
`.github/build-tags.manifest` uses about itself. That sentence is the model this whole ADR
follows and it is not decoration: it is what keeps a self-declared column from reading like
a gate.

### 5. The deadcode lane's three groups, and why group (c) is the point

**Address for (c) in the reachability lane: `announce_open_bugs` in
`.github/scripts/deadcode.sh`, which re-prints every `OPEN-BUG`-prefixed reason as a
`::warning` on every run. Address for (c) in the coverage lane: none — `uncovered-guards.sh`
has no equivalent marker.**

Every unreachable function is one of three things:

* **(a) genuinely dead** → delete it;
* **(b) not a finding** → a GOOS-union artifact, a test entrypoint; keep it with the reason
  written out;
* **(c) unreachable because of a production bug** → stop. Open an issue. Do **not** delete
  it, and do not file it as accepted debt.

Group (c) is why the lane exists at all. A guard with no call site passes its own tests and
protects nothing; deleting it converts a live bug into a tidy diff. BEO-114 came out of
this check.

The marker is live machinery with zero users today: `.github/deadcode.allow` currently
contains no `OPEN-BUG` line. That is the correct state — the last four were the BEO-112
relay chain above, cleared by #509 — and it is also why the convention is easy to forget
exists; the file reads as if it only ever held debt. The asymmetry is the real gap: a
refusal branch unreachable because of a production bug has no marker in the coverage lane,
so it can only be waived as though it were debt.

### 6. A guard no lane can measure: what may be claimed about it

This is the rule that needed a decision rather than a transcription, and it gets three
separate answers.

**6.1 — The strong form is rejected, and not on grounds of cost.** "Do not merge code no
lane measures" would forbid every `_windows.go` change in this repository, BEO-114's fix
included. More decisively, it contradicts a design decision already made and already pinned
by fixtures: `uncovered-guards.sh` classifies a refusal site in a file no lane compiles as
`unmeasured` and excludes it from **both** directions of the comparison, with the reason
written in its header — excluding it from one direction only makes the baseline
platform-dependent, red on a laptop and green on the runner. Two fixtures
(`.github/testdata/uncovered-guards/unmeasured-file/`, `missing-lane/`) hold that behaviour
in place through `uncovered-guards.sh selftest`. The strong form would reverse it. An ADR
line that contradicts a tested invariant is not a rule; it is a future argument.

**6.2 — The declaration obligation is accepted, and its home is the artifact, not the PR
body.** Where a lane cannot reach, the limit is stated, and stated as a limit rather than
as a result. But a PR body is not in the repository, nothing reads it, and it is not
where anyone looking at the code will be standing. This repository already puts these
sentences in the right place and should keep doing so:

* `.github/build-tags.manifest` classifies `windows` as `goos-vet` with the sentence
  *"15 files, 3 of them `_test.go` that no lane had ever compiled. `GOOS=windows go vet
  ./...` covers them"*. That row is gated — an unclassified tag turns `invariants` red — so
  the *existence* of a declaration is enforced even though its content is not.
* BEO-114's own commit message carries the model sentence: *"The added test asserts
  SysProcAttr carries the flag when compiled for windows; it does not exercise real process
  -group behaviour."* Stating the limit, claiming nothing more.
* `uncovered-guards.sh` already emits a `::warning` per unmeasured file on every run, with
  the comment that a blind spot nobody is reminded of is one somebody eventually hides in.

So the obligation is: **the sentence goes next to the code or in the register that carries
it, in the form "no lane runs this; X is what covers it", and never in a form that reads
like proof.** The set of places needing one is already derived and printed; what this rule
adds is the sentence.

**6.3 — The population is 8, and a Windows test lane is a separate issue, not a
prerequisite.** `.github/scripts/guardsites` finds 941 refusal sites at `22009ed`. Eight sit
in files no `ubuntu-latest` lane compiles: six across
`internal/home/{watcher,taskmeta,canonical}_lock_windows.go` and two in
`internal/orchestrator/afk_process_identity_darwin.go`. Every job in `ci.yml` is
`runs-on: ubuntu-latest`; the only Windows coverage in the repository is
`GOOS=windows go vet ./...`, which compiles the three Windows `_test.go` files and runs
none of them.

Eight is small enough that 6.2 costs eight sentences, and large enough that a
`windows-latest` job would be worth its cost — it would move six of them from unmeasured
to measured. But the two answer different questions and neither waits on the other: 6.2
governs what may be **claimed**, a Windows lane changes what can be **measured**. 6.2
belongs here because it is a rule about claims, and this document is where the repository's
rules about claims live. The lane belongs in its own issue, sized against the matrix cost,
and is listed as work below.

**One qualification on the rule as it reached this ADR, which matters for its own headline
case.** Phrased about *guards*, it does not cover BEO-114. `configureWatcherProcess` on
Windows is not a refusal branch, so `uncovered-guards.sh` never classifies it, and it has
two live call sites, so `deadcode` correctly reports it reachable. Both lanes are silent
and correct. What found it was §3.4 read by a person. The noun in this rule is therefore
**"a refusal branch or a platform behaviour no lane executes"**, not "a guard" — otherwise
the rule excludes the case it was written from.

## Removal condition

Split by what each part is attached to, because they do not expire together. The general
claim that "these rules die when the burn-down finishes" is false for half of them.

**Dies with the burn-down.** The *cadence* of §2 — one hand-written case file per batch,
run once, reviewed, discarded. When `.github/uncovered-guards.baseline` holds no
`burn-down(BEO-69)` line and `.github/deadcode.allow` holds no `burn-down(BEO-63)` line,
there are no batches, and §2 describes a script nobody has a reason to invoke. At that point
there are exactly two honest outcomes and no third: the derived case list of
[W2](#work-this-creates) exists and §2 becomes a real check, or `mutation-check.py` and this
section are **deleted together**. A batch tool kept "just in case" after its batches end is
the dormant machinery ADR-0008 forbids.

**Dies when its register does.** §4 and §3.2 exist because waivers exist. If a lane ever
reaches zero waivers and is switched to refusing new ones, both go with it. That is not
foreseeable from here and is recorded as the condition rather than as a plan.

**Does not die.** §3.1, §3.3, §3.4 and §5 are not burn-down rules. Deleting code, waiving a
branch, and reading a recorded reason are permanent activities in this repository, and each
of these four is a specific way of getting one of them wrong that has already happened at
least once. They have no removal condition, and saying so is more useful than inventing one.

**§6 is superseded, not removed, by measurement.** Every site 6.2 covers leaves its scope
the moment a lane executes it. If a Windows lane lands, the six `_lock_windows.go` sites
stop needing a sentence — they will be measured, and the guards lane will say so. §6 shrinks
as coverage grows and is deleted when `uncovered-guards.sh` reports zero `unmeasured` files.

## Work this creates

Named, not done. None of it is in the PR that carries this ADR.

* **W1 — land #511.** Until then §2 has no address at all: the script and its only case file
  are on an unmerged branch.
* **W2 — derive the mutation case list, or declare permanently that §2 is a batch tool.**
  A hand-written TSV cannot become a gate (§1). The blocking piece is test attribution: a
  merged coverage profile says a block was executed, not *which test* executed it, and §2
  needs the pair. Per-test profiles are the obvious route and their cost is unmeasured.
* **W3 — check that `Premise pinned by <TestName>` names a test that exists.** Small: read
  the fifth column of the baseline, match the phrase, confirm `go test -list` finds each
  name. Closes the §4 gap where deleting a premise test leaves its waivers green.
* **W4 — give `uncovered-guards.sh` an `OPEN-BUG` marker with reachability-lane parity**
  (§5), so a refusal branch uncovered because of a production bug cannot be filed as debt.
* **W5 — repoint the five `ADR-0009` citations and decide where layer 1's record lives.**
  This ADR does not do it: one of the five is in `.github/deadcode.allow`, which the PR
  carrying this document is scoped out of touching. Whether layer 1 gets its own ADR or the
  citations point at this one is a call for whoever picks it up.
* **W6 — a `windows-latest` test lane** (§6.3), sized against the matrix cost. Would move
  six unmeasured refusal sites into the guards lane.

## Alternatives rejected

**Put these rules in `CLAUDE.md` instead of an ADR.** `CLAUDE.md` already carries the layer-1
lanes and is the right place for "run this command". It is the wrong place for a decision
with a rejected alternative and a removal condition — §6.1 rejects a specific proposal on
specific evidence, and §4 corrects a rule that was already in use. `CLAUDE.md` has no
register for either, and its own maintenance rule asks for pruning rather than accumulation.
A pointer from `CLAUDE.md` to this file is the right size of overlap.

**Write only the rules that have an enforcement address, and drop the rest.** This would
delete §3 entirely — four rules, each of which caught a defect the lanes did not. It also
inverts the point: the rules with no address are exactly the ones that vanish when the
person carrying them is not on the run, which is the entire reason this document exists.

**Make the whole set a required check by adding a "layer 2" job to `ci.yml`.** Rejected as
premature on §1's grounds: with a declared case list, the job would be green on a PR that
adds a guard, adds a tautological test, and adds no row — a check that cannot fail is a
false claim of protection, which is the ground ADR-0016 rejected
`required_conversation_resolution` on. W2 first, then this.

**State §6 strongly and forbid unmeasurable code.** Rejected in §6.1: it contradicts
behaviour pinned by two fixtures and would make the baseline platform-dependent.

## Consequences

* Ten rules carried in dispatch comments are now in the repository, each with the address
  that enforces it or an explicit statement that nothing does.
* Four of them (§2) are mechanically enforced, by a script that is not yet on `main` and is
  not a required check. Five (§3, §4a-b) have no address and are recorded as owned in the
  ADR-0016 sense. The rest are split, and the split is written out rather than averaged.
* The rule set is now falsifiable: §4 says a waiver must declare how to invalidate itself,
  and §6.3, §5 and the removal conditions apply that standard to this document's own claims.
* Three corrections stand against the brief this was written from — `mutation-check.py`'s
  location, the 348 figure, and the waiver form that already exceeded its stated rule.
* One defect is recorded and deliberately not fixed here: five citations of "ADR-0009" mean
  a document that was never committed, and the invariant that would normally catch a dead
  ADR reference reads path form only.
* The gap is stated, not closed. Layer 2 fails open by construction, and until W2 exists a
  PR can add a guard, a test that asserts nothing about it, and no case row, and every
  required check stays green.
