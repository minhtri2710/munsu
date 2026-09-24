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

Detection checks each adapter's env markers (CLAUDECODE, CODECLIMB, OPENCODE, PI_CODING_AGENT_DIR, PI_CODING_AGENT, GROK_VM_ID, GROK_AGENT, ANTIGRAVITY_LS_ADDRESS, ANTIGRAVITY_AGENT), then falls back to process ancestry.

## Turn-end hooks

Each adapter's turn-end hook is its `TurnEndHook` entry in `internal/harness/adapter.go`. See `REFERENCE.md` for dispatch precedence.

---

See `REFERENCE.md` for the complete reference.
