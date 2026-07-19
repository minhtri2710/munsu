---
name: munsu-update
description: Self-update munsu and fast-forward captain homes. Use when the user invokes /munsu-update (e.g. "update munsu", "pull the latest munsu", "update my captains"). Fast-forwards the munsu install root from origin (via `munsu update`), then lists each registered captain home and fast-forwards it (fast-forward only, never forced, never disruptive). Reloads AGENTS.md and nudges each updated captain to re-read its charter, keeping the whole fleet on the latest instructions.
user-invocable: true
metadata:
  internal: true
---

# munsu-update — self-update munsu and its captains

Munsu's own repo (the install root) and each captain home are fast-forwarded
from origin to pick up the latest `bin/`, `AGENTS.md`, `.agents/skills/`, and
other tracked material. Only `AGENTS.md`, `bin/`, and `.agents/skills/` are a
running soldier's instruction surface.

The update is **fast-forward only** — same sanctioned self-write as fleet sync.
It never forces, never creates a merge commit, never stashes, and advances a
target only on a clean fast-forward. Anything dirty, diverged, offline, or on
the wrong branch is skipped and reported.

A tracked-files fast-forward leaves the gitignored operational dirs
(`data/`, `state/`, `config/`, `projects/`), tmp/`) untouched, so in-flight
work is never disrupted. This touches only the munsu repo and its own
worktrees, never anything under `projects/`.

## What it does

### 1. Self-update munsu install root

```sh
munsu update
```

This runs the built-in `selfupdate.Update()` which:

1. Resolves the munsu binary's real path and walks up to find the git repo root.
2. Verifies it is a git repository (handling both bare `.git/` directories and
   `.git` worktree files).
3. Fetches origin.
4. Determines the current branch.
5. Runs `git merge --ff-only origin/<branch>` to fast-forward.
6. Prints the updated commit range, or exits with a failure reason if the tree
   is dirty, diverged, or on a detached HEAD.

**Prerequisite:** `git` must be on PATH. The install root must be a git clone
of the munsu repo.

### 2. Update each captain home and nudge

#### One-shot: `munsu update --captains`

After the self-update succeeds, `--captains` fast-forwards every registered
captain home to the parent default-branch commit and nudges each captain whose
instruction surface (`AGENTS.md`, `bin/`, `.agents/skills/`) advanced to re-read
its charter. This reuses the same locked convergence sweep as `munsu captain
converge`: same-remote check, default-branch fast-forward, fail-closed on
dirty/diverged/offline, and a durable nudge marker delivered via the captain's
session endpoint (`/re-read-agents`).

```sh
munsu update --captains
```

Per-captain errors (dirty, diverged, offline) are reported but never abort the
self-update or the remaining captains. A captain with no live endpoint keeps
its nudge marker; the next `munsu captain converge` retries delivery.

#### Manual procedure (fallback)

If `--captains` is not used, the operator must fast-forward each registered
captain home manually after the self-update.

#### List registered captains

```sh
munsu captain list
```

Each entry shows `<id> (<home-path>)`. The home path is a directory under the
parent munsu home's `captains/` directory.

#### Fast-forward a captain home

Each captain home is a treehouse worktree of the munsu repo at a detached
HEAD on the default branch. To fast-forward a specific captain home:

```sh
cd <captain-home>
git fetch origin
git merge --ff-only origin/main    # or the tracked default branch
```

Repeat for each captain home listed.

#### Skip conditions

Skip a captain home (report the skip reason) if:

- **Offline:** The worktree path does not exist or is not reachable.
- **Dirty:** `git status --porcelain` produces output (uncommitted changes).
- **Diverged:** `git merge --ff-only` fails because the local branch has
  diverged from the remote.
- **Detached HEAD without explicit check:** Verify the general is on the
  correct detached HEAD of the default branch. If on a different commit,
  `git fetch origin && git switch --detach origin/main`.

#### Nudge updated captains

For each captain that was successfully advanced, send a one-line steer to
tell it to re-read its AGENTS.md:

```sh
munsu send <captain-id> "munsu was updated — please re-read your AGENTS.md to pick up the new instructions."
```

Skip the nudge for captains that were skipped, already current, or have no
live process.

### 3. Re-read AGENTS.md

After the install root is updated, re-read `AGENTS.md` to refresh your own
operating instructions before doing anything else. This ensures you act on the
latest instructions rather than stale ones.

```sh
# Read the updated AGENTS.md in the munsu install root
# (your process was launched with the previous version)
```

### 4. Report to the general

Summarize what landed in plain outcomes: which parts of the fleet are now on
the latest, and which were left as-is and why.

Surface any skipped target whose reason needs the general's attention — for
instance a captain with un-landed changes (diverged) or local edits (dirty),
which were left untouched on purpose.

## Safety

- **Fast-forward only.** A target that has diverged, is dirty, is offline, or is
  on a non-default branch is skipped and reported, never forced or stashed.
  Nothing with un-landed work is ever discarded.
- **Only the munsu repo and its worktrees** are touched, never `projects/`.
  This is the same sanctioned self-write as fleet sync.
- **Captains are never disrupted.** A captain gets a tracked-files
  fast-forward (safe while it is mid-task, since its work lives in gitignored
  operational dirs and separate project worktrees) plus a gentle re-read nudge.
  It is never torn down, interrupted, or forced.

## See also

- `.agents/skills/munsu-ops/SKILL.md` — fleet orchestration operator skill
  (spawn, supervise, teardown, full lifecycle).
- `.agents/skills/updatefirstmate/SKILL.md` — analogous firstmate self-update
  skill (reference pattern for update mechanics and safety rules).
- `internal/selfupdate/update.go` — authoritative Go implementation of the
  `munsu update` command.
- `internal/captain/captain.go` — captain lifecycle management
  (list homes, launch, retire).
- `internal/worktree/worktree.go` — treehouse worktree management helpers.
