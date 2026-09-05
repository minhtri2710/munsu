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

Recording them was the original scope of this ADR. It changed no code, no workflow, and
neither register. Where a rule needed machinery that did not exist, the machinery was named
as work and not built there. Section 2.5 is a later layer-1 amendment: it records the
mechanical identity ratchet with disclosed growth now enforced by the coverage lane.

### What is actually in the tree, and what is not

Two factual corrections to the brief this ADR was written from:

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

### 2. Rules enforced mechanically

§§2.1–2.4 recorded four mutation-scoring rules enforced by `mutation-check.py`, a batch tool run by hand per burn-down batch. They and the tool were removed on 2026-09-05 under the [Removal condition](#removal-condition): the tool observed nothing on `main`, its batches had ended, and keeping it was the dormant machinery ADR-0008 forbids. §2.5 below is retained under its original number so cross-references to it hold.

**2.5 — The uncovered-guards baseline is an identity ratchet with disclosed growth.** The
authoritative implementation is `.github/scripts/uncovered-guards.sh check`, with its derived
site identity owned by `.github/scripts/guardsites`. The identity is exactly `(file, function,
ordinal, predicate)`; the fifth-column reason and any comments are metadata except for the
reserved growth acknowledgment described below.

The complete ratchet dispositions are:

* Equal identity sets pass; reason and comment edits on existing identities pass.
* Pure shrink passes.
* Pure growth passes only when every added row's reason begins with
  `growth(#<issue>): <reason>`. The acknowledgment discloses which issue introduced the new
  guard class and preserves the row's substantive explanation. The check still lists every
  added identity, so a green result cannot hide the scope of the growth.
* Undisclosed growth fails and enumerates every added identity.
* Any mixed removal and addition fails, including equal-count replacement, even when the added
  rows carry valid acknowledgments. Growth disclosure is not a way to replace one identity
  with another; replacement must be split into a separately reviewable shrink and growth.
* Missing current or base data fails closed. Malformed current or base rows also fail closed: every data row must contain exactly five tab-separated columns, with no empty field and a positive decimal `nth` ordinal in the third column.

This policy is necessary because a genuinely new guard class must be able to land with its
waivers disclosed. Issue #614 is the pinned example: it derives **+311 refusal sites** and
adds **+27 baseline identities**, all acknowledged with `growth(#614): ...`; the remaining
284 sites are covered in the executable `disclosed-growth-614` fixture. Rejecting all growth
would make the guard-class improvement unable to land through the same required check. The
acknowledgment is deliberately per identity and machine-validated, rather than relying on a
PR description alone.

This ratchet is additional to the existing bidirectional coverage checks, which still require
every current uncovered site to be listed and reject listed sites that are covered or no longer
exist. In production, the exact base is selected through `GUARDS_BASE_REF`, which CI supplies as
the pull request base SHA or, for a push, the predecessor commit. The `GUARDS_BASELINE_BASE` seam
exists only for executable fixtures; it is not a production input. CI fetches that exact ref
rather than assuming `origin/main`, so stacked changes compare against their immediate parent.

### 3. Rules only a reader enforces

**Address: none. Each is stated here because stating it is the whole of the enforcement.**

**3.1 — Do not assert on `err != nil` at a guard site.** It is green for any error,
including one raised by a guard that runs *before* the one under test. The assertion must
name what this guard refuses.

This rule had a partial machine address until 2026-09-05: a test that only checks `err != nil` SURVIVES its mutant, and the mutation-scoring rules formerly in §2 caught it — but only for a branch somebody put in the case file, and only after somebody ran the script. With those rules and their tool removed, nothing sees it, and 3.1 is enforced by a reader alone.

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
`::warning` on every run. Address for (c) in the coverage lane: the same mechanism, added
as `announce_open_bugs` in `.github/scripts/uncovered-guards.sh` (issue #542) with the reason
read from the baseline's fifth column; the `open-bug` fixture pins it.**

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
exists; the file reads as if it only ever held debt. That asymmetry is closed as of issue #542:
`uncovered-guards.sh` carries the same marker, so a refusal branch uncovered because of a
production bug is announced on every run instead of being waivable as though it were debt.

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

**A narrower form of the same demand was asserted in review, retracted, and is rejected
here by name.** During the BEO-69/BEO-122 Windows batch, a review comment claimed this ADR
requires every `_unix.go`/`_windows.go` pair sharing a function name to have a `_test.go`
that the `goos-vet` lane compiles. It does not, and the claim was withdrawn (issue #543);
this paragraph records it so nobody re-derives it from the comment it appeared in. The
claimed rule is the strong form of §6.1 with the objection narrowed from "no lane measures
it" to "the lane must compile a test of it", and it fails on the same three grounds. First,
it is the strong form: §6.1 already declines the demand that GOOS-gated code carry
lane-measurable coverage, on fixture-pinned grounds rather than cost. Second, the tree does
not satisfy it: at `91551afc` four of the twelve `_unix.go`/`_windows.go` pairs share a
function name no test the lane compiles references — `cli/watch_process`'s
`processIsAlive` and `signalWatchProcess`, `fleet/process_runtime`'s `isProcessMissing`,
`home/taskmeta_lock`'s `lockExclusive` and `unlockFile`, `home/watcher_lock`'s
`lockWatcherFile` and `unlockWatcherFile`; five of the twelve if a `!windows` test only the
darwin half of the lane compiles (`canonical_lock_errno_unix_test.go` on
`lockScopedFile`/`unlockScopedFile`) is not allowed to satisfy it. Adopting it would not
tighten a mostly-held rule; it would declare a third of the platform-split pairs
non-compliant on the spot, each a burn-down of its own. Third, where a production call site
already compiles in the same lane it was measured to add nothing: the `var _ f = g`
assertions it would have demanded guard functions whose signature drift is already caught
by live call sites — `lockWakeFile`/`unlockWakeFile` at `wake_lease.go:58,61`,
`isProcessAlive` at `watcher_lease.go:47,107` and `supervision_watcher.go:237`,
`stopProcess`/`stopProcessIsLossy` at `afk_return.go:134,178`, `signalWatcherProcess` at
`supervision_watcher.go:213` — that `GOOS=windows go vet ./...` compiles before any test
file matters. Making the rule real would require an ADR of its own and a burn-down of the
violating files; a review comment is neither.

**6.2 — The declaration obligation is accepted, and its home is the artifact, not the PR
body.** Where a lane cannot reach, the limit is stated, and stated as a limit rather than
as a result. But a PR body is not in the repository, nothing reads it, and it is not
where anyone looking at the code will be standing. This repository already puts these
sentences in the right place and should keep doing so:

* `.github/build-tags.manifest` classifies `windows` as `goos-vet` and states its limit in
  the row — the coverage is the `GOOS=windows go vet ./...` compile plus the native
  `windows-build-vet` gate (#544), no figure (the file's header explains why a count no job
  verifies is not carried). That row is gated — an
  unclassified tag turns `invariants` red — so the *existence* of a declaration is enforced
  even though its content is not.
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
`internal/orchestrator/afk_process_identity_darwin.go`. In `ci.yml`, every job runs on
`ubuntu-latest` except the `windows-build-vet` gate (#544), which builds and vets natively
on `windows-latest`; Windows coverage is therefore the `GOOS=windows go vet ./...` compile
plus that gate. Both compile the Windows `_test.go` files (vet includes test files)
and neither executes them — only the dispatch-only windows-observation lane does. The gate
produces no coverage profile, so the eight sites stay unmeasured.

Eight is small enough that 6.2 costs eight sentences, and large enough that a
`windows-latest` *test* lane would be worth its cost — it would move six of them from
unmeasured to measured. But the two answer different questions and neither waits on the
other: 6.2 governs what may be **claimed**, a Windows lane changes what can be **measured**.
6.2
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

**Died with the burn-down (2026-09-05).** The *cadence* of §§2.1–2.4 — one hand-written case file per batch, run once, reviewed, discarded. When `.github/uncovered-guards.baseline` held no untriaged batch rows and `.github/deadcode.allow` held no untriaged batch rows, there were no batches, and those rules described a script nobody had a reason to invoke. Of the two honest outcomes named here — the derived case list of [W2](#work-this-creates) exists and §2 becomes a real check, or `mutation-check.py` and those rules are **deleted together** — the second was taken: the tool, its case files, §1 and §§2.1–2.4 were deleted on 2026-09-05. A batch tool kept "just in case" after its batches end is the dormant machinery ADR-0008 forbids.

**Dies when its register does.** §4 and §3.2 exist because waivers exist. If a lane ever
reaches zero waivers and is switched to refusing new ones, both go with it. That is not
foreseeable from here and is recorded as the condition rather than as a plan.

**Does not die.** §3.1, §3.3, §3.4 and §5 are not burn-down rules. Deleting code, waiving a
branch, and reading a recorded reason are permanent activities in this repository, and each
of these four is a specific way of getting one of them wrong that has already happened at
least once. They have no removal condition, and saying so is more useful than inventing one.

**§6 is superseded, not removed, by measurement.** Every site 6.2 covers leaves its scope
the moment a lane executes it. If a Windows *test* lane ever lands, the six
`_lock_windows.go` sites stop needing a sentence — they will be measured, and the guards
lane will say so. §6 shrinks
as coverage grows and is deleted when `uncovered-guards.sh` reports zero `unmeasured` files.

## Work this creates

Named, not done. None of it is in the PR that carries this ADR.

* **W1 — land #511. Closed unmet 2026-09-05:** #511 was not landed; §§2.1–2.4 and the script were deleted instead of gaining an address.
* **W2 — derive the mutation case list, or declare permanently that §2 is a batch tool. Closed 2026-09-05:** the case list was not derived; the second removal-condition outcome was taken and §§2.1–2.4 were deleted, so the choice no longer stands open.
* **W3 — check that `Premise pinned by <TestName>` names a test that exists.** Small: read
  the fifth column of the baseline, match the phrase, confirm `go test -list` finds each
  name. Closes the §4 gap where deleting a premise test leaves its waivers green.
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

**Make the whole set a required check by adding a "layer 2" job to `ci.yml`.** Rejected as premature on the grounds the removed §1 recorded (§1 was deleted 2026-09-05): with a declared case list, the job would be green on a PR that adds a guard, adds a tautological test, and adds no row — a check that cannot fail is a false claim of protection, which is the ground ADR-0016 rejected `required_conversation_resolution` on.

**State §6 strongly and forbid unmeasurable code.** Rejected in §6.1: it contradicts
behaviour pinned by two fixtures and would make the baseline platform-dependent.

## Consequences

* Ten rules carried in dispatch comments were brought into the repository, each with the address that enforces it or an explicit statement that nothing does; the four mutation rules among them (§§2.1–2.4) were removed on 2026-09-05 with their tool.
* The four mutation rules formerly in §§2.1–2.4 were mechanically scored by `mutation-check.py`, a script never on `main` and never a required check; they and the script were removed on 2026-09-05 and nothing now enforces them. The §2.5 coverage ratchet is mechanically enforced by a required CI check, alongside the bidirectional coverage comparison. Five (§3, §4a-b) have no address and are recorded as owned in the ADR-0016 sense. The rest are split, and the split is written out rather than averaged.
* The rule set is now falsifiable: §4 says a waiver must declare how to invalidate itself,
  and §6.3, §5 and the removal conditions apply that standard to this document's own claims.
* Two corrections stand against the brief this was written from — the 348 figure, and the waiver form that already exceeded its stated rule.
* One defect is recorded and deliberately not fixed here: five citations of "ADR-0009" mean
  a document that was never committed, and the invariant that would normally catch a dead
  ADR reference reads path form only.
* The gap is stated, not closed. Layer 2 — recorded in the former §1, removed 2026-09-05 — failed open by construction and was deleted rather than derived into a gate. A PR can still add a guard, a test that asserts nothing about it, and no case row, and every required check stays green: §2.5's ratchet catches an *uncovered* guard, not a guard covered by a tautological test.
