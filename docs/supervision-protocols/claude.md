# Claude supervision protocol

**Mode:** background-notify

## Supervision loop

When this session owns supervision and away mode is not active:

1. Drain first: `munsu wake-drain`.
2. Arm the watcher as a background task: `munsu watch-arm`.  
   Do not bundle with other commands and do not use shell `&`.
3. Treat `watcher: started ...` or `watcher: attached ...` as proof of a live cycle.
4. On completion with `signal:`, `stale:`, `check:`, or `heartbeat`: drain, handle, re-arm.
5. Treat `watcher: FAILED - no live watcher with a fresh beacon` as an alarm; repair before ending turn.
6. For a forced restart: `munsu watch-arm --restart`.
7. Do not send idle progress while the watcher is parked.

## Key differences from firstmate

- Use `munsu watch-arm` instead of `bin/fm-watch-arm.sh`.
- Use `munsu wake-drain` instead of `bin/fm-wake-drain.sh`.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for crews.
- The primary session's own turn-end guard is the agent's responsibility per the seeded orchestrator manual.

## Harness-specific

- The watcher itself is `munsu watch`; `munsu watch-arm` is the verified arm wrapper.
- `munsu watch ensure` provides an idempotent alternative to check/arm the watcher.
- On re-arm, an existing healthy cycle is reused; the background task stays live until it ends.

## See also

- `munsu watch-arm --help`, `munsu wake-drain --help`, `munsu guard --help`
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
