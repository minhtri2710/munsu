# decision-hold-lifecycle — canonical reference

## Status

Policy owner: `internal/cli/skills/decision-hold-lifecycle/SKILL.md` (embedded in munsu binary).

This doc records the mechanism and regression evidence.

## Mechanism

General decisions discovered during investigations or visual reviews must be tracked as structured holds before the originating work may be treated as complete.

### Hold lifecycle

The embedded policy owner is authoritative for the unresolved-decision procedure:
`internal/cli/skills/decision-hold-lifecycle/SKILL.md`. For the registered command
forms and flags, use `munsu decision-hold --help` and the relevant subcommand help;
this reference intentionally does not duplicate that syntax.

### Key rules

- Each decision gets one stable key. Retry with the same key is idempotent.
- A completed investigation or ended visual review uses the same owner and completion command.
- The hold remains open until the general's answer is recorded and any dependent work is unblocked.
- Bearings reads resolved holds; it must not scrape historical reports or chat.
- Resolved findings, recommendations needing no choice, and prose that merely sounds decision-like do not create holds.
