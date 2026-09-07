---
name: bootstrap-diagnostics
description: Handle session-start bootstrap diagnostics — toolchain readiness lines (MISSING, NEEDS_GH_AUTH, TANGLE, SOLDIER_HARNESS, SOLDIER_DISPATCH, FLEET_SYNC, SECOND_SYNC, SECOND_LIVENESS, TASKS_AXI).
user-invocable: false
metadata:
  internal: true
---

# bootstrap-diagnostics — session-start diagnostics handling

Agent-only wrapper for the bundled `REFERENCE.md`, which covers every diagnostic line printed during session-start.

## Diagnostic lines

When the bootstrap diagnostics section prints lines like `MISSING: <tool>`, `NEEDS_GH_AUTH`, `TANGLE:`, `SOLDIER_HARNESS:`, `SOLDIER_DISPATCH:`, `FLEET_SYNC:`, `SECOND_SYNC:`, `SECOND_LIVENESS:`, or `TASKS_AXI:`, consult `REFERENCE.md` for the handling playbook.

## Silent bootstrap

If no diagnostic lines are printed, everything is good — proceed normally.

---

See `REFERENCE.md` for the complete reference.
