---
name: bootstrap-diagnostics
description: Handle session-start bootstrap diagnostics — toolchain readiness lines (MISSING, NEEDS_GH_AUTH, TANGLE, CREW_HARNESS_OVERRIDE, CREW_DISPATCH, FLEET_SYNC, SECOND_SYNC, SECOND_LIVENESS, TASKS_AXI).
user-invocable: false
metadata:
  internal: true
---

# bootstrap-diagnostics — session-start diagnostics handling

Thin agent-only wrapper for `docs/skills/bootstrap-diagnostics.md`. The canonical full doc at that path covers the diagnostic line patterns and handling playbook for every bootstrap check printed during session-start.

## Diagnostic lines

When the bootstrap diagnostics section prints lines like `MISSING: <tool>`, `NEEDS_GH_AUTH`, `TANGLE:`, `CREW_HARNESS_OVERRIDE:`, `CREW_DISPATCH:`, `FLEET_SYNC:`, `SECOND_SYNC:`, `SECOND_LIVENESS:`, or `TASKS_AXI:`, consult `docs/skills/bootstrap-diagnostics.md` for the full handling playbook.

## Silent bootstrap

If no diagnostic lines are printed, everything is good — proceed normally.

---

See `docs/skills/bootstrap-diagnostics.md` for the complete reference.
