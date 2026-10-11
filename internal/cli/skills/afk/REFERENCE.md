# AFK supervision reference

`munsu afk` runs a foreground away-mode daemon. It owns the consent flag, PID lock, batched digest, and wedge detection. It diagnoses and accumulates; it never writes to the General's pane. `munsu afk return` stops the daemon and prints the digest; `munsu afk return check` exits non-zero while actionable state remains.

Safety invariants:

1. Every operation is scoped to one `MUNSU_HOME`.
2. The daemon never repairs: nothing it detects is acted on without the General.
3. The sentinel marker distinguishes wake-delivery nudges from user input; AFK writes none.
4. Repeated wakes are bounded by digesting, not by writing to a pane.
5. Return is idempotent.
6. AFK never merges, approves, or changes delivery authority.

State files, all under `MUNSU_HOME`:

| Path | Purpose |
|---|---|
| `state/.afk` | consent flag (RFC3339 timestamp) |
| `state/.afk.lock` | identity lock of the running daemon |
| `state/.afk-digest` | batched escalation digest (JSON), cleared on start |
| `state/.afk-wedge-alarm` | prior wedge alarm marker, removed on start (nothing in this tree writes it) |
| `state/.seen-*` | watcher dedup markers, cleared on start |
| `state/.subsuper-*` | subsupervisor escalation artifacts, cleared on start |
