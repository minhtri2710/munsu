# captain-provisioning — full lifecycle migration sequence

Companion to `SKILL.md` (router). Follow this runbook when moving a captain to a
new home or upgrading its infrastructure. Per-verb details live in
[REFERENCE.md](REFERENCE.md).

When moving a captain to a new home or upgrading its infrastructure:

1. **Update** — `munsu captain update <captain-home>` to ensure the captain is
   on the latest instruction surface.
2. **Verify idle** — Confirm no in-flight soldiers via `munsu captain list`,
   `munsu soldier-state <id>`, pane liveness, and backlog.
3. **Retire** — `munsu captain retire <captain-home>` to stop the captain
   process and clear parent meta. Use `--force` only after proving blockers
   are stale/done.
4. **Migrate** — `munsu captain migrate <captain-home> <id> --repo <path>`
   to create the managed worktree. This does NOT retire or relaunch the live
   captain, so verify the retire step was already completed.
5. **Validate worktree and backup** — Confirm the migrated home exists at
   the new worktree path and the old home is backed up.
6. **Recover** — `munsu captain recover <captain-id>` to validate provenance,
   refresh the charter and config, and confirm launch readiness while the
   migrated Captain is still stopped.
7. **Launch** — `munsu captain launch <captain-home>` to start the Captain
   process in the new home.
8. **Converge / Guard** — `munsu captain converge` then `munsu guard` to
   reconcile state and verify fleet health.
