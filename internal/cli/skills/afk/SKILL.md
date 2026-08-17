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

# While away, the daemon triages wakes and accumulates a digest. It never
# writes to the general pane — you read the digest on return.

# On return:
munsu afk return
# → reads the digest summary, prints escalations/wedges/blocked

# Verify clean:
munsu afk return check
echo $?   # 0 = clean, 1 = actionable items remain
```

## Contract

See `REFERENCE.md` for the bundled contract:

- Consent flag (`state/.afk`) — durable away-mode marker
- Identity lock (`state/.afk.lock`) — prevents duplicate daemons per home
- Sentinel marker (U+2063) — distinguishes daemon-generated messages from captain input
- Batched digest (`state/.afk-digest`) — 60s window accumulation
- Wedge alarm — stale/missing beat, repeated wake
- Return catch-up gate — `return check` confirms all-clear
- Approval authority unchanged — AFK never merges or approves

## Safety rules

1. All AFK operations are scoped to a single `MUNSU_HOME` — never touches sibling homes
2. The daemon diagnoses only — it never writes to the general pane (ADR-0013)
3. Return is idempotent — safe to call on clean state
4. See `REFERENCE.md` for the full safety invariants
