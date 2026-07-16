# OpenCode supervision protocol

**Mode:** TUI plugin background wake

## Supervision loop

When this session owns supervision and away mode is not active:

1. Drain first: `munsu wake-drain`.
2. The `.opencode/plugins/fm-primary-watch-arm.js` plugin arms supervision after the OpenCode session goes idle.
3. The plugin listens for `session.idle`, spawns `munsu watch-arm --restart` without awaiting it, and calls `client.session.promptAsync` when the child exits with an actionable watcher reason.
4. If the plugin reports `watcher: healthy ...`, do not start another cycle.
5. If the plugin reports a watcher failure, drain, inspect, and use `munsu watch-arm` manually only as a short recovery probe.
6. Never use shell `&` for watcher supervision.
7. Do not rely on this plugin in headless `opencode run` — primary supervision targets persistent TUI sessions.

## Key differences from firstmate

- Use `munsu watch-arm` instead of `bin/fm-watch-arm.sh`.
- Use `munsu wake-drain` instead of `bin/fm-wake-drain.sh`.
- No PreToolUse seatbelt — munsu's pull-based watcher replaces turn-end hooks for crewmates.

## Harness-specific

- OpenCode's persistent TUI plugin runtime is the wake mechanism.
- The plugin applies in the main primary checkout and a secondmate's own home.

## See also

- `munsu watch-arm --help`, `munsu wake-drain --help`, `munsu guard --help`
- Seeded `AGENTS.md` (orchestrator operating manual) §4 (Harness dispatch) and §5 (Supervision protocol)
