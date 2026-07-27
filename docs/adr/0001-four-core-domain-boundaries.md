# 0001. Restructure 44 Packages into 4 Core Domain Boundaries and Apply Clean Break Branch Prefix (`mu/`)

* **Status:** Superseded in detail by ADR-0002; four-core/five-infra direction reaffirmed
* **Date:** 2026-07-26
* **Deciders:** munsu core architecture team

## Context & Problem Statement

`munsu` was ported from Firstmate as a compiled Go CLI engine. Over time, `internal/` fragmented into 44 small sub-packages (e.g., `nostatus`, `herdrprune`, `hometag`, `marker`, `contract`, `composer`, `scope`, `uplink`). This over-fragmentation created confusion around module boundaries, increased maintenance overhead, and resulted in an over-engineered test suite of 69,671 lines across 143 test files with a test execution time of 45-50 seconds.

Additionally, branch naming retained the legacy Firstmate prefix (`fm/`), and supervision retained deprecated commands like `wake-drain`.

## Decision Drivers

* **Maintainability & Clarity:** Consolidate fragmented packages into clear domain boundaries with deep modules and shallow interfaces.
* **Test Speed & Reliability:** Reduce test execution time from ~45 seconds to <5 seconds by unifying setup boilerplate in `internal/testutil` and using in-memory mocks instead of spawning external shell processes.
* **Clean Break Policy:** Completely drop legacy Firstmate artifacts (`fm/` branch prefix, `wake-drain` command, `CREW/MARSHAL/SECOND` terminology) without backward compatibility code paths.
* **Resilience & Safety:** Integrate native fallbacks (e.g., `git worktree add` when `treehouse` is missing, `tmux` fallback when alternative backends fail), OS-level `flock` file locking, and RBAC for agent skill invocation.

## Decided Options

### 1. Four Core Domain Boundaries + Shared Infra Leaf Packages

Consolidate 35+ fragmented sub-packages into 4 core domain boundaries, while maintaining 5 low-level infrastructure leaf packages:

1. `internal/domain`: Core domain models, state representation (`task`, `soldierstate`), delivery rules (`PR.CanMerge`), event stream folding (`classify`), and contract schemas.
2. `internal/backend`: Terminal session adapters (`tmux`, `herdr`, `orca`), session resolution, and worktree pool management.
3. `internal/orchestrator`: Supervision loops (`watch`), durable waker, wake delivery, AFK daemon, and captain mailbox.
4. `internal/fleet`: Captain lifecycle, fleet sync/view, project registry, and harness hooks.
5. **Shared Infra Leaf Packages:** `internal/config`, `internal/home`, `internal/harness`, `internal/bootstrap`, `internal/testutil`.

### 2. Clean Break Strategy for Legacy Artifacts

* Standardize default branch prefix to `mu/<task-id>`. Remove all code handling `fm/` legacy branches.
* Remove `munsu wake-drain` command; use `munsu wake claim` with lease locking exclusively.
* Remove legacy parity tests (`captain_parity_test.go`).

### 3. Integrated Resilience & Hardening

* **Worktree Fallback:** Automatically fallback to `git worktree add` if `treehouse` CLI is unavailable.
* **Session Backend Fallback:** Fallback to `tmux` with a warning if custom backends (`herdr`/`orca`/`zellij`) fail to resolve.
* **AFK Supervision:** `munsu afk` auto-ensures watcher (`watch ensure`) on startup.
* **RBAC for Skill CLI:** `munsu skill show` rejects management skills when `MUNSU_ROLE=soldier`.

## Consequences

### Positive

* Reduces sub-package count from 44 to ~9-10 (78% reduction).
* Eliminates circular dependency risks (`import cycle not allowed`).
* Reduces test suite line count from 70k to ~25k lines, accelerating `go test ./...` from 45s to <5s.
* Zero technical debt from legacy Firstmate backwards compatibility logic.

### Negative / Trade-offs

* Major structural migration requiring systematic updating of import paths across `internal/cli/` and test files.
