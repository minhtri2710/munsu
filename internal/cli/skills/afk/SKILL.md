---
name: afk
description: Away-mode supervision daemon for munsu — start, return, and check the AFK sub-supervisor lifecycle.
---

# afk — away-mode supervision skill

Implements the AFK supervision protocol with munsu-native commands.

## Commands

```
munsu afk                    # Start the away-mode daemon (foreground, ^Z bg to background)
munsu afk return             # Stop daemon, drain digest, print summary
munsu afk return check       # Exit 0 when clean, non-zero when actionable state remains
```

## Quick start

```sh
# Start the AFK daemon in a dedicated pane (or ^Z bg after start):
munsu afk

# While away, the daemon triages wakes, accumulates a digest, and optionally
# injects summaries into the general pane (if config/general-pane is set).

# On return:
munsu afk return
# → reads the digest summary, prints escalations/wedges/blocked

# Verify clean:
munsu afk return check
echo $?   # 0 = clean, 1 = actionable items remain
```

## Contract

See `docs/skills/afk.md` for the full contract:

- Consent flag (`state/.afk`) — durable away-mode marker
- Identity lock (`state/.afk.lock`) — prevents duplicate daemons per home
- Sentinel marker (U+2063) — distinguishes daemon injects from captain input
- Batched digest (`state/.afk-digest`) — 60s window accumulation
- Wedge alarm — stale/missing beat, repeated wake
- Target safety — inject only when composer is Empty
- Return catch-up gate — `return check` confirms all-clear
- Approval authority unchanged — AFK never merges or approves

## Safety rules

1. All AFK operations are scoped to a single `MUNSU_HOME` — never touches sibling homes
2. Inject requires consent flag + configured target + Empty composer (triple gate)
3. Return is idempotent — safe to call on clean state
4. See `docs/skills/afk.md` for full safety invariants
