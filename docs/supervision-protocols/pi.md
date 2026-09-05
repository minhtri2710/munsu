# Pi supervision protocol

**Mode:** extension background wake

## Supervision loop

When this session owns supervision and away mode is not active:

1. Confirm Pi has loaded the munsu integration extension (`munsu-pi-integration.ts`).
   If not, reinstall and restart: `munsu integrate install --harness pi`.
2. The extension arms the watcher automatically on session start (it runs `munsu session-start`, which arms the watcher).
   Do **not** run `munsu watch ensure` through Pi's bash tool — let the extension manage the watcher.
3. On `agent_settled` the extension claims queued wakes and sends a follow-up user message; resolve each with the `munsu_wake_resolve` tool (or the `/munsu:wake resolved [key=<slug>]: <summary>` command as human fallback).
4. If no wake is pending, do not start another cycle.
5. If the watcher is unhealthy, repair with `munsu watch ensure --restart` and restart Pi with the extension if needed.
6. Never use shell `&` for watcher supervision.

## Key supervision commands

- Use `munsu watch ensure` to arm the watcher.
- The extension uses `munsu wake claim` to claim queued wake records under a lease; do not claim them manually.
- Resolve claimed wakes with the `munsu_wake_resolve` tool or the `/munsu:wake` command.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for soldiers.

## Harness-specific

- After Pi becomes idle (`agent_settled`), the extension claims queued wakes and sends follow-up user messages.
- The turn-end guard extension is a structural backstop, not the normal wake path.

## See also

- `munsu watch ensure --help`, `munsu wake claim --help`, `munsu guard --help`
- `munsu skill show harness-adapters` — Pi launch template details.
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
