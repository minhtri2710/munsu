# 0014. The Shell Channel Is Decided on Its Write Targets, and Refusal Is Scoped to the Bound Repository

* **Status:** Accepted
* **Date:** 2026-08-17
* **Extends:** ADR-0009 (checkout identity has one owner: `fleet.ClassifyIdentity`)
* **Follows:** BEO-62 (`849b05c`, one channel per tool payload), BEO-47 (`869319d8`, `bootstrap.IsBoundRepository`)
* **Triggered by:** BEO-73 review evidence (Security Reviewer, six harness shapes)

## Context

After `849b05c` there are three payload channels into `munsu integrate safety-check`:
the native file-write channel (`--file-path`), the apply-patch channel, and the shell
channel (`--command`). The first two decide on the **target** of the write. The third
decided on nothing but the **session location** (`checkPath`).

Measured on `fafd6ca`, on all six output shapes (`claude`, `codex`, `grok`, `opencode`,
`agy`, `pi`), a soldier standing in its own valid worktree writes the shared checkout
through the shell channel unopposed:

```
cwd=<worktree>  "echo pwned > <shared>/README.md"          exit=0
cwd=<worktree>  "sed -i '' s/a/b/ <shared>/README.md"      exit=0
cwd=<worktree>  "cd <shared> && echo pwned > README.md"    exit=0
```

The control directions block correctly, so this is not a fixture artifact: the same
command with `cwd=<shared>` is refused, and `--file-path <shared>/README.md` is refused.
The shell channel simply had no target model at all — `evaluateGitMutationSafety` returns
early for every non-git segment (`internal/cli/git_worktree_safety.go:47`), and nothing
else reads a shell argument.

A second question sat on the same path. `bootstrap.SafetyCheck` fails **closed** when the
session `cwd` classifies as `Unrelated` (`integration_integrate.go:607`). That refusal
fires regardless of what the call actually writes, so a call whose target is provably the
bound worktree is refused for standing in `/tmp`.

## Decision

### 1. The shell channel is decided on its write targets, through the existing owner

`shellWriteTargets` (`internal/cli/shell_write_safety.go`) extracts the paths a command
names as write targets; `runSafetyCheck` hands every target — shell, patch and native
alike — to `evaluateFileWriteSafety`, the function the native channel already used, which
asks `fleet.ClassifyIdentity` and then `bootstrap.IsBoundRepository`. No new path
comparison is invented; the `commonDir` vs `binding.CommonDir` comparison BEO-50 settled
stays the only one.

`cd` inside the command line moves the base every relative target resolves against. This
is what closes the `cd <shared> && echo pwned > README.md` variant that a pure
absolute-path scan would miss.

Quoting survives tokenization here (`shellSegments`), because `echo "x > f"` writes
nothing while `echo x > f` writes `f`, and the two are indistinguishable once quotes are
dropped — the existing `splitSafetySegments` drops them.

**A heredoc body is content, not a command line** (`stripHeredocBodies`). This is the same
"one payload, one channel" rule BEO-62 settled for apply-patch, applied to the shell
channel. Splitting a body at its newlines and tokenizing each line turned document text
into write targets and let a `cd` line inside a document move the resolution base for the
real commands after the terminator — refusing writes that must go through. Read
redirections (`<`, `<<<`) drop their operand for the same reason: they name a source, never
a target.

