# munsu — Remaining Architecture Remediation Tasks

> Items 1–5 shipped in **PR #14** (commit `b67a160` on main).
> This file is retained for reference only; no further action needed.

---

All five architecture remediation items were implemented and merged:

| Item | Area | Status | Commit |
|---|---|---|---|
| 1 | homeDir parameteriation | **SHIPPED** | PR #14 / `b67a160` |
| 2 | `internal/backlog` extraction + audit docs | **SHIPPED** | PR #14 / `b67a160` |
| 3 | secondmate atomic Handoff + safe ConfigPush | **SHIPPED** | PR #14 / `b67a160` |
| 4 | `internal/hometag` + `session.Resolve` + stub errors | **SHIPPED** | PR #14 / `b67a160` |
| 5 | backlog manual markdown fallback | **SHIPPED** | PR #14 / `b67a160` |

## Residual notes

- `internal/backlog/backlog.go` exists but the CLI `munsu backlog` verb still dispatches through the original `runBacklog` in `root.go`. If `internal/backlog.ManualRun` is preferred as the single entry point, future cleanup could consolidate.
- ConfigPush gitignore check uses `git check-ignore -q` on the secondmate home; if the secondmate home is not a git repo, this step silently succeeds with a warning printed to stdout.
