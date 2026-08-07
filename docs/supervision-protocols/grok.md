# Grok supervision protocol

**Mode:** background-notify

## Supervision loop

When this session owns supervision and away mode is not active:

1. Claim first: `munsu wake claim --consumer <id>`.
2. Arm the watcher as a background tool call: `munsu watch-arm`.  
   In Grok, use the tracked background tool (`run_terminal_command` with `background: true`).
   Do not bundle with other commands and do not use shell `&`.
3. Treat `watcher: started ...` or `watcher: attached ...` as proof of a live cycle.
4. On completion (Grok injects a synthetic user message with `synthetic_reason: task_completed`):
   - Claim with `munsu wake claim`.
   - Handle the wake reason (`signal`, `stale`, `check`, `heartbeat`).
   - Re-arm if work remains in flight or X-mode polling is needed.
5. Treat `watcher: FAILED ...` as an alarm; fix and re-arm.
6. For a forced restart: `munsu watch-arm --restart`.
7. Waiting is silent; do not send idle progress.
8. Do not invent a wake from an attach-status line alone.

## Key supervision commands

- Use `munsu watch-arm` to arm the watcher.
- Use `munsu wake claim` to claim queued wake records under a lease.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for soldiers.

## Harness-specific

- Grok's background tool completion is the wake mechanism.
- Interactive TUI primary sessions are the supported supervision host. Headless `grok -p` may not surface full wake output.

## See also

- `munsu watch-arm --help`, `munsu wake claim --help`, `munsu guard --help`
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
