# AFK supervision reference

`munsu afk` runs a foreground away-mode daemon. It owns the consent flag, PID lock, batched digest, wedge detection, and safe General-pane injection. `munsu afk return` stops the daemon and prints the digest; `munsu afk return check` exits non-zero while actionable state remains.

Safety invariants:

1. Every operation is scoped to one `MUNSU_HOME`.
2. Injection requires the consent flag, a configured target, and an empty composer.
3. The sentinel marker distinguishes daemon injections from user input.
4. Duplicate injection is bounded by cooldown and digesting.
5. Return is idempotent.
6. AFK never merges, approves, or changes delivery authority.
