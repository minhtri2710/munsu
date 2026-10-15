# Contributing

Thanks for wanting to contribute.

## Workflow

Push a feature branch, open a pull request against `main` with `gh pr create`,
and let the required checks gate it; the Human merges. `AGENTS.md` ("Delivery
rules") owns the details.

## Repo conventions

- **Language:** Go. Build, lint and test commands are in `AGENTS.md` ("Build / test / lint").
- **CLI framework:** cobra (github.com/spf13/cobra).
- **Module path:** github.com/minhtri2710/munsu.
- **AGENTS.md** is the project conventions file for soldiers working on munsu. `CLAUDE.md` is a symlink to it.
- Everything personal (`state/`, `data/`, `config/`, `projects/`) is gitignored; never commit it.
- Keep `README.md` a concise overview plus pointers.

## Development

CI lane composition (race detector, integration tags) lives in `.github/workflows/ci.yml`; build-tag classification and lane coverage checks live in `.github/build-tags.manifest` and `.github/scripts/build-tags.sh`.

## Questions

Open an issue.
