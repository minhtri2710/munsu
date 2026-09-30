# 0024. A New Refusal Needs the Owner's Words; the Shell Write-Target Lexer's Grammar Is Frozen

* **Status:** Accepted
* **Date:** 2026-09-30
* **Amends:** ADR-0014 (the shell channel is decided on its write targets: the lexer's grammar stops growing and its role becomes secondary), ADR-0005 §6 (the git shim stays as a secondary layer)
* **Extends:** ADR-0025 (the Words record)
* **Triggered by:** Human decision G297 item 2 (the OS write fence is the primary write boundary; F3: no Windows fence now, the lexer stays the Windows guard) and the S14/B8 and S17 findings of the munsu-roadmap delivery

## Context

Two patterns kept recurring in the guard work that ADR-0014 started.

First, refusals were added on the strength of a reviewer's or an agent's judgment
alone. A guard, check or gate that refuses an action munsu previously allowed changes
what every operator can do, and nothing recorded that the owner wanted it. ADR-0017
records the cost side: each refusal branch needs a test and a waiver argument. It does not
say who may decide the refusal should exist.

Second, the shell write-target lexer (`shellWriteTargets`, `internal/cli/shell_write_safety.go`)
grew one shell construct at a time: quoting, `cd`, function chains, Windows volumes.
ADR-0014 §2 already states that its claim of coverage is narrow by construction and
that everything outside it is open. Each new construct modeled moves the boundary a little
and leaves the class open: brace expansion, pathname expansion, eval, indirection and
interpreters remain outside any lexical model of a shell. A lexer cannot be the write
boundary of a process that can run arbitrary programs.

The write boundary that can hold is the operating system's. An OS write fence confines
what a launched process can write regardless of how it spells the command. Two are being
built: F1, the soldier fence, confines a soldier to its bound worktree, and F2, the
reviewer-seat fence, makes a reviewer seat unable to write the checkout it reviews (the
launch scope ADR-0025 §3 left separate). Both are macOS only in this delivery (Human decision
G356, "A: Chỉ macOS (Lead recommends)"). On Linux, spawn records "no fence" with a reason, the
same stance as Windows. F3 is decided for now as "no Windows fence now": there is no
Windows fence today, and building one would need new Human words. A Linux fence is a later slice.

## Decision

### 1. A new refusal surface needs the owner's words before it ships

A refusal surface is a guard, check or gate that refuses an action munsu previously
allowed. Before one ships, the owner's words for it are recorded: the Human's, with the
grantor, the channel and the verbatim quote, in the shape of the ADR-0025 Words record
(`domain.Words`). The record goes in the change that ships the refusal, so a reader can
find who wanted it and where they said so.

This applies to a new surface, and widening a refusal is a new surface: it refuses
actions that were allowed, so it needs words. The one exemption is fixing a refusal that
misfires on an action it was never meant to refuse. Removing a refusal that the owner
asked for needs words too.

The cost is documentation only: no code, schema or command changes. The gate is the Lead,
who checks for the record before accepting the change. As with ADR-0025 §5, the record is
a claim; it shows what was asserted, not that the assertion was verified.

### 2. The shell write-target lexer's grammar is frozen

The lexer's grammar, as ADR-0014 and the code in `internal/cli/shell_write_safety.go`
define it at the date of this ADR, is frozen. No new shell-grammar modeling is added:
no new expansion, quoting, control-flow, function or interpreter form. Fixing a defect in
a construct the lexer already models is not growth, and stays allowed.

Reopening grammar growth needs evidence of a live miss class that only the lexer could
close, meaning a write path the OS fence cannot cover, and new Human words. It is never
silent growth by a fix in another scope.

### 3. The OS write fence is the primary write boundary; the lexer and the git shim are secondary

The OS write fence (F1, F2) is the primary write boundary. The lexer and the git shim
(ADR-0005 §6) stay as secondary layers behind it. Neither is a security sandbox, and
ADR-0005 §6's residuals stand as stated there.

The lexer and the git shim stay as the guard on Linux and Windows, where there is no fence
now: Linux because F1 and F2 are macOS only (G356), Windows because F3 is decided as "no
Windows fence now". On those platforms they are the only shell-channel protection a session
has. A Windows fence would need new Human words; a Linux fence is a later slice.

### 4. Deleting the lexer is a capability deletion

Deleting the lexer removes a refusal that operators have today. It needs the Human's words
(Decision 1 in reverse), and it comes only after both of these hold:

* F1 and F2 are proven on macOS, and
* every other platform the lexer guards (today Linux and Windows) has a proven fence or
  replacement guard, or the Human accepts that platform without the lexer.

Neither ADR-0014 nor ADR-0005 is edited by this ADR. They keep their text; this ADR bounds
them.

## Consequences

* A reviewer or soldier who wants a new refusal must obtain and record the Human's words
  first, or present the refusal to the Lead as unshipped.
* Shell-grammar work stops at the current boundary. A reported bypass is answered by the
  OS fence, not by another lexer rule; if the miss is one only the lexer could close, it
  needs new Human words.
* The lexer, its tests and the git shim stay in the tree until the ADR-0024 §4
  conditions hold. Nothing is deleted now.
* Until F1 and F2 land, the lexer remains the only shell-channel write protection on every
  platform, and on Linux and Windows it stays so after they land. This ADR does not weaken it.
