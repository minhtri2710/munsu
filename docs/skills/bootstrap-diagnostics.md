# Bootstrap diagnostics reference

## Diagnostic lines

| Line pattern | Handling |
|---|---|
| `MISSING: <tool>` | Tool is missing from PATH. Suggest an install command if known. |
| `NEEDS_GH_AUTH` | Run `gh auth login`. |
| `SOLDIER_HARNESS: <name>` | Note the selected Soldier harness before spawning. |
| `SOLDIER_DISPATCH: active (<n> rules)` | Dispatch profiles are active; inspect them before spawning. |
| `SECOND_LIVENESS:` | Relaunch dead Captain endpoints with `munsu captain recover` or `munsu session-start --recover`. |

If no diagnostic lines appear, proceed normally.
