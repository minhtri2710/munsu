# munsu

Standalone CLI port of firstmate crew capabilities, usable from any project directory.

## What it is

munsu is an installable CLI that gives any coding-agent harness the firstmate crew
capability — spawning autonomous agents in visible session backends, supervising
them with an event-driven zero-token watcher, and delivering finished PRs or
investigation reports — **without requiring a firstmate checkout**.

Think of it as the firstmate toolbelt + state model, ported into a compiled Go CLI
with a relocated home (`~/.munsu`) and usable from any git repo directory.

## Install

```sh
git clone https://github.com/minhtri2710/munsu
cd munsu
./install.sh
```

This builds the binary and symlinks `munsu` into `~/.local/bin`.

## Quick start

```sh
munsu home --mkdir       # create ~/.munsu/{state,data,config,projects}
munsu --help             # see the full command tree
munsu --version          # print version
```

## Documentation

- `docs/port-mapping.md` — mapping between firstmate concepts and munsu commands.
- `CONTRIBUTING.md` — how to contribute.
- `AGENTS.md` — conventions file for crewmates working on munsu.

## License

MIT — see LICENSE. NOTICE acknowledges firstmate as the behavioral origin of munsu's capabilities.
