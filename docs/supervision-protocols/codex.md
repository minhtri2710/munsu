# Codex supervision protocol

**Mode:** foreground checkpoint

## Supervision loop

When this session owns supervision and away mode is not active:

1. Claim first: `munsu wake claim --consumer <id>`.
2. Run one foreground watcher checkpoint: `munsu watch run`.
   The command polls once and exits — no timeout flag is needed.
3. If the command prints `signal:`, `stale:`, `check:`, or `heartbeat`: drain queued wakes, handle that wake, then start the next checkpoint.
4. If the command exits with `checkpoint:`: drain queued wakes anyway, process any visible user message, then start the next checkpoint.

## Key supervision commands

- Use `munsu watch run` to run the watcher.
- Use `munsu wake claim` to claim queued wake records under a lease.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for soldiers.

## Harness-specific

- Codex cannot reason while a foreground tool call is running; the poll-once checkpoint returns control regularly.
- No background mechanism — use foreground checkpoints only.

## See also

- `munsu watch run --help`, `munsu wake claim --help`, `munsu guard --help`
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
