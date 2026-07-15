# munsu — Remaining Architecture Remediation Tasks

> **ARCHIVED — all remediation items shipped in PR #14. See commit history for details.**
> This file is retained for reference only; no further action needed.
> Verified still accurate at tip `0b88e6f` (2026-07-15).

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
- Item 5 config gate: `Run()` checks `config/backlog-backend` for `"manual"` before consulting tasks-axi. When the file contains `"manual"` (trimmed), the manual markdown parser runs even if a compatible tasks-axi is on PATH.
