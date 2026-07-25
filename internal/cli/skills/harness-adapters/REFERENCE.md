# Harness adapters reference

## Launch templates

| Harness | Model flag | Effort flag | Autonomy / interactive |
|---|---|---|---|
| claude | `--model` | none | — |
| codex | none | none | — |
| opencode | `--model` | none | — |
| pi | `--model` | `--thinking` | low/medium/high |
| grok | none | none | — |
| agy | `--model` | none | `--dangerously-skip-permissions` + `-i` |

Harness detection checks environment markers, then process ancestry. Dispatch precedence is CLI override, matched dispatch profile, then adapter defaults. Manage profiles with `munsu config dispatch`.
