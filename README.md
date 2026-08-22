# munsu

<p align="center">
  <img src="docs/images/munsu-mascot.png" alt="Munsu — Shin Angyo Onshi / Ám Hành Ngự Sử" width="600">
</p>

Standalone CLI port of firstmate soldier capabilities, usable from any project directory.
provides the capability — spawning autonomous agents in visible session backends, supervising
them with an event-driven zero-token watcher, and delivering finished PRs or
investigation reports — without requiring a specific project checkout.

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

### Option 3: Download a release binary

Pre-built binaries are published on the [GitHub releases page](https://github.com/minhtri2710/munsu/releases).
Download the archive for your platform, extract it, and place the `munsu` binary on your `$PATH`.

### PATH setup

After installation, ensure the binary directory is on your `$PATH`:

- **go install path:** add `export PATH="$HOME/go/bin:$PATH"` to your shell rc.
- **make install path:** `${XDG_BIN_HOME:-$HOME/.local/bin}` is commonly on `PATH`; otherwise add
  `export PATH="${XDG_BIN_HOME:-$HOME/.local/bin}:$PATH"` to your shell rc.

### Home directory creation

```sh
munsu home --mkdir       # create ~/.munsu/{state,data,config,projects}
```

### no-mistakes init (for delivery pipelines)

> **Note:** `no-mistakes` is an external delivery pipeline tool, not owned by munsu. The commands below work only when the
> no-mistakes daemon is installed and configured on the host.

```sh
cd projects/<name>
no-mistakes init
no-mistakes doctor
```

This registers the project for automated code review, linting, testing, and PR delivery.
For standalone munsu use, skip this step — `munsu spawn --mode direct-PR` or `--mode local-only` works without the no-mistakes daemon.

## Quick start

```sh
munsu home --mkdir       # create ~/.munsu/{state,data,config,projects}
munsu init               # auto-detect backend, harness, and seed AGENTS.md
munsu doctor             # run diagnostics with fix commands
munsu --help             # see the full command tree
munsu --version          # print version
```

## How agents load .agents/skills

munsu ships with a `.agents/skills/` directory at the project root. Each skill is a
`SKILL.md` file with YAML frontmatter (`name:`, `description:`) that modern coding
agent harnesses auto-discover at session start.

| Harness | Discovery mechanism |
|---------|-------------------|
| **Pi** | Auto-discovers `.agents/skills/` under the project root once the project is trusted. Skills are indexed by their frontmatter `name:` and available by name. |
| **Claude Code** | Loads `.agents/skills/` (or `.claude/skills/` symlink) as skill definitions. Each `SKILL.md` with proper frontmatter is discoverable via `/skill-name`. |
| **Codex** | Reads the same `.agents/skills/*/SKILL.md` conventions for skill discovery via `$skill-name`. |
| **agy / Antigravity** | Discovers `.agents/skills/` alongside other agent skill standards. Launch via `munsu spawn --harness agy`. |
| **Other agents** | The `.agents/skills/*/SKILL.md` pattern is a cross-harness standard for agent-facing skill files. Most agent CLIs detect and index the directory automatically. |

## Available skills

| Skill | Description | Trigger |
|-------|-------------|---------|
| **munsu-ops** | Fleet orchestration — init home, session-start, spawn/supervise soldiers, task lifecycle, captains, watcher, delivery helpers. | Running munsu, spawning soldiers, claiming wakes, managing tasks. |
| **captain-provisioning** | Full captain lifecycle — seed, launch, retire, handoff, config-push — following the idle-by-default charter contract. | Provisioning, inspecting, or retiring a persistent domain supervisor (captain). |
| **munsu-update** | Self-update munsu and fast-forward captain homes. | Invoking `/munsu-update`, "update munsu", "pull the latest munsu". |
| **bootstrap-diagnostics** | Handle session-start bootstrap diagnostics — toolchain readiness lines. | Session-start diagnostic output (MISSING, NEEDS_GH_AUTH, TANGLE, etc.). |
| **harness-adapters** | Verified adapter launch templates for spawning soldiers — model flags, effort flags, harness detection, and turn-end hooks. | Spawning soldiers with harness-specific flags. |
| **stuck-soldier-recovery** | Escalation ladder for unresponsive or stuck soldiers — peek, steer, interrupt, relaunch, fail. | Unresponsive or stuck soldier. |

## Usage examples

### Bootstrap

```sh
munsu home --mkdir
munsu init
munsu doctor
```

### Session start

```sh
munsu session-start
```

### Task management

```sh
munsu task add <task-id> "<description>" --kind ship --repo <name>
munsu task start <task-id>
munsu task done <task-id>
```

### Soldier lifecycle

```sh
munsu spawn <task-id> [<project>] [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only]  (default: auto-detect, project inferred from cwd)
munsu watch-arm [--restart]
munsu send <task-id> "<instruction>"
munsu peek <task-id> [--lines N]
munsu teardown <task-id>
```

### Fleet management

```sh
munsu fleet view
munsu guard
munsu captain list
```

## Documentation

- `docs/architecture.md` — architecture overview, module layout, key interfaces, design decisions.
- `docs/port-mapping.md` — full command reference grouped by domain.
- `CONTRIBUTING.md` — how to contribute.
- `AGENTS.md` — conventions file for soldiers working on munsu.
- `COMMANDS.md` — curated command map grouped by lifecycle phase; use `munsu --help` and per-command `--help` for the complete registered set.
- `SUPERVISION.md` — watch/guard/afk loop details.

## License

MIT — see LICENSE and NOTICE for attribution.
