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
redirections (`<`, `<<<`, `<(...)`, and their fd prefixed forms such as `0<<EOF` or
`3</dev/null`) drop their operand for the same reason: they name a source, never a target.
A `<(...)` is read whole to the `)` that balances it, as a `$(...)` is, and a `<` word ends
at an unquoted `(` or `)`, so neither `cat <x)` nor a `)` quoted in a `<(...)` body moves a
subshell's end.
Every argv reader (the write verbs, `builtinCommand`, the directory movers) sees a segment
through `withoutRedirections`, so a redirection in any position (`2>/dev/null cd <shared>`,
`env 3</dev/null rm f`) never shifts the command word or an operand; the git guard's argv
reader (`segmentGitCommands`) does the same, so `git 2>/dev/null push --force` is read as
the push it is. Only an fd number belongs to the redirection, and only when it is unquoted
and touches the operator (`redirectPrefix`): `9 >x`, `"9">x`, `\9>x` and `12&>x` pass `9` or
`12` as an argument, as bash does. A `{name}` word is never dropped: bash 3.2 passes
`{x}>x`'s `{x}` as an argument, where 5.3 reads it as a descriptor name, so both guards read
it both ways (`descriptorName`), and `git push origin mu/<task> {x}>/dev/null` is refused. A descriptor duplication
(`>&2`, `2>&1`, `>&-`) names no file and leaves the segment with its fd; `&>` and `&>>`
redirect; neither is a background `&`.

A `<<` or `<` inside a substitution or a `${...}` group belongs to it, not to the command
line: `splitHeredocBodies` keeps both whole.

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
  assignment (`X=1`, `X+=1`, `a[i]=1`; at assignment position a subscript is read to its
  balanced `]`, blanks and operators included, as bash reads it), reserved word (`if`,
  `then`, `while`, `{`, …) and `!`, and after
  every wrapper with its options. The wrappers are a fixed list, `wrapperVerbs`, chosen by
  one principle — a command that runs the rest of its argv as a command: `command`,
  `builtin`, `exec`, `env`, `nohup`, `time`, `nice`, `timeout`, `sudo`, `setsid`,
  `stdbuf`. Each option grammar was measured with a bash 3.2 and 5.3 probe on macOS, and
  taken from GNU where macOS lacks the command or the option. A wrapper option that sets
  the command's directory (`env -C`, `sudo -D`) moves the base its targets resolve
  against; one the shell computes makes relative targets ambiguous, and a changed root
  (`sudo -R`) makes every target ambiguous. `command -v`/`-V` and `type` run nothing and
  name nothing. The file `time -o`/`--output` writes is a target. `env` and `sudo` pass
  every word holding a `=` before the command to its environment, whatever its name.
* **Subshells, groups and case** (`tokenizeSegments`): an unquoted `(` that starts a
  word, and a `)` that closes none opened inside a word, are operators, so `(rm x)` and
  `((rm x))` reach the verb; `>(` and quoted parens are unchanged. Text inside a comment
  (an unquoted `#` that starts a word, up to the newline) is no grammar: no paren, quote,
  substitution, heredoc, `case`, `esac`, `in` or `;;` in it has any effect, and its words
  stay tokenized, as bash 3.2 and 5.3 read it. A `$(...)`,
  `$((...))` or backtick substitution is one piece of its word, unquoted, inside double
  quotes and inside a `${...}` group (`substitutionEnd`): no paren, operator, quote,
  comment, heredoc or reserved word in it is grammar of the command around it, and a
  `${...}` group keeps its parens, operators and blanks in its word, so neither a `)` nor
  an `esac`, `in` or `;;` in one closes a subshell or a case. A substitution the POSIX
  reading cannot end, or a `$(...)` with a `case` at a command word (whose pattern parens
  bash 3.2 and 5.3 read apart), leaves the rest of the command an unfinished segment. The
  tokenizer keeps a construct stack of subshells, brace groups and `case` statements: a
  `case` pattern's `(` and `)` open and close no subshell, `;;`, `;&` and `;;&` return to
  the patterns, and a directory move inside a case arm moves the segments after `esac`, as
  bash runs it. A case still open at the end of the command yields an unfinished segment;
  bash runs nothing of such a line, and both guards refuse it (the write guard reads it
  ambiguous, the git guard refuses it outright), which also refuses a git commit message
  text containing an unterminated `case x in` — an accepted over-refusal.
