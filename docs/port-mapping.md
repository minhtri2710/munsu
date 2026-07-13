# Port mapping: firstmate concepts → munsu commands / Go packages

This document maps firstmate scripts and concepts to their munsu equivalents.
Maintained as munsu ports more capabilities.

| firstmate concept / script | munsu command | munsu Go package | Status |
|---|---|---|---|
| `FM_HOME` / `~/.firstmate` | `munsu home` | `internal/home` | **A0: done** |
| `bin/fm-send.sh` | `munsu send` | `internal/cli` (stub) | stub |
| `bin/fm-spawn.sh` | `munsu spawn` | `internal/cli` (stub) | stub |
| `bin/fm-brief.sh` | `munsu brief` | `internal/cli` (stub) | stub |
| `bin/fm-teardown.sh` | `munsu teardown` | `internal/cli` (stub) | stub |
| `bin/fm-peek.sh` | `munsu peek` | `internal/cli` (stub) | stub |
| `bin/fm-crew-state.sh` | `munsu crew-state` | `internal/cli` (stub) | stub |
| `bin/fm-promote.sh` | `munsu promote` | `internal/cli` (stub) | stub |
| `bin/fm-harness.sh` | `munsu harness` | `internal/cli` (stub) | stub |
| `bin/fm-project-mode.sh` | `munsu project mode` | `internal/cli` (stub) | stub |
| `bin/fm-fleet-sync.sh` | `munsu fleet-sync` | `internal/cli` (stub) | stub |
| `bin/fm-bootstrap.sh` | `munsu bootstrap` | `internal/cli` (stub) | stub |
| `bin/fm-update.sh` | `munsu update` | `internal/cli` (stub) | stub |
| `bin/fm-session-start.sh` | `munsu session-start` | `internal/cli` (stub) | stub |
| `bin/fm-watch.sh` | `munsu watch` | `internal/cli` (stub) | stub |
| `bin/fm-watch-arm.sh` | `munsu watch-arm` | `internal/cli` (stub) | stub |
| `bin/fm-guard.sh` | `munsu guard` | `internal/cli` (stub) | stub |
| `bin/fm-stow.sh` | `munsu stow` | `internal/cli` (stub) | stub |
| `bin/fm-ensure-agents-md.sh` | `munsu ensure-agents-md` | `internal/cli` (stub) | stub |
| Project registry | `munsu project add/list/show/rm` | `internal/cli` (stub) | stub |
| Backlog (tasks-axi) | `munsu backlog` | `internal/cli` (stub) | stub |
| PR check/merge | `munsu pr-check` / `munsu pr-merge` | `internal/cli` (stub) | stub |
| Local merge | `munsu merge-local` | `internal/cli` (stub) | stub |
| Worktree pool (treehouse) | `munsu worktree` | `internal/cli` (stub) | stub |
| Config | `munsu config get/set` | `internal/cli` (stub) | stub |
| Session backend (tmux) | `--backend` flag | `internal/session` (future) | not yet |
| Harness adapters | `munsu harness detect/crew/secondmate` | `internal/harness` (future) | not yet |
| Dispatch profiles | `config/crew-dispatch.json` | `internal/config` (future) | not yet |
| Home init / init | `munsu init` | `internal/init` (future) | not yet |

## Structural differences from firstmate

| Aspect | firstmate | munsu |
|---|---|---|
| Entrypoint | Agent harness reads `AGENTS.md` in clone | `munsu` CLI binary on `PATH` |
| Home | Repo root (default) or `FM_HOME` | `~/.munsu` (default) or `MUNSU_HOME` / `--home` |
| Language | Bash (bin/fm-*.sh) | Go (github.com/spf13/cobra) |
| Dispatcher | None (harness calls bin/fm-*.sh directly) | Single `munsu` entrypoint dispatching subcommands |
| Runtime dep on firstmate | N/A (is firstmate) | No (standalone) |
| Orchestrator manual | Repo's AGENTS.md | Scaffolded by `munsu init` (Wave C2) |
