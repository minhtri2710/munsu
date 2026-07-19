# Stuck-crew recovery — agent-only reference

Playbook for unresponsive or stuck crews.

## Escalation ladder

1. **Peek** — `munsu peek <id>` to read the last N lines of pane output.
2. **Steer** — `munsu send <id> "<one-line instruction>"` to nudge the crew.
3. **Interrupt** — if the crew is spinning without making progress, use harness-specific interrupt:
   - Pi: `Ctrl+C` via herdr pane send-keys or tmux send-keys
   - Claude: `Ctrl+C` same mechanism
4. **Relaunch** — if interrupt+re-steer fails, teardown and re-spawn:
   - `munsu teardown <id> --force` then `munsu spawn <id> <project>`
   - Include a progress note: "relaunched after stale/lost state"
5. **Fail** — only after the above is exhausted:
   - Append `failed: {evidence of what happened}` to the status file

## When to use each level

| Condition | Action |
|---|---|
| Crew not writing status | Peek + steer |
| Repeated same question | Steer with answer |
| Rate limit / 429 | Paused — recheck on long cadence |
| No output for >5 min | Peek → if idle, steer → if no response, interrupt+steer |
| Pipeline stuck on CI with no checks | Steer to skip CI step |
| Model unreachable | Try re-arming or wait for provider recovery |
