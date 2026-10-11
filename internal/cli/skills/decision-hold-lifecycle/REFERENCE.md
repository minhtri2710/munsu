# Decision-hold lifecycle reference

Every unresolved General decision found during an investigation or review must be recorded before the originating work is treated as complete.

1. Record a stable keyed `needs-decision` status.
2. Preserve non-obvious context with `munsu stow`.
3. Block dependent work through the Task Authority.
4. Relay the choice to General.
5. Record the answer as `resolved`.
6. Unblock dependent work.
7. Verify no keyed decision remains open.

Structured `munsu decision-hold` commands are preferred when available; otherwise use task status and task block/unblock primitives.

## Key rules

- Each decision gets one stable key. Retry with the same key is idempotent.
- A completed investigation or ended visual review uses the same owner and completion command.
- The hold remains open until the general's answer is recorded and any dependent work is unblocked.
- `munsu decision-hold list` reads the recorded holds; nothing scrapes historical reports or chat for decisions.
- Resolved findings, recommendations needing no choice, and prose that merely sounds decision-like do not create holds.
