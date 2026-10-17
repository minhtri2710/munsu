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

## Quick start

```sh
munsu init               # create ~/.munsu, detect the soldier harness, seed config/base.json and AGENTS.md
munsu config set backend herdr   # or tmux; spawning requires a session backend
munsu doctor             # run diagnostics with fix commands
munsu --help             # see the full command tree
munsu --version          # print version
```

## Skills

Skills are embedded in the binary. `munsu skill list` shows them and
`munsu skill show <name>` prints one; `munsu init --skill` installs `munsu-ops`.
Six of them are symlinked from `.agents/skills/` into `internal/cli/skills/`, so
agent harnesses that auto-discover that directory (Pi, Claude Code, Codex, agy)
load the embedded source by frontmatter `name:`.

## Usage

`munsu skill show munsu-ops` prints the operator workflow and its command map
(`internal/cli/skills/munsu-ops/COMMANDS.md`); `munsu <command> --help` is authoritative.

## Documentation

- `CONTRIBUTING.md` — how to contribute.
- `AGENTS.md` — conventions file for soldiers working on munsu, including the package map.
- `internal/cli/skills/munsu-ops/` — canonical operator skill (`munsu skill show munsu-ops`): `COMMANDS.md` command map and `SUPERVISION.md` watch/wake/guard/AFK loop.

## License

MIT — see LICENSE and NOTICE for attribution.
