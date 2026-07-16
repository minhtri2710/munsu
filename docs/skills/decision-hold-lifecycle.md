# decision-hold-lifecycle — canonical reference

## Status

Policy owner: `internal/cli/skills/decision-hold-lifecycle/SKILL.md` (embedded in munsu binary).

This doc records the mechanism and regression evidence.

## Mechanism

Captain decisions discovered during investigations or visual reviews must be tracked as structured holds before the originating work may be treated as complete.

### Hold lifecycle (Phase A — protocol only)

In Phase A, decision-hold uses status-file conventions and backlog primitives. Structured `munsu decision-hold` commands will be added in PR sequence D.

| Step | Action | Command |
|------|--------|---------|
| Record | Append `needs-decision` status | `munsu task status <id> "needs-decision" "<key>: <summary>"` |
| Block | Block dependent task | `munsu backlog block <dependent-id>` |
| Surface | Tell captain the key + summary | (escalation) |
| Record answer | Append resolved status | `munsu task status <id> "resolved" "<key>: <answer>"` |
| Unblock | Clear blocker | `munsu backlog ready <dependent-id>` |
| Verify | No stale needs-decision lines remain | `munsu backlog show <id>` |

### Hold lifecycle (Phase D+ — structured commands)

When the `munsu decision-hold` subcommand exists:

```
munsu decision-hold hold <key> --reason "<summary>" --from <task-id>
munsu decision-hold complete <key>... [--none]
munsu decision-hold resolve <key> --answer "<answer>" --unblock <dep-id>
```

### Key rules

- Each decision gets one stable key. Retry with the same key is idempotent.
- A completed investigation or ended visual review uses the same owner and completion command.
- The hold remains open until the captain's answer is recorded and any dependent work is unblocked.
- Bearings reads resolved holds; it must not scrape historical reports or chat.
- Resolved findings, recommendations needing no choice, and prose that merely sounds decision-like do not create holds.