The delimiter word is read the way bash reads it, including `\` as a quoting form
(`<<\EOF` ends at `EOF`). Getting that wrong is worse than the surface §2 leaves open: an
unrecognized delimiter swallows the rest of the payload, so it blinds the guard to every
later command, including the covered ones — while `python -c` only opens the one command
that names it.

### 2. The claim of coverage is narrow by construction, and everything outside it is open

This is the constraint that keeps the guard from stalling runs. The claim is exactly:

* an unquoted `>` / `>>` / `>|` redirection target, and
* the argument tokens of a **named write verb** (`rm`, `mv`, `cp`, `tee`, `touch`,
  `mkdir`, `truncate`, `install`, `ln`, `rsync`, `chmod`, `chown`, `dd of=`, and
  `sed`/`perl` with an in-place flag) — including the destination `cp`, `mv`, `ln`
  and `install` name through `-t DIR` / `--target-directory=DIR` rather than in last
  position. Only those four: `rsync` spells `-t` as `--times`, and asking it the
  same question reads a *source* as a destination — a refusal on a legitimate run,
  which is the one outcome this section forbids.

Everything else is open, explicitly and by design:

* **Reads are never targets.** `cat`, `grep -r`, `go build`, `rg` pointed at the shared
  checkout stay allowed. Reading the shared checkout as a reference is ordinary, frequent,
  legitimate work; blocking it is the "every agent run freezes" failure mode this guard
  must not have. This is why the design is a write-verb allowlist and not a path scan.
* **A verb not on the list is open** — including interpreters (`python -c`, or a script fed
  through a heredoc) that write through absolute paths, and wrappers (`sudo`, `xargs`,
  `env`). These are not claimed. The `cwd` ladder still covers them when the session sits in
  the shared checkout; the absolute-target dimension for interpreters remains open and is
  stated as such.
* **`$(...)` / backtick substitution is open** on this path: a target the shell computes is
  not knowable here. (The git ladder still refuses substitution for git mutations.)

Because tokenization cannot fail — `splitSafetyWords` always returns a token list — this
channel has **no unparseable state**, and therefore inherits none of the fail-closed
obligation `applyPatchTargets` carries. That obligation exists because a declared-covered
tool must not pass unexamined on a broken payload; a claim this narrow never reaches that
condition. Making the claim wider would drag the obligation with it, and "shell command
that does not parse" is a far more common state than "malformed patch".

### 3. The `Unrelated` refusal is scoped to what this guard protects

`bootstrap.SafetyCheck` keeps its `cwd` rules **unchanged**. The narrowing is applied by
the caller (`runSafetyCheck`) and only on the target-classification path:

> When a call names at least one write target, every one of those targets is outside the
> bound repository's primary checkout, and a worktree binding exists to compare against —
> then an `Unrelated` **cwd** is not by itself a reason to refuse.

Three bounds, all deliberate:

1. **Only the target path is relaxed.** A call that names no write target (`echo hi`,
   `ls`) is still refused on an `Unrelated` cwd, exactly as before.
2. **Only when there is a binding to compare.** `BoundRepositoryCommonDir` returning
   `false` — no `MUNSU_HOME`, no `MUNSU_TASK_ID`, unreadable authority — leaves current
   behaviour untouched. No binding, no relaxation.
3. **Only that one refusal.** A classification error, a present gate, or a bound-primary
   cwd all still refuse; the narrowing checks the refusal it is narrowing.

This is a **relaxation of a security rule**, recorded here rather than folded into a
"fix the target" commit. Its basis: the guard exists to stop writes into the *shared
checkout of the bound repository*. Another repository, a scratch directory, a reference
clone — not its business, and refusing them reproduces exactly the false positive BEO-50
measured, where `Primary` was read as "every git repo" instead of "the bound shared
checkout".

### 4. Sibling worktrees are out of scope

Writing into another task's worktree is not refused, on any channel. A sibling worktree is
not the primary checkout, and this guard protects the shared checkout rather than
arbitrating between concurrent tasks. Adjudicating task-to-task isolation is a different
invariant and belongs to a different issue; it is deliberately not smuggled in here.

Note the asymmetry this leaves standing, so a later reader does not mistake it for an
oversight: `validateGitTargetBinding` *does* refuse git mutations aimed at a sibling
worktree, because a git mutation is checked against the binding rather than against
checkout identity. File writes are not.

## Fail direction, stated once

`IsBoundRepository` and every target-classification path fail **open**: missing
environment, unreadable authority, unclassifiable path → allowed. The git-mutation path
fails **closed** in the same situations (`git mutation worktree binding unavailable`). The
two paths agree on the *definition* of the protected repository and disagree on the
*direction of failure*, deliberately: a refused git mutation costs a retry, a refused file
write costs the run. This ADR does not change either direction — it is recorded so the
next change to this area does not flip one of them silently.

## Consequences

* The shell channel now refuses on target, on all six harness shapes, since they share
  `runSafetyCheck`.
* An `Unrelated` cwd combined with a provably-safe target no longer refuses. Every other
  `Unrelated` refusal is unchanged.
* The residual open surface is written down above rather than implied: interpreters,
  wrappers, shell-computed targets, sibling worktrees.
