# Supervision loop — watch / wake-drain / guard / afk

This document details the crewmate supervision loop. For the high-level step, see `SKILL.md` step 5.

## Watch loop (`munsu watch`)

The watcher is a singleton persistent event-driven daemon. It queues actionable wake reasons and continues polling until stopped.

- **Ensure:** `munsu watch ensure [--restart]`
- **Run once:** `munsu watch` (exits with wake reason or does nothing)
- **Singleton:** Home-scoped lock prevents multiple concurrent watchers.

## Wake handling (`munsu wake-drain`)

When the watcher fires, a wake is queued. The operator drains it:

```sh
munsu wake-drain    # process all queued wakes
munsu crew-state <id>  # read ground truth (not raw status tail)
```

After handling actionable wakes, confirm the persistent watcher remains healthy if tasks are still in flight:

```sh
munsu watch ensure
```

## Post-action guard (`munsu guard`)

Run `munsu guard` after every fleet action. It checks for:
- **Tangle**: primary checkout has a non-default branch checked out.
- **Stale watcher**: watcher lock is held but no process is alive.

## Away mode (`munsu afk`)

When stepping away from the fleet, start the away-mode daemon:

```sh
munsu afk
```

This sets an AFK flag and polls the fleet at a reduced cadence. Only captain-relevant events (done, failed, needs-decision) are printed. Stop with SIGTERM/SIGINT; the flag is cleared on stop.

## Stuck crewmate recovery

If a crewmate is unresponsive, do not re-implement recovery logic here. Consult `docs/skills/stuck-crewmate-recovery.md` for the escalation ladder:

1. `munsu peek <id>` — read last N lines.
2. `munsu send <id> "<instruction>"` — steer.
3. Interrupt (harness-specific: Ctrl+C via tmux/herdr send-keys).
4. `munsu teardown <id> --force` then `munsu spawn <id> <project>` — relaunch.
5. Append `failed:` only after all above exhausted.
