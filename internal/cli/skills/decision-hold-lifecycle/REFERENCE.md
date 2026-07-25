# Decision-hold lifecycle reference

Every unresolved General decision found during an investigation or review must be recorded before the originating work is treated as complete.

1. Record a stable keyed `needs-decision` status.
2. Preserve non-obvious context with `munsu stow`.
3. Block dependent work through the backlog.
4. Relay the choice to General.
5. Record the answer as `resolved`.
6. Unblock dependent work.
7. Verify no keyed decision remains open.

Structured `munsu decision-hold` commands are preferred when available; otherwise use task status and backlog dependency primitives.
