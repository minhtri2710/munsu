---
name: munsu-update
description: Self-update munsu and fast-forward captain homes. Use when the user invokes /munsu-update (e.g. "update munsu", "pull the latest munsu", "update my captains"). Fast-forwards the munsu install root from origin (via `munsu update`), then lists each registered captain home and fast-forwards it (fast-forward only, never forced, never disruptive). Reloads AGENTS.md and nudges each updated captain to re-read its charter, keeping the whole fleet on the latest instructions.
user-invocable: true
metadata:
  internal: true
---

# munsu-update

Fast-forward the munsu install root and registered Captain homes without disrupting operational state.

## Procedure

1. Run `munsu update` for the install root.
2. Prefer `munsu update --captains` to update every registered Captain and deliver durable re-read nudges.
3. If manual control is required, run `munsu captain list`, then `munsu captain update <captain-home>` for each entry.
4. Nudge only Captains whose typed outcome is `fast-forwarded`.
5. Re-read the updated install root `AGENTS.md`.
6. Report updated, current, and skipped targets with their typed reasons.

## Safety boundary

- Fast-forward only: never force, stash, discard local work, or create merge commits.
- Never replace `munsu captain update` with raw Git commands.
- Touch only the munsu install root and registered Captain worktrees, never project worktrees.
- Leave dirty, diverged, offline, wrong-remote, wrong-branch, invalid-provenance, and state-only homes unchanged.
- Do not interrupt, teardown, or relaunch Captains during an update.

## Detail

Load [REFERENCE.md](REFERENCE.md) for command semantics, typed Captain outcomes, nudge behavior, reporting, and source references.
