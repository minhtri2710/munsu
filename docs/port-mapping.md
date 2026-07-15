# Port mapping: firstmate concepts → munsu commands / Go packages

This document maps firstmate scripts and concepts to their munsu equivalents.
All commands are fully implemented unless marked otherwise.

| firstmate concept / script | munsu command | munsu Go package | Status |
|---|---|---|---|
| `FM_HOME` / `~/.firstmate` | `munsu home` | `internal/home` | **implemented** |
| Task meta + status protocol | `munsu task add/show/status` | `internal/task` | **implemented** (list: delegated to tasks-axi) |
| `bin/fm-send.sh` | `munsu send` | `internal/cli` | **implemented** |
| `bin/fm-spawn.sh` | `munsu spawn` | `internal/cli` | **implemented** |
| `bin/fm-brief.sh` | `munsu brief` | `internal/brief` | **implemented** |
| `bin/fm-teardown.sh` | `munsu teardown` | `internal/teardown` | **implemented** |
| `bin/fm-peek.sh` | `munsu peek` | `internal/cli` | **implemented** |
| `bin/fm-crew-state.sh` | `munsu crew-state` | `internal/crewstate` | **implemented** |
| `bin/fm-promote.sh` | `munsu promote` | `internal/task` | **implemented** |
| `bin/fm-harness.sh` | `munsu harness detect/crew/secondmate` | `internal/harness` | **implemented** |
| `bin/fm-project-mode.sh` | `munsu project mode` | `internal/project` | **implemented** |
| `bin/fm-fleet-sync.sh` | `munsu fleet-sync` | `internal/fleet` | **implemented** |
| `bin/fm-fleet-snapshot.sh` | `munsu fleet-snapshot` | `internal/fleet` | **implemented** |
| `bin/fm-fleet-view.sh` | `munsu fleet-view` | `internal/fleet` | **implemented** |
| `bin/fm-bearings-snapshot.sh` | `munsu bearings` | `internal/fleet` | **implemented** |
| `bin/fm-bootstrap.sh` | `munsu bootstrap` | `internal/bootstrap` | **implemented** |
| `bin/fm-update.sh` | `munsu update` | `internal/selfupdate` | **implemented** |
| `bin/fm-session-start.sh` | `munsu session-start` | `internal/session` | **implemented** |
| `bin/fm-watch.sh` | `munsu watch` | `internal/supervision` | **implemented** |
| `bin/fm-watch-arm.sh` | `munsu watch-arm` | `internal/cli` | **implemented** |
| `bin/fm-wake-drain.sh` | `munsu wake-drain` | `internal/waker` | **implemented** |
| `bin/fm-guard.sh` | `munsu guard` | `internal/cli` | **implemented** |
| Stow skill (`.agents/skills/stow`) | `munsu stow` | `internal/stow` | **implemented** |
| `bin/fm-ensure-agents-md.sh` | `munsu ensure-agents-md` | `internal/agentsmd` | **implemented** |
| Project registry | `munsu project add/list/show/rm` | `internal/project` | **implemented** |
| Backlog (tasks-axi + manual fallback) | `munsu backlog` | `internal/backlog` | **implemented** |
| `bin/fm-review-diff.sh` | `munsu review-diff` | `internal/delivery` | **implemented** |
| PR check/merge | `munsu pr-check` / `munsu pr-merge` | `internal/delivery` | **implemented** |
| Local merge | `munsu merge-local` | `internal/delivery` | **implemented** |
| Worktree pool (treehouse) | `munsu worktree get/return/status` | `internal/worktree` | **implemented** |
| Config | `munsu config get/set` | `internal/config` | **implemented** |
| Session backend (tmux + herdr) | `--backend` flag | `internal/session` | **implemented** (future backends: experimental — see docs) |
| Dispatch profiles | `config/crew-dispatch.json` | `internal/harness` | **implemented** |
| Home init / init | `munsu init` | `internal/cli` | **implemented** |
| AFK away-mode supervision | `munsu afk` | `internal/afk` | **implemented** |
| Self-update | `munsu update` | `internal/selfupdate` | **implemented** |
| Secondmate lifecycle | `munsu secondmate seed/launch/retire/list/handoff/config-push` | `internal/secondmate` | **implemented** |
## Structural differences from firstmate

| Aspect | firstmate | munsu |
|---|---|---|
| Entrypoint | Agent harness reads `AGENTS.md` in clone | `munsu` CLI binary on `PATH` |
| Home | Repo root (default) or `FM_HOME` | `~/.munsu` (default) or `MUNSU_HOME` / `--home` |
| Language | Bash (bin/fm-*.sh) | Go (github.com/spf13/cobra) |
| Dispatcher | None (harness calls bin/fm-*.sh directly) | Single `munsu` entrypoint dispatching subcommands |
| Runtime dep on firstmate | N/A (is firstmate) | No (standalone) |
| Orchestrator manual | Repo's AGENTS.md | Scaffolded by `munsu init` (Wave C2) |
