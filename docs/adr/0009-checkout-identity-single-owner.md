# 0009. Checkout Identity Classification Has One Owner

* **Status:** Accepted
* **Date:** 2026-08-14
* **Extends:** ADR-0008 (one owner and one canonical implementation path per lifecycle)
* **Triggered by:** BEO-46 audit finding C1 → BEO-48 → BEO-51 architecture review

## Context

Two independent pieces of code answered the same question — *is this checkout the
primary clone or a linked worktree?*

1. `backend.IsIsolated` / `backend.EnsureNotPrimary` (`internal/backend/worktree.go`),
   written as a fail-closed guard meant to stop a Soldier from running on the
   primary checkout.
2. `fleet.ClassifyIdentity` (`internal/fleet/scope_scope.go`), which returns
   `Primary` / `Worktree` / `Unrelated` and is consumed by fleet lifecycle entry
   points.

Only the second one is on an execution path. `EnsureNotPrimary` has never had a
production call site in the repository's history: the sole non-test reference it
ever had was a pass-through re-export in `internal/backend/backend.go`, added in
`694797b` and removed in `a4f8104`, itself never called. `IsIsolated`'s only
production caller is `EnsureNotPrimary`.

BEO-23 (PR #441) fixed a real bug in `IsIsolated` — from a subdirectory of a
primary checkout, `git rev-parse` answers `--git-dir` absolutely and
`--git-common-dir` relatively, so the comparison reported a primary checkout as
isolated, i.e. the guard passed in exactly the case it existed to reject. The fix
is correct and has regression tests. It repaired a function nobody calls, so no
statement of the form "the guard is fixed, therefore checkouts are protected" was
ever true. The protection came from `ClassifyIdentity` the whole time.

The real checkout guard lives in the Soldier launch sequence
(`internal/fleet/spawn_runner.go`):

```go
// Run(): the launch sequence, in order
if err := r.acquireWorktree(); err != nil { ... }   // line 222
if err := r.bindWorktree(); err != nil { ... }      // line 225  ← guard
...
if err := r.buildSoldierPrompt(); err != nil { ... } // line 236 (FailClosedDuringLaunch + PersistLaunchFiles)
if err := r.createSession(); err != nil { ... }      // line 242
if err := r.submitLaunch(); err != nil { ... }       // line 251
if err := r.writeLaunchManifest(); err != nil { ... }// line 254
```

```go
// buildTaskWorktreeBinding, spawn_runner.go:1047
identity, gitDir, commonDir, err := ClassifyIdentity(canonicalWorktree)
if err != nil {
    return taskauthority.WorktreeBinding{}, err
}
if identity != Worktree {
    return taskauthority.WorktreeBinding{}, fmt.Errorf("worktree binding target is %s, not worktree", identity)
}
```

`bindWorktree` is unconditional in `Run()` and fails closed when the Task
Authority is not composed. `r.wtPath` is assigned only in `acquireWorktree`
(lines 734, 758) and `bindWorktree` (line 1001) and never afterwards, so every
write and every launch step downstream of line 225 operates on a path that
`ClassifyIdentity` has already classified as `Worktree` — or the spawn has
already been refused.

## Decision

### 1. `fleet.ClassifyIdentity` is the single owner of checkout identity classification

Any code that needs to know whether a path is a primary checkout, a linked
worktree, or unrelated to any repository calls `fleet.ClassifyIdentity`. No
module re-derives this from `git rev-parse --git-dir` / `--git-common-dir`.

`ClassifyIdentity` owns the classification; it is deliberately **not** a guard.
Each consumer states its own admission rule over the returned `Identity`, and
the rule is a whitelist:

* `buildTaskWorktreeBinding` (`spawn_runner.go:1051`) admits only `Worktree`.
* `authorizeSpawn` (`spawn_runner.go:396`) refuses only `Worktree` callers —
  the opposite direction, same owner.
* `validateGitTargetBinding` (`internal/cli/git_worktree_safety.go:277`) admits
  only `Worktree` for a git mutation. It reaches the classification through
  `gitSafetyIdentity` (`:354`), which is a rendering step — it calls
  `ClassifyIdentity` and maps the `Identity` to the string the gate compares —
  not a second derivation.

That last one is the reason this ADR is a rule rather than an observation.
`gitSafetyIdentity` used to re-derive the answer itself, with its own
`rev-parse --git-dir` / `--git-common-dir` comparison and its own path
canonicalization, and unlike the copy in `internal/backend` it was *live*. Two
independent derivations feeding one comparison is also a correctness problem in
its own right: the git-dir and common-dir the gate checks against a
`WorktreeBinding` are now canonicalized by the same code that wrote them.

### 2. `backend.IsIsolated` and `backend.EnsureNotPrimary` are removed

Both are deleted together with the tests that pin them and the private helpers
that become unreachable (`resolveGitDir`, `gitRevParse` in
`internal/backend/worktree.go`). `internal/backend` keeps worktree *provider*
mechanics (acquire, return, status) and stops answering identity questions.

The alternative — wiring `EnsureNotPrimary` into `PersistLaunchFiles` or
`FailClosedDuringLaunch` as defence in depth — is rejected. It would add a
second, weaker owner of the same question, strictly downstream of an existing
check that already refuses on a superset of inputs (see §4), covering no path
the existing check does not cover.

### 3. The worktree binding is the durable carrier of the classification verdict

`taskauthority.WorktreeBinding` is constructed in exactly one place,
`buildTaskWorktreeBinding` (`spawn_runner.go:1042-1076`), and only after
`ClassifyIdentity` has returned `Worktree`. Recovery and re-entry paths
(`acquireWorktree` line 733, `bindWorktree` line 997) therefore adopt
`agg.Worktree.Path` without re-classifying: the committed binding *is* the
verdict, and the reservation-fence comparison at `spawn_runner.go:998` pins it
to this launch.

This invariant is load-bearing. Any future producer of `WorktreeBinding` must
classify first, or the adopt paths silently lose their guarantee.

### 4. Fail-closed semantics

`ClassifyIdentity` fails closed on every condition under which it cannot
establish the answer, and returns `Unrelated` with a `nil` error only when it
*positively* establishes the path is not in a repository:

| Condition | Return | `err` |
|---|---|---|
| path unresolvable / symlink loop (`scope_scope.go:124-127`) | `Unrelated` | non-nil |
| path missing (`:128-131`) | `Unrelated` | non-nil |
| path not a directory (`:132-134`) | `Unrelated` | non-nil |
| `git` absent or `rev-parse` fails for any reason other than "not a git repository" (`:136-139`, `gitPath` `:110`) | `Unrelated` | non-nil |
| positively not a repository (`:140-142`) | `Unrelated` | nil |
| `--git-common-dir` unavailable while `--git-dir` succeeded (`:147-149`) | `Unrelated` | non-nil |
| `gitDir == commonDir` (`:150-152`) | `Primary` | nil |
| otherwise (`:153`) | `Worktree` | nil |

Because the launch consumer admits only `Worktree`, `Unrelated`-with-`nil` is
refused just as firmly as an error. Overall the launch guard is strictly
stronger than the removed one: `EnsureNotPrimary` rejected `Primary` and errors,
while `ClassifyIdentity` + the whitelist rejects `Primary`, `Unrelated`, and
every classification failure — and canonicalizes with `EvalSymlinks` before
comparing, instead of falling back to the un-evaluated path the way
`resolveGitDir` did (`worktree.go:459-464`).

`fleet.GateRefuseFromCWD` (`scope_scope.go:249-253`) deliberately passes through
on a classification error. That is a different guard with a different subject —
no-mistakes gate-agent refusal, mirroring firstmate's `fm-refuse-if-gate-agent`
— and this ADR does not change it.

## Consequences

* One place to read, test, and change checkout classification. A future guard
  bug like BEO-23's is fixed once and lands on the execution path by
  construction.
* `internal/backend` no longer exports checkout identity. Callers that need it
  depend on `internal/fleet`, consistent with the ADR-0008 dependency direction
  (`fleet` → `backend`, never the reverse).
* `internal/backend`'s own tests must assert "this is a real linked worktree"
  without `IsIsolated` and without importing `fleet`. Assert the worktree marker
  locally (a `.git` *file*, not a directory) or invoke `git rev-parse` inline in
  the test.
* Protection of the primary checkout at Soldier launch now rests on a single
  check, so that check carries its own tests rather than inheriting coverage
  from the classifier. `TestBuildTaskWorktreeBinding_RefusesNonWorktree` and
  `TestBuildTaskWorktreeBinding_AdmitsLinkedWorktree`
  (`internal/fleet/spawn_worktree_binding_test.go`, default lane) pin both
  directions; `TestClassifyIdentity_*` (`internal/fleet/scope_scope_test.go`,
  build tag `integration`) pins the classifier beneath them, including
  `TestClassifyIdentity_DeletedPathFailsClosed` and
  `TestClassifyIdentity_GitUnavailableFailsClosed`.
* The git mutation gate stops carrying its own path canonicalization for
  identity. `canonicalSafetyPathRuntime` (`git_worktree_safety.go:388`) falls
  back to the un-evaluated path when `EvalSymlinks` fails — the same weakness
  §4 cites against `resolveGitDir` — so routing identity through
  `ClassifyIdentity` removes it from the classification path. It still
  canonicalizes `--show-toplevel` and the explicit `--git-dir` / `--work-tree`
  overrides, which are path comparisons, not classification.
* One behaviour change at that gate, in the refusing direction: a target
  outside any repository used to fail as `git mutation target unavailable`
  (git errored) and now fails as `git mutation target is not the bound
  worktree` (classified `Unrelated`, refused by the whitelist). Refused either
  way; only the reason text differs.

## Residual risk (out of scope for this ADR)

The fail-closed `defer` at `spawn_runner.go:217-221` may call
`backend.ReturnWorktree` on a path that `bindWorktree` has just refused as
`Primary`. The git fallback provider is safe there — `gitWorktreeProvider.Return`
reads `<path>/.git` as a file and errors out on a primary checkout, where `.git`
is a directory (`worktree.go:344-350`). The treehouse provider runs
`treehouse return --force <path>` (`worktree.go:214-220`) and the outcome is the
third-party CLI's behaviour. If defence in depth is wanted for the primary
checkout, `backend.ReturnWorktree` is the insertion point worth arguing about —
not `PersistLaunchFiles`. Tracked separately; not a reason to keep
`EnsureNotPrimary`.