* **Functions and coproc** (`definesFunction`, `opensScope`): every definition form
  (`f()`, `f ()`, `function f`, `function f()`) and a `coproc` body are read in their own
  scope, split by synthetic subshell tokens, so a directory move inside the body does not
  move the segments after the definition. A write in a function body is a target even if
  the function is never called: an accepted over-refusal, pinned by test. `coproc` is a
  reserved word, and its body is read in a child scope. A body of any compound form a
  reserved word ends (`if`, `while`, `until`, `for`, `select`, `case`) is scoped the same
  way; a `[[ ]]` body runs no command. Both guards record each function the command
  defines, in an `eval` payload too, whether its body moves the directory (a `cd`, `pushd`
  or `popd` at any depth of the body, `eval` and a nested subshell included, a nested
  function's body excluded: it moves when that function runs), and the
  calls its body makes (`shellFunctions`, `functionCalls`). A call resolves when
  it runs, as bash's does: a later call at command position leaves the directory
  unknown, as `popd` on an empty stack does, when the function it names or any function its
  body calls, as defined at that call, moves it. The call reads its command word decoded
  (`"f"`, `\f`, `$'f'`) and under every reading of an expansion (`${x:-f}`): any name it
  may run that moves the directory leaves it unknown. A body may so call a function defined
  after it; a redefinition replaces the earlier body, and a recursive body is read once.
  What a name resolves to is kept until the next definition or `unset -f`, and all
  resolutions in one command share one budget of `maxWriteReadings` steps, past which a
  call leaves the directory unknown. A `( )` body, a call in a subshell, or behind
  `command` leave it unchanged. A call in the background (`&`) or in a pipeline counts as
  a `cd` there does: zsh and ksh run a pipeline's last command in this shell, so
  `f() { cd P; }; : | f; rm` removes in P there. An `eval` payload
  defines its functions in the shell that runs it; a payload of a named shell works on a
  copy of the functions, so a function a `bash -c` payload defines is gone when it ends.
  A subshell works on a copy too: at its `)` the walk drops the definitions and
  `unset -f` made inside it, so `(f() { :; }); f` and `(unset -f f); f` run the outer
  `f`, as bash 3.2 and 5.3 do. A definition head after reserved words (`{ g() {`,
  `then function g`) starts a segment of its own, so a definition inside a body, a brace
  group or an `if` arm is read as one. What a function body defines and unsets is dropped
  at its close and kept on the function (`shellFunction.defines`): bash makes it when the
  function runs, so `f() { g() { cd P; }; }; g() { :; }; f; g` runs the moving `g`. A
  call replays it merged (`merge`), because whether a call runs (`false && f`, a
  candidate name) is not known: a name already defined moves unless both definitions are
  inert, and a replayed `unset -f` leaves the name as it is. Both are accepted
  over-refusals: `g() { cd P; }; f() { g() { :; }; }; f; g` is refused though bash runs
  the inert `g`, and `g() { cd P; }; f() { unset -f g; }; f; g` is refused though bash
  finds no `g`. A coproc
  body's definitions are dropped. A segment is apart when it runs in a child: a pipeline
  member, a background command, or any segment of a compound command or a function
  definition, head included, whose first or closing segment a pipeline or `&` detaches
  (`g() { :; } &`, `: | { :; g() { :; }; }`). A definition made apart (`defineHead`), or
  replayed by a call apart (`: | f`), is merged as a replay is, never overwriting or
  dropping a name: bash and dash drop it, zsh and ksh keep a pipeline's last member. A
  foreground `unset -f` whose names are plain words
  removes those functions. Any other spelling (`unset f`, a quoted name, one apart)
  removes nothing: an accepted over-refusal. As with `cd`, a conditional `unset -f`
  (`false && unset -f f`) is read as run.
* **Directory moves** (`segmentDirMove`, `builtinCommand`): `cd`, `pushd` and `popd` move
  the base relative targets resolve against, in both guards. `cd` skips `--` and options
  made only of `L`, `P`, `e` and `@`; any other option moves nothing, as bash refuses it.
  `pushd` pushes the old directory and `popd` restores it; `cd -` restores the previous
  directory. A move the guard cannot follow (`popd` on an empty tracked stack, `pushd +N`,
  `cd -` with no tracked previous directory, an expandable operand) makes the directory
  unknown: relative targets after it are ambiguous for the write guard, and the git guard
  refuses a mutation there. `cd` with no operand and a `~` operand of `cd` or `pushd` move
  to a home this does not read, and leave the directory unknown the same way. `pushd -n`
  changes only the stack. Both guards read the move
  through `builtinCommand`, past assignments, reserved words, `time` and the `builtin` and
  `command` prefixes that still run it in this shell; a move behind any other wrapper (`env
  cd`) runs outside the shell and moves nothing. A redirection on a move segment opens
  before the move, so its target resolves against the old directory. A move inside
  `( ... )` returns at its `)`. `CDPATH` is not read and stays open.
* **Named shell payloads** (`shellPayloads`): the `-c` operand of `bash`, `sh`, `zsh`,
  `dash` or `ksh` (`shellConsumers`), in any option form the shell accepts (`-lc`, `-l -c`,
  `-o name -c`, and `+c`, `+xc`, which each of them reads as `-c`); the heredoc body or
  here-string such a shell reads as its script when it has no `-c` and no script operand,
  or has `-s` or `+s`; and the words of `eval`, joined by single spaces as bash joins them.
  Each shell's options are read by its own grammar (`shellGrammars`, `shellOptions`),
  measured on bash 3.2 and 5.3, zsh 5.9, dash and ksh 93u+: the options taking the next
  word are `-o`/`+o`, `-O`/`+O`, `--rcfile` and `--init-file` for bash and `sh`, `-o`/`+o`
  and `--emulate` for zsh, `-o`/`+o` for dash, and `-o`/`+o`, `-R` and `-T` for ksh. An
  option the grammar does not know (zsh's and ksh's `--NAME` option spellings among them)
  is read both as a flag and as taking the next word, and a payload either reading finds
  is read. `env -S` splits its string into words that stand in its place in the argv, each
  argument after it staying one word (`commandPosition`), so `env -S 'bash -c' 'rm x'`
  reads `rm x` as the `-c` payload. An `eval` payload runs in the same shell, so its `cd`,
  in both guards, moves the segments after it; the git guard reads it in its own walk
  (`evalPayloads`). A shell's payload runs in a child that starts in its parent segment's
  directory, and its `cd` does not leak out. A payload
  nested past `maxShellPayloadDepth`, or past `maxWriteReadings` payloads in one command,
  is not read: its segment is ambiguous, and the hook refuses on the write guard's own
  verdict, not on the git guard's.

Only a named consumer's payload is read. The git guard reads every word that reads as more
than itself (`evaluateGitMutationSafety`), each as a shell of its own on a copy of the
directory and the functions (`evaluateGitPayloadSafety`), so nothing it does returns; the
write guard deliberately does not. A refused
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
* **A brace group, a `cd` or a function call in a pipeline or in the background is
  over-refused**: `{ cd <shared>; } &` and `f &` with a moving `f` run in a child in bash,
  but the walk reads them as moving the commands after them, as zsh and ksh do for a
  pipeline's last member.
* **Aliases are open**: an alias is not expanded, so an alias that runs a write verb or a
  `cd` is read as the plain word it is.
* **`>` inside `[[ ]]` is read as a redirection**: `[[ a > b ]]` names `b` as a write target,
  an over-refusal when `b` is protected.
* **`$(...)` / backtick substitution is open** on this path: its extent is read, but a
  target it computes, and anything it runs, is not knowable here. A word that is only a
  substitution is not read as possibly absent, so in `$(true) rm <shared>/x`, where bash
  runs `rm`, the write guard reads `$(true)` as the command word and the write is open.
  (The git ladder still refuses substitution for git mutations.)
* **`>(...)` is read as a redirection**: `diff a >(b)` names the file `(b)` in the current
  directory as a write target, an over-refusal when that directory is protected. A
  `>(...)` body is not read for writes, like a `$(...)` body; a `<(...)` body is read as
  the subshell it runs in.

Tokenization cannot fail — `tokenizeSegments` always returns a segment list, with an
unfinished `case` or substitution as its own marked segment — so this channel has **no
unparseable state**. It does, however, carry explicit fail-closed states: for an unfinished
`case` or substitution, for an unknown

directory,
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
