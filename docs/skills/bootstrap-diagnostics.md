# Bootstrap diagnostics — agent-only reference

Handling playbook for session-start bootstrap diagnostics.

## Diagnostic lines

When the session-start digest prints any of these lines, handle as follows:

| Line pattern | Handling |
|---|---|
| `MISSING: <tool>` | Tool is missing from PATH. Suggest install command if known. |
| `NEEDS_GH_AUTH` | `gh auth login` is needed. |
| `TANGLE:` | Primary checkout has a non-default branch checked out. Restore with `git checkout <default>`. |
| `CREW_HARNESS_OVERRIDE: <name>` | Crew harness is set to a non-default adapter. Note for spawn decisions. |
| `CREW_DISPATCH: active` | Dispatch profile rules are active. Consult before spawning. |
| `FLEET_SYNC:` | Fleet clones were synced or STUCK. Review STUCK entries. |
| `SECONDMATE_SYNC:` | Secondmate homes were fast-forwarded. |
| `SECONDMATE_LIVENESS:` | Secondmates were probed; any dead ones were respawned. |
| `TASKS_AXI: available` | Tasks-axi is ready. Use it for backlog ops. |

## Silent bootstrap

If no diagnostic lines are printed, everything is good — proceed normally.
