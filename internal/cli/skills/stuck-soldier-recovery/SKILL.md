---
name: stuck-soldier-recovery
description: Playbook for unresponsive or stuck soldiers — escalation ladder (peek, steer, interrupt, relaunch, fail) and when-to-use guide.
user-invocable: false
metadata:
  internal: true
---

# stuck-soldier-recovery — soldier recovery escalation

Agent-only wrapper for the bundled `REFERENCE.md`, covering the escalation ladder and condition-based guidance.

## Escalation ladder

1. **Peek** — `munsu peek <id>`
2. **Steer** — `munsu send <id> "<instruction>"`
3. **Interrupt** — harness-specific Ctrl+C via herdr/tmux send-keys
4. **Relaunch** — `munsu teardown <id> --force` then re-spawn. Teardown
   retains the task data directory and any report under its generation-bound name.
5. **Fail** — append `failed:` to status file with evidence

## When to use each level

Consult `REFERENCE.md` for the condition table mapping symptoms to the appropriate action.

---

See `REFERENCE.md` for the complete reference.
