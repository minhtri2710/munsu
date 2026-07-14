# Project: munsu

## Architecture
- CLI Entrypoint: `cmd/munsu/main.go`
- CLI Cobra command tree: `internal/cli/root.go`
- Home Resolution: `internal/home`
- Harness: `internal/harness`
- Worktree: `internal/worktree`
- Session: `internal/session`
- Task: `internal/task`
- Brief: `internal/brief`
- Teardown: `internal/teardown`
- Crewstate: `internal/crewstate`
- Delivery: `internal/delivery`

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|---|---|---|---|
| 1 | Architecture Audit & Plan | Analyze munsu vs firstmate and write docs/architecture-audit.md | none | DONE |
| 2 | Top-Priority Refactor Fix | Implement the selected top-priority fix | 1 | DONE |
| 3 | E2E Testing Verification | Pass all existing tests, run reviewers, challengers, and auditor | 2 | DONE |

## Interface Contracts
### CLI ↔ Home/Harness/Worktree
- Cobra command definitions wire CLI inputs to package entrypoints.

## Code Layout
- `cmd/munsu/main.go` — entrypoint
- `internal/cli/` — Cobra command wiring and root command
- `internal/home/` — Home resolution and directory creation
- `internal/harness/` — Harness detection and secondmate resolution
- `internal/worktree/` — Treehouse wrapper and isolation assertion
