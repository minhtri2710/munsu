# AFK away-mode supervision — munsu-native contract

> munsu ships a Go-native AFK sub-supervisor daemon (`internal/orchestrator/`). This doc covers
> the contract, CLI, lifecycle, and safety invariants.

## CLI

```
munsu afk                Start the away-mode daemon (foreground, blocks until SIGTERM/SIGINT)
munsu afk return         Stop daemon, drain digest, print summary of escalations/wedges/blocked
munsu afk return check   Exit 0 when clean, non-zero when actionable state remains
```

All three take `--home` / `MUNSU_HOME` to scope to a specific home.

## Lifecycle

```
      consent flag set (state/.afk)
      identity lock (state/.afk.lock)
      stale artifacts cleared
            │
            ▼
    ┌───────────────────┐
    │   runLoop (30s)   │──── triage → feed digester → wedge check → flush
    └───────────────────┘
            │
      SIGTERM/SIGINT
            │
            ▼
      consent flag cleared
      identity lock released
      final digest flush (state/.afk-digest)
```

The daemon is **foreground**: it holds the terminal. The operator starts it in a pane, sends it to
the background with `^Z bg`, or launches it in a dedicated session. The daemon never daemonizes
itself.

## Contract

### Consent flag (`state/.afk`)

The durable marker that the daemon is in away-mode. Written at start, removed on clean shutdown.
All digestion gates check this flag. Absence == no AFK.

Contents: RFC3339 UTC timestamp of start.

### Identity lock (`state/.afk.lock`)

PID-based lock that prevents two daemon instances in the same home. Format:

```
<PID>\t<RFC3339>
```

Idempotent acquire: if the lock exists and the PID is alive, `Start` returns an error. Stale
locks (dead PID) are reclaimed silently.

### Sentinel marker (U+2063)

Wake-delivery activation nudges (`wakedelivery_deliver.go`) are prefixed with `\u2063` — the
Unicode INVISIBLE SEPARATOR. This zero-width marker distinguishes machine-delivered messages
from captain-typed input. The return gate (`IsReturnSignal`) rejects marked lines as return
candidates; the AFK daemon writes no marked message of its own.

### Batched digest (`state/.afk-digest`)

The durable accumulation of triage results over a 60s window. Written by the digester on flush.
Each entry has a type (routine, decision, failure, credential, review-ready, wedge) and carries
the wake payload. The digest also records:

- `wedge_alarm` — if a wedge condition was detected during the window

### Wedge alarm

Detects three conditions:
1. **Stale watcher beat** — watcher beat file older than 5m
2. **Missing watcher beat** — beat file never written
3. **Repeated stale wake** — identical wake key arriving 3+ times in a row (within 2 poll intervals)

On detection the alarm is recorded in the digest and surfaced in the return report.

### Return catch-up gate

`munsu afk return check` re-reads the digest file and exits 0 only when no actionable state
remains. Actionable state is any non-routine escalation, wedge alarm, or blocked item.

The general runs `munsu afk return` to drain the digest, print the summary, and clear the flag.
After reconciling any escalations, `munsu afk return check` confirms all-clear.

### Approval authority unchanged

AFK does not change the merge/approval authority. The daemon only monitors and digests. It
never merges, approves, or modifies delivery state.

## Files (all under `MUNSU_HOME`)

| Path | Purpose |
|------|---------|
| `state/.afk` | Consent flag (RFC3339 timestamp) |
| `state/.afk.lock` | Identity lock (PID + timestamp) |
| `state/.afk-digest` | Batched escalation digest (JSON) |
| `state/.seen-*` | Watcher dedup markers (cleared on start) |
| `state/.subsuper-*` | Subsupervisor artifacts (cleared on start) |

## Safety invariants

1. **No repair, only diagnosis** — the daemon never writes to the general pane (ADR-0013)
2. **No digest without consent flag** — every digestion path checks `state/.afk`
3. **Stale lock reclamation** — dead PID lock is reclaimed, never blocking a new start
4. **Idempotent return** — multiple `munsu afk return` calls on clean state succeed
