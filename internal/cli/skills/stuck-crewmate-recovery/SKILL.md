---
name: stuck-crewmate-recovery
description: Playbook for unresponsive or stuck crewmates — escalation ladder (peek, steer, interrupt, relaunch, fail) and when-to-use guide.
user-invocable: false
metadata:
  internal: true
---

# stuck-crewmate-recovery — crewmate recovery escalation

Thin agent-only wrapper for `docs/skills/stuck-crewmate-recovery.md`. The canonical full doc at that path covers the full escalation ladder and condition-based guidance for deciding which level to apply.

## Escalation ladder

1. **Peek** — `munsu peek <id>`
2. **Steer** — `munsu send <id> "<instruction>"`
3. **Interrupt** — harness-specific Ctrl+C via herdr/tmux send-keys
4. **Relaunch** — `munsu teardown <id> --force` then re-spawn
5. **Fail** — append `failed:` to status file with evidence

## When to use each level

Consult `docs/skills/stuck-crewmate-recovery.md` for the condition table mapping situations (no status writes, repeated questions, rate limits, stale output, pipeline stalls, unreachable models) to the appropriate action.

---

See `docs/skills/stuck-crewmate-recovery.md` for the complete reference.
