# Stuck Soldier recovery reference

## Escalation ladder

1. Peek with `munsu peek <id>`.
2. Steer with one bounded `munsu send <id> "<instruction>"` command.
3. Interrupt a spinning harness with Ctrl+C through its session backend.
4. Relaunch with `munsu teardown <id> --force`, then spawn again with a progress note.
5. Report failure only after the preceding steps are exhausted.

| Condition | Action |
|---|---|
| No status writes | Peek, then steer. |
| Repeated question | Steer with the missing answer. |
| Provider rate limit | Pause and retry on a longer cadence. |
| No output for more than five minutes | Peek, steer, then interrupt if needed. |
| CI pipeline stalled without checks | Steer toward the documented fallback. |
| Model unreachable | Wait or relaunch through a verified adapter. |
