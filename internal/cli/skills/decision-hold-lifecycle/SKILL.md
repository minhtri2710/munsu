---
name: decision-hold-lifecycle
description: >-
  Agent-only policy for completing investigations and visual reviews without losing unresolved captain decisions.
  Load before treating an investigation, scout report, or structured review as complete, before ending a review
  that exposed a decision, and when recording or routing the captain's answer.
user-invocable: false
metadata:
  internal: true
---

# decision-hold-lifecycle — durable unresolved-decision lifecycle

This skill is the single policy owner for unresolved captain decisions discovered during an investigation or review.

## Policy

Every unresolved decision that belongs to the captain and is discovered while producing, reading, presenting, or ending an investigation or review must become a structured captain-held work item in the authoritative backlog of the home that owns the originating work before that work or review may be treated as complete.

The agent performs the semantic inventory — scripts must not infer decisions from report prose, terminal output, or chat.

**Procedure using munsu primitives:**

1. **Record each unresolved decision.** Give each distinct unresolved decision a stable privacy-safe key. Append a `needs-decision:` status line to the originating task:
   ```
   munsu task status <id> "needs-decision" "<key>: <one-line summary>"
   ```
2. **Capture context.** Use `munsu stow` to save the full decision context as a durable learning if the context is non-obvious from the report alone.
3. **Block dependent work.** When other tasks depend on the decision, record the dependency:
   ```
   munsu backlog block <dependent-id> --by <blocker-id>
   ```
   When using the tasks-axi backend, `--by <blocker-id>` is required. Omitting it falls back to manual backend.
   (The block will be cleared when the captain resolves the decision.)
5. **Record the answer.** After the captain decides, update the originating task:
   ```
   munsu task status <id> "resolved" "<key>: <captain decision summary>"
   ```
6. **Unblock dependent work.** When a decision clears a blocker:
   ```
   munsu backlog ready <dependent-id>
   ```
7. **Close the loop.** Verify that no stale `needs-decision:` status lines remain for the originating task.

## Operating sequence

1. Read the complete investigation result before declaring complete.
2. Inventory only genuine unresolved choices that require the captain — do not create holds for resolved findings, recommendations that need no choice, or prose that merely sounds decision-like.
3. For each choice, choose a stable key and record it on the originating task.
4. Relay the choices to the captain.
5. After the captain decides, record the answer and unblock any dependent work.
6. Confirm no stale `needs-decision:` holds remain.

## When to load this skill

- Before treating an investigation, scout report, or structured review as complete.
- Before ending a review that exposed an unresolved decision.
- When recording or routing the captain's answer to a decision.

## See also

- `docs/skills/decision-hold-lifecycle.md` — canonical reference with detailed mechanism.
- Firstmate's decision-hold Go module (`internal/decisionhold/`) in PR sequence D will add structured `munsu decision-hold` commands.

---

See `docs/skills/decision-hold-lifecycle.md` for the complete reference.
