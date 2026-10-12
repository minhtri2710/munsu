# munsu

<p align="center">
  <img src="docs/images/munsu-mascot.png" alt="Munsu — Shin Angyo Onshi / Ám Hành Ngự Sử" width="600">
</p>

Standalone CLI for soldier orchestration, usable from any project directory.
It spawns autonomous agents in visible session backends, supervises them with an
event-driven zero-token watcher, and delivers finished PRs or investigation reports,
without requiring a specific project checkout.

## What it is

A compiled Go CLI with a relocated home (`~/.munsu`) and usable from any git repo directory.

## Install

### Option 1: go install (Go users)

```sh
go install github.com/minhtri2710/munsu/cmd/munsu@latest
```

Ensure `~/go/bin` is on your `$PATH`.

### Option 2: git clone + make install

```sh
git clone https://github.com/minhtri2710/munsu
cd munsu
make install
```

This installs `munsu` into `${XDG_BIN_HOME:-$HOME/.local/bin}`. Override the destination with `make install BINDIR=/custom/bin`.

### PATH setup

After installation, ensure the binary directory is on your `$PATH`:

- **go install path:** add `export PATH="$HOME/go/bin:$PATH"` to your shell rc.
- **make install path:** `${XDG_BIN_HOME:-$HOME/.local/bin}` is commonly on `PATH`; otherwise add
  `export PATH="${XDG_BIN_HOME:-$HOME/.local/bin}:$PATH"` to your shell rc.

### no-mistakes init (for delivery pipelines)

> **Note:** `no-mistakes` is an external delivery pipeline tool, not owned by munsu. The commands below work only when the
> no-mistakes daemon is installed and configured on the host.

```sh
cd projects/<name>
no-mistakes init
no-mistakes doctor
```

This registers the project for automated code review, linting, testing, and PR delivery.
For standalone munsu use, skip this step — without a `review` tool entry the review step is baseline and `munsu spawn` works without the no-mistakes daemon.

## Quick start

```sh
munsu home --mkdir       # create ~/.munsu/{state,data,config,projects}
munsu init               # detect the soldier harness, seed config/base.json and AGENTS.md
munsu doctor             # run diagnostics with fix commands
munsu --help             # see the full command tree
munsu --version          # print version
```

## Skills

Skills are embedded in the binary. `munsu skill list` shows them and
`munsu skill show <name>` prints one; `munsu init --skill` installs `munsu-ops`.
Six of them are mirrored as `SKILL.md` files under `.agents/skills/`, which
agent harnesses that auto-discover that directory (Pi, Claude Code, Codex, agy)
load by frontmatter `name:`.

## Usage examples

### Session start

```sh
munsu session-start
```

### Task management

```sh
munsu task add <task-id> "<description>" --kind ship --repo <name>
munsu task add <task-id> "<description>" --kind review --reviews <ship-id> [--repo <name>]
munsu task start <task-id>
munsu task done <task-id>
```

### Soldier lifecycle

```sh
munsu spawn <task-id> [<project>]  (project inferred from cwd; delivery mode derived from the project's review and forge tool entries)
munsu watch ensure [--restart]
munsu send <task-id> "<instruction>"
munsu peek <task-id> [--lines N]
munsu teardown <task-id>
```

### Fleet management

```sh
munsu dashboard       # interactive fleet view (needs a terminal)
munsu fleet view      # non-interactive fleet view
munsu guard
munsu captain list
```

## Documentation

- `CONTRIBUTING.md` — how to contribute.
- `AGENTS.md` — conventions file for soldiers working on munsu, including the package map.
- `internal/cli/skills/munsu-ops/` — canonical operator skill (`munsu skill show munsu-ops`): `COMMANDS.md` command map and `SUPERVISION.md` watch/wake/guard/AFK loop.

## License

MIT — see LICENSE and NOTICE for attribution.
