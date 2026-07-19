# Codex supervision protocol

**Mode:** foreground checkpoint

## Supervision loop

When this session owns supervision and away mode is not active:

1. Drain first: `munsu wake-drain`.
2. Run one foreground watcher checkpoint: `munsu watch run`.
   The command polls once and exits — no timeout flag is needed.
3. If the command prints `signal:`, `stale:`, `check:`, or `heartbeat`: drain queued wakes, handle that wake, then start the next checkpoint.
4. If the command exits with `checkpoint:`: drain queued wakes anyway, process any visible user message, then start the next checkpoint.

## Key differences from firstmate

- Use `munsu watch run` instead of `bin/fm-watch-checkpoint.sh`.
- Use `munsu wake-drain` instead of `bin/fm-wake-drain.sh`.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for soldiers.

## Harness-specific

- Codex cannot reason while a foreground tool call is running; the poll-once checkpoint returns control regularly.
- No background mechanism — use foreground checkpoints only.

## See also

- `munsu watch run --help`, `munsu wake-drain --help`, `munsu guard --help`
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
