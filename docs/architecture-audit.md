# Architecture Audit & Remediation Plan

> **Status: All 5 remediation items shipped in PR #14 (commit `b67a160`).**
> This document is retained as an architectural reference; the plan has been executed.

This document synthesizes the architectural audit findings comparing the `munsu` Go implementation against the reference `firstmate` bash implementation. It highlights architectural deviations, boundary violations, missing modules, coupling issues, and missing firstmate patterns, and proposes a prioritized remediation plan.

## 1. Audit Findings

### Category 1: Boundary Violations
Several domain-level business logic functions and subprocess executions are defined directly within the CLI command layer in `internal/cli/root.go` instead of being encapsulated in a clean business logic domain layer.
- **Identified Violations**:
  - `runBacklog` (lines 945-952 in `internal/cli/root.go`)
  - `tasksAxiAvailable` (lines 955-970 in `internal/cli/root.go`)
  - `isCompatibleVersion` (lines 973-986 in `internal/cli/root.go`)
  - `parseVersion` (lines 989-996 in `internal/cli/root.go`)
  - `atoi` (lines 999-1008 in `internal/cli/root.go`)
  - `runTasksAxi` (lines 1012-1025 in `internal/cli/root.go`)
  - The inline implementation of `newSpawnCmd` (lines 609-682 in `internal/cli/root.go`) is heavily bloated with domain logic (worktree acquisition, harness detection, session creation, metadata writing).
- **Firstmate Counterpart**: `firstmate` separates these concerns by utilizing standalone library and script files like `bin/fm-tasks-axi-lib.sh` instead of embedding them directly within command-line option handlers.

### Category 2: Missing Modules
- ~~**Unimplemented Backlog Fallback**: `internal/cli/root.go` (line 951) fails with an error when `tasks-axi` is unavailable: `"backlog: tasks-axi not available and fallback backlog.md editing not yet implemented"`. In contrast, `firstmate` provides a fully defined manual fallback format for editing `$FM_HOME/data/backlog.md`.~~
  **RESOLVED**: `internal/backlog` now implements a full manual fallback parser/renderer with verbs: add, list, show, start, done, block, ready/unblock. Uses `$MUNSU_HOME/data/backlog.md` with markers `[ ]` queued, `[-]` in-flight, `[!]` blocked, `[x]` done. Auto-creates file with header on first add.
- **Stubbed Session Backends**: In `internal/session/session.go`, backends like `zellij`, `cmux`, `herdr`, and `orca` are mapped to stub implementations in `internal/session/backend_*.go` that simply return `ErrNotImplemented`.
- **Missing Hometag Isolation**: `munsu` lacks a home tag namespace isolation helper, which is defined in `firstmate` as `fm_backend_hometag()` in `bin/fm-backend-hometag-lib.sh`. This is crucial to prevent multiplexer namespace collisions.

### Category 3: Coupling Issues
- **Static / Global Home Resolution**: The functions `metaPath` (line 22) and `statusPath` (line 31) in `internal/task/task.go` call `home.Resolve("")` statically. Similarly, `PRCheck` in `internal/delivery/prcheck.go` (line 45) calls `home.Resolve("")` directly. This couples execution to global process state and ignores the custom `--home` CLI flag (which is parsed to `homeOverride` but never passed down).
- **Duplicated File Paths**: Because of this coupling, other packages (e.g., `Handoff` in `internal/secondmate/secondmate.go` lines 148-175) bypass the `task` package API entirely and reconstruct paths:
  `srcMeta := filepath.Join(parentHome, "state", key+".meta")`
- **Firstmate Counterpart**: In `firstmate`, the resolved home path (`FM_HOME`) is evaluated once at the script entrypoint and passed explicitly to all down-stream scripts and library functions.

### Category 4: Missing Firstmate Patterns
- **Safety Convergent Config Propagation**: The configuration propagation in `internal/secondmate/secondmate.go` (`ConfigPush` lines 178-200) simply copies files. It lacks:
  1. Gitignore Protection (`git check-ignore` checks) to verify that local configs are gitignored in the destination worktree.
  2. Primary-Authoritative Deletion (mirroring deletion by removing files downstream if they don't exist in the parent configuration).
  3. Overridable Inheritable List (customizing config propagation via environment variable `FM_INHERITABLE_CONFIG`).
  4. Config inheritance report logging (`FM_CONFIG_INHERIT_REPORT`).
- **Firstmate Counterpart**: These features are defined and enforced in firstmate's `bin/fm-config-inherit-lib.sh` (`propagate_inheritable_config`).

---

## 2. Prioritized Remediation Plan

Below is the prioritized plan to align `munsu` with `firstmate`'s architecture and fix identified defects.

### Item 1: Fix dynamic home resolution in task and delivery modules
- **What to change and why**: Parameterize all functions in `internal/task` (`WriteMeta`, `ReadMeta`, `AppendStatus`, `ReadStatus`, `PromoteMeta`) and `internal/delivery` (`PRCheck`, `PRMerge`, `ReviewDiff`, `MergeLocal`) to accept `homeDir string` instead of calling `home.Resolve("")`. Update all CLI wrappers to pass the dynamically resolved home path.
- **Firstmate script/pattern**: Direct alignment with `firstmate`'s explicit passing of `FM_HOME` to sub-scripts.
- **Refactor or New Capability**: Pure Refactor.

### Item 2: Extract backlog business logic to a dedicated `internal/backlog` package
- **What to change and why**: Extract version parsing, compatibility checks, and subprocess invocation from `internal/cli/root.go` into a dedicated package `internal/backlog`. Fix the version-parsing regex to support prefixed versions (e.g. `v1.0.0`) and avoid returning `0` due to `atoi` parsing limitations.
- **Firstmate script/pattern**: Aligns with `bin/fm-tasks-axi-lib.sh` separating logic from command routing.
- **Refactor or New Capability**: Pure Refactor & Bug Fix.

### Item 3: Implement true backlog item moving and gitignore protections in secondmate config push
- **What to change and why**: Update `secondmate.Handoff` to parse and move backlog items atomically, and update `secondmate.ConfigPush` to perform `git check-ignore` checks, mirror deletion of absent keys, and support overridable inheritable lists.
- **Firstmate script/pattern**: Aligns with `bin/fm-backlog-handoff.sh` and `bin/fm-config-inherit-lib.sh`.
- **Refactor or New Capability**: New Capability.

### Item 4: Implement standard backend configuration resolution and hometag namespace isolation
- **What to change and why**: Implement home tag isolation in `internal/hometag` to generate path hashes for session namespaces. Resolve target session backends by checking flag overrides > environment variables > config files > auto-detection.
- **Firstmate script/pattern**: Aligns with `bin/fm-backend-hometag-lib.sh` and `docs/configuration.md` backend resolution rules.
- **Refactor or New Capability**: New Capability.

### Item 5: Implement backlog manual fallback parser  **DONE**
- **What was changed**: Built a local fallback parser/renderer in `internal/backlog` supporting all verbs (add, list, show, start, done, block, ready, unblock). The `Run` function now takes `homeDir` and falls back to `$MUNSU_HOME/data/backlog.md` when `tasks-axi` is unavailable. Auto-creates file with header on first add. Unknown verbs return a clear error.
- **Firstmate script/pattern**: Aligns with firstmate's manual fallback backlog editing mode.
- **Refactor or New Capability**: New Capability.
