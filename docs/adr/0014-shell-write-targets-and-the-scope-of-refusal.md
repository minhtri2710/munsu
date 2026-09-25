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
asks `fleet.ClassifyIdentity` and then `bootstrap.IsBoundRepository`. No new repository
identity comparison is invented; the `commonDir` vs `binding.CommonDir` comparison BEO-50
settled stays the only one.

`cd` inside the command line moves the base every relative target resolves against. This
is what closes the `cd <shared> && echo pwned > README.md` variant that a pure
absolute-path scan would miss. Shell lexical resolution is owned by
`resolveShellWritePath`; final repository classification remains with
`evaluateFileWriteSafety`. On Windows, a volume-less rooted path such as
`\src\repo\README.md` or `/src/repo/README.md` inherits the shell's active
volume rather than being treated as a relative child of the worktree. A same-volume drive-relative path such as
`C:src\repo\README.md` resolves against the base directory, using
case-insensitive volume comparison. A different-volume drive-relative path such
as `D:src\repo\README.md` is ambiguous because the other drive's current
directory cannot be reconstructed. The command is refused when that target has
no independently resolved candidate under the other backslash interpretation;
ambiguity from one reading does not override a rooted or otherwise independent
candidate for the same target span. An unresolved different-volume drive-relative
`cd` records unknown state for that volume. Read-only commands and independent
absolute or rooted targets remain classifiable, while relative writes are
currently refused whenever the active volume is unknown; same-volume
per-volume-cwd restoration is implemented for known volumes but does not yet
narrow that active-volume refusal. An expandable `cd` operand has the same
unknown-cwd effect: read-only commands and independent absolute or rooted writes
remain classifiable, while dependent relative writes are refused.

Quoting survives tokenization here (`shellSegments`), because `echo "x > f"` writes
nothing while `echo x > f` writes `f`, and the two are indistinguishable once quotes are
dropped — the existing `splitSafetySegments` drops them. Backslash interpretation is
quote-aware: outside quotes the POSIX reading quotes the next character while the
Windows reading preserves the backslash as a path separator; inside single quotes the
backslash is literal, and inside double quotes the POSIX reading only treats `$`, backtick,
`"`, `\\`, and newline specially. Resolved candidates from both readings are unioned because the harness does not identify
which shell will execute the command; ambiguity is decided independently for each target
span, and a span remains ambiguous only when neither reading resolves it. A `$` or backtick inside
single quotes, or behind a POSIX-valid backslash escape, is literal and remains
classifiable; genuinely expandable target tokens are omitted because their paths
are not knowable to the guard.

**A heredoc body is content, not a command line** (`splitHeredocBodies`). This is the same
"one payload, one channel" rule BEO-62 settled for apply-patch, applied to the shell
channel. Splitting a body at its newlines and tokenizing each line turned document text
into write targets and let a `cd` line inside a document move the resolution base for the
real commands after the terminator — refusing writes that must go through. Read
redirections (`<`, `<<<`) drop their operand for the same reason: they name a source, never
a target.

The delimiter word is read once using POSIX backslash-quoting rules: `<<\EOF`
ends at bare `EOF`, and both tokenization passes then use that same stripped
body. `readHeredocRedirect` removes the quoting backslash while reading the
delimiter, so the later literal-backslash tokenization pass does not retain it
as part of the delimiter.
This keeps either pass from swallowing the remaining payload and missing later
covered commands — while `python -c` only opens the one command that names it.

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

A named write verb is found where bash finds the command word, and inside the payloads
bash runs:

* **Command position** (`commandPosition`): the command word is the first word after every
  assignment (`X=1`), reserved word (`if`, `then`, `while`, `{`, …) and `!`, and after
  every wrapper with its options. The wrappers are a fixed list, `wrapperVerbs`, chosen by
  one principle — a command that runs the rest of its argv as a command: `command`,
  `builtin`, `exec`, `env`, `nohup`, `time`, `nice`, `timeout`, `sudo`, `setsid`,
  `stdbuf`. Each option grammar was measured with a bash 3.2 and 5.3 probe on macOS, and
  taken from GNU where macOS lacks the command or the option. A wrapper option that sets
  the command's directory (`env -C`, `sudo -D`) moves the base its targets resolve
  against; one the shell computes makes relative targets ambiguous, and a changed root
  (`sudo -R`) makes every target ambiguous. `command -v`/`-V` and `type` run nothing and
  name nothing. The file `time -o`/`--output` writes is a target.
* **Subshells and cd** (`tokenizeSegments`, `builtinCommand`): an unquoted `(` that starts
  a word, and a `)` that closes none opened inside a word, are operators, as bash reads
  them, so `(rm x)` and `((rm x))` reach the verb; `$(`, `<(`, `>(`, `$((` and quoted
  parens are unchanged. A `cd` inside `( ... )` returns at its `)`. Both guards read a
  segment's `cd` through `builtinCommand`, past assignments, reserved words, `time` and the
  `builtin` and `command` prefixes that still run it in this shell (`builtin cd <shared>
  && rm f`); a `cd` behind any other wrapper (`env cd`) runs outside the shell and moves
  nothing.
