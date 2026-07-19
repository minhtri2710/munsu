---
name: harness-adapters
description: Verified adapter launch templates for spawning crews — model flags, effort flags, harness detection (env markers, process ancestry), and turn-end hooks.
user-invocable: false
metadata:
  internal: true
---

# harness-adapters — launch templates and harness detection

Thin agent-only wrapper for `docs/skills/harness-adapters.md`. The canonical full doc at that path covers the launch template table (model flag and effort flag per harness), harness detection via env markers and process ancestry, and turn-end hooks mechanics.

## Launch templates per harness

Consult `docs/skills/harness-adapters.md` for the complete table of model flags and effort flags for claude, codex, opencode, pi, and grok harnesses.

## Harness detection

The canonical doc covers env marker checks (CLAUDE_CODE, GITHUB_COPILOT, OPENCODE, PI_CODING_AGENT_DIR, GROK_VM_ID) and process tree fallback.

## Turn-end hooks

See `docs/skills/harness-adapters.md` for turn-end guard installation and removal details.

---

See `docs/skills/harness-adapters.md` for the complete reference.
