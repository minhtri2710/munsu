# munsu-update reference

## Update the install root

Run:

```sh
munsu update
```

The command resolves the installed munsu repository, verifies it is a clean Git checkout, fetches origin, and fast-forwards the current branch with `git merge --ff-only`. It never forces, stashes, creates a merge commit, or advances a dirty, diverged, offline, or detached checkout.

The tracked-files fast-forward leaves gitignored operational directories (`data/`, `state/`, `config/`, `projects/`, `tmp/`) untouched.

## Update Captain homes

Preferred one-shot route:

```sh
munsu update --captains
```

This reuses the locked Captain convergence sweep. It checks provenance, remote identity, the default branch, and fast-forwardability before updating each registered Captain. A Captain whose instruction surface advances receives a durable re-read nudge; an offline Captain retains the nudge marker for the next `munsu captain converge`.

Manual fallback:

```sh
munsu captain list
munsu captain update <captain-home>
```

`munsu captain update` is the only manual update route. Do not replace it with raw `git fetch` or `git merge`: the command applies the same fail-closed provenance, remote, branch, cleanliness, and fast-forward checks as the one-shot sweep.

Typed outcomes:

- `already-current` — no update and no nudge.
- `fast-forwarded` — advanced; nudge this Captain.
- `state-only-skipped` — no Git worktree.
- `dirty` / `diverged` — left untouched; report to the General.
- `offline` / `wrong-remote` / `wrong-branch` / `invalid-provenance` — skipped; report the reason.

Only `fast-forwarded` authorizes a re-read nudge:

```sh
munsu send <captain-id> "munsu was updated — please re-read your AGENTS.md to pick up the new instructions."
```

## Refresh instructions

After the install root updates, re-read its `AGENTS.md` before further work. Running agents were launched with the previous instruction surface.

## Report outcomes

Report which targets were fast-forwarded, already current, or skipped. Surface dirty, diverged, offline, wrong-remote, wrong-branch, and invalid-provenance outcomes without attempting a force update.

## Safety boundary

- Fast-forward only; never force, stash, or discard local work.
- Touch only the munsu install root and registered Captain worktrees, never project worktrees under `projects/`.
- Do not interrupt, teardown, or relaunch Captains during an update.
- A skipped target remains unchanged.

## Source references

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill.
- `internal/cli/selfupdate_update.go` — `munsu update` implementation.
- `internal/fleet/captain_captain.go` — Captain list/update lifecycle.
- `internal/backend/worktree.go` — worktree mechanics.
