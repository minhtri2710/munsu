---
name: harness-adapters
description: Verified adapter launch templates for spawning soldiers — model flags, effort flags, harness detection (env markers, process ancestry), and turn-end hooks.
user-invocable: false
metadata:
  internal: true
---

# harness-adapters — launch templates and harness detection

Agent-only wrapper for the bundled `REFERENCE.md`, covering launch templates, harness detection, and turn-end hooks.

## Launch templates per harness

Consult `REFERENCE.md` for the complete model and effort flag table.

## Harness detection

The canonical doc covers env marker checks (CLAUDE_CODE, GITHUB_COPILOT, OPENCODE, PI_CODING_AGENT_DIR, GROK_VM_ID) and process tree fallback.

## Turn-end hooks

See `REFERENCE.md` for turn-end guard and dispatch details.

---

See `REFERENCE.md` for the complete reference.
