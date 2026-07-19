# Pi supervision protocol

**Mode:** extension background wake

## Supervision loop

When this session owns supervision and away mode is not active:

1. Drain first: `munsu wake-drain`.
2. Confirm Pi has loaded the munsu watcher extension (`fm-watch-arm-pi` custom tool).  
   If not, restart with both watcher and turn-end guard extensions loaded.
3. Arm supervision using the `fm_watch_arm_pi` custom tool (or `/fm-watch-arm-pi` as human fallback).  
   Do **not** run `munsu watch-arm` through Pi's bash tool — that can wedge the agent and bypasses extension-owned cleanup.
4. The extension starts `munsu watch-arm --restart`, keeps the child attached, and sends a follow-up user message when the child exits with an actionable reason.
5. If the extension says the watcher is already healthy, do not start another cycle.
6. If the extension reports a watcher failure, drain, inspect, and restart Pi with extensions if needed.
7. Never use shell `&` for watcher supervision.

## Key supervision commands

- Use `munsu watch-arm` to arm the watcher.
- Use `munsu wake-drain` to drain queued wake records.
- Pi extension tool `fm_watch_arm_pi` is the munsu-owned arm mechanism.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for soldiers.

## Harness-specific

- Pi's extension runtime automatically sends follow-up messages on watcher exit.
- The turn-end guard extension is a structural backstop, not the normal wake path.

## See also

- `munsu watch-arm --help`, `munsu wake-drain --help`, `munsu guard --help`
- `munsu skill show harness-adapters` — Pi launch template details.
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
