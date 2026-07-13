# Contributing

Thanks for wanting to contribute.

**Pull requests targeting `main` must be raised through [`no-mistakes`](https://github.com/kunchenguid/no-mistakes).**
We require this to reduce the maintainer's burden of reviewing and merging contributions.

`no-mistakes` puts a local git proxy in front of your real remote.
Pushing through it runs an AI-driven review/test/lint pipeline in an isolated worktree, forwards the push upstream only after every check passes, and opens a clean PR automatically.

## Workflow

1. Fork the repo, then clone the parent repo or set your local `origin` back to the parent.
2. Create a branch and make your changes.
3. Initialize the gate with your fork as the push target: `no-mistakes init --fork-url git@github.com:<you>/munsu.git` (or plain `no-mistakes init` for maintainers with push access).
4. Commit your changes.
5. Push through the gate instead of pushing to `origin`:
   ```sh
   git push no-mistakes
   ```
6. Run `no-mistakes` to attach to the pipeline, watch findings, authorize auto-fixes, and review ask-user findings as needed.
7. Once the pipeline passes, it opens the PR for you.

## Repo conventions

- **Language:** Go. Build with `go build ./...`, lint with `go vet ./...`, test with `go test ./...`.
- **CLI framework:** cobra (github.com/spf13/cobra).
- **Module path:** github.com/minhtri2710/munsu.
- **AGENTS.md** is the project conventions file for crewmates working on munsu. `CLAUDE.md` is a symlink to it.
- Everything personal (`.no-mistakes/`, `state/`, `data/`, `config/`, `projects/`) is gitignored; never commit it.
- Keep `README.md` a concise overview plus pointers; route detail to `docs/`.

## Development

```sh
go build ./...      # build all packages
go vet ./...        # static analysis
go test ./...       # run all tests
```

## Questions

Open an issue.
