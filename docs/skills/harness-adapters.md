# Harness adapters — agent-only reference

Verified adapter launch templates for spawning soldiers.

## Launch templates per harness

| Harness | Model flag | Effort flag | Autonomy / interactive |
|---|---|---|---|
| claude | `--model` | none (n/a) | — |
| codex | none | n/a | — |
| opencode | `--model` | n/a | — |
| pi | `--model` | `--thinking` (low/medium/high) | — |
| grok | none | n/a | — |
| agy | `--model` | none | `--dangerously-skip-permissions` + `-i` |

## Harness detection

Check env markers, then process ancestry:
- Claude Code: `CLAUDE_CODE=1` | `CODECLIMB=1`
- Codex: `GITHUB_COPILOT=1`
- OpenCode: `OPENCODE=1`
- Pi: `PI_CODING_AGENT_DIR` set
- Grok: `GROK_VM_ID` set
- Agy: `ANTIGRAVITY_AGENT=1`

Fallback: check process tree for harness-specific argv patterns.

## Turn-end hooks

The turn-end guard marks the end of a soldier's turn after a status write or pane staleness.
Installed per-spawn; removed on teardown.

## Dispatch profiles (`config/soldier-dispatch.json`)

Supports `defaultHarness`/`defaultModel`/`defaultEffort` + `profiles` (`match`/`when`, `harness`/`model`/`effort`, optional `use[]`),
and the alternate `default` + `rules` shape.

Manage via `munsu config dispatch show|set-default|add|rm|path`.

Spawn precedence: CLI `--harness`/`--model`/`--effort` > matched dispatch profile > adapter template defaults.