* **Named shell payloads** (`shellPayloads`): the `-c` operand of `bash`, `sh`, `zsh`,
  `dash` or `ksh` (`shellConsumers`), in any option form bash accepts (`-lc`, `-l -c`,
  `-o name -c`); the heredoc body or here-string such a shell reads as its script when it
  has no `-c` and no script operand, or has `-s`; the words of `eval`, joined by single
  spaces as bash joins them; and the string `env -S` splits. An `eval` payload runs in the
  same shell, so its `cd` moves the segments after it; a shell's payload runs in a child
  that starts in its parent segment's directory, and its `cd` does not leak out. A payload
  nested past `maxShellPayloadDepth`, or past `maxWriteReadings` payloads in one command,
  is not read: its segment is ambiguous, and the hook refuses on the write guard's own
  verdict, not on the git guard's.

Only a named consumer's payload is read. The git guard reads every word that reads as more
than itself (`evaluateGitMutationSafety`); the write guard deliberately does not. A refused
git mutation costs a retry, a refused file write costs the run, and under the git guard's
rule `git commit -m "rm <shared>/x"` and `grep "> <shared>/x"` would be refused. This
asymmetry is a choice, not a gap.

Everything else is open, explicitly and by design:

* **Reads are never targets.** `cat`, `grep -r`, `go build`, `rg` pointed at the shared
  checkout stay allowed. Reading the shared checkout as a reference is ordinary, frequent,
  legitimate work; blocking it is the "every agent run freezes" failure mode this guard
  must not have. This is why the design is a write-verb allowlist and not a path scan.
* **A verb not on the list is open, and so is a runner not in `wrapperVerbs`.** The wrapper
  list is fixed, not a class: `arch`, `caffeinate`, `xcrun`, `doas`, `ionice`, `chronic`,
  `xargs`, `find -exec`/`-execdir`, `parallel` and `watch` stay open. So do `source`/`.`,
  script files (a shell given a script operand), a pipe into a shell (`echo "rm x" | sh`, whose payload is
  another command's output), and interpreters other than the named shells (`python -c`,
  `perl -e` without `-i`, `node -e`), which write through absolute paths unclaimed. The
  `cwd` ladder still covers them when the session sits in the shared checkout.
* **A payload the shell computes is open** (`bash -c "$cmd"`, `eval "$cmd"`): like a
  computed target, its text is not knowable here.
* **Stdin given to a group is open**: a heredoc or here-string after a subshell's `)` or a
  brace group's `}` (`(bash) <<EOF`, `{ bash; } <<EOF`) is not read as the shell's payload.
* **`$(...)` / backtick substitution is open** on this path: a target the shell computes is
  not knowable here. (The git ladder still refuses substitution for git mutations.)

Because tokenization cannot fail — `tokenizeSegments` always returns a segment list — this
channel has **no unparseable state**. It does, however, carry explicit fail-closed states:
for ambiguous cross-volume drive-relative paths, when `resolveShellWritePath` cannot
reconstruct the other drive's current directory, and for a payload too deep or too
numerous to read, `runSafetyCheck` refuses before target classification. Apart from that
ambiguity, the narrow parser claim inherits none of
the fail-closed obligation `applyPatchTargets` carries. That obligation exists because a
declared-covered tool must not pass unexamined on a broken payload; a claim this narrow
never reaches that condition. Making the claim wider would drag the obligation with it,
and "shell command that does not parse" is a far more common state than "malformed patch".

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

`IsBoundRepository` and target-classification failures fail **open**: missing
environment, unreadable authority, unclassifiable path → allowed. Shell ambiguity is the
exception: a target span for which every backslash interpretation depends on an
unreconstructable different-volume cwd, or a payload too deep or too numerous to read, is
refused before classification. An independently
resolved candidate for that span is still classified normally. The git-mutation path fails
**closed** in the same situations (`git mutation worktree binding unavailable`). The two
paths agree on the *definition* of the protected repository and disagree on the *direction
of failure*, deliberately: a refused git mutation costs a retry, a refused file write
costs the run. This change leaves those classification directions unchanged; the shell
ambiguity refusal occurs before classification. The distinction is recorded so the next
change to this area does not flip either behavior silently.

## Consequences

* The shell channel now refuses on target, on all six harness shapes, since they share
  `runSafetyCheck`.
* An `Unrelated` cwd combined with a provably-safe target no longer refuses. Every other
  `Unrelated` refusal is unchanged.
* The claim covers named write verbs reached through command-position prefixes, the fixed
  `wrapperVerbs` list and named shell payloads. The residual open surface is written down
  above rather than implied: runners outside `wrapperVerbs` (`arch`, `caffeinate`, `xcrun`,
  `doas`, `ionice`, `chronic`, `xargs`, `find -exec`, `parallel`, `watch`), `source`,
  script files, pipes into a shell, stdin given to a subshell or brace group, interpreters
  other than the named shells, shell-computed payloads and targets, and sibling worktrees.
