# Harness adapters reference

## Launch templates

| Harness | Model flag | Effort flag | Default model / effort | Extra launch args |
|---|---|---|---|---|
| claude | `--model` | none | none | none |
| codex | `--model` | `--effort` | `gpt-5.2-codex` / `80` | none |
| opencode | `--model` | `--effort` | `gpt-5.2-codex` / `80` | none |
| pi | `--model` | `--thinking` | none | none |
| grok | `--model` | `--reasoning-effort` | none | none |
| agy | `--model` | none | none | `--dangerously-skip-permissions` |

Harness detection checks environment markers, then process ancestry. Dispatch precedence is CLI override, matched dispatch profile, the project's Soldier harness and model, then adapter template defaults. Manage profiles with `munsu config dispatch`.
