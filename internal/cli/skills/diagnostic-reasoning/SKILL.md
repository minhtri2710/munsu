---
name: diagnostic-reasoning
description: Systematic root-cause diagnostic reasoning methodology for inter-task dependencies and stuck soldier recovery.
---

# Diagnostic Reasoning Methodology

This skill provides a systematic approach for diagnosing stuck tasks, environment failures, and inter-task dependencies.

## Diagnostic Loop

When a soldier task is stuck, failing tests, or blocked, follow this 4-step diagnostic loop:

### Step 1: Fact Gathering (Empirical Evidence Only)

Never guess or assume root causes. Collect concrete evidence first:
1. Run `munsu doctor` to check toolchain readiness and environment diagnostics.
2. Read the full un-truncated error log or test output.
3. Check `munsu soldier-state <id>` to inspect pane liveness and current run-step.
4. Inspect the task's durable metadata projection and status event stream; use the logical task ID when querying them.

### Step 2: Isolation & Boundary Check

Determine whether the failure is local to the task or systemic:
- **Task-Local:** Code bug, missing import, failing test assertion inside the worktree.
- **Environment:** Toolchain missing (`gh-axi`, `treehouse`, `tmux`), auth failure, or disk space.
- **Dependency Block:** Blocked by another task or waiting for parent captain/general response.

### Step 3: Hypothesis Verification

Formulate a single testable hypothesis based strictly on the log evidence:
- Test the hypothesis with the minimal targeted command (e.g. `go test -v -run TestTarget ./internal/pkg`).
- If hypothesis fails, revert test state and formulate alternative hypothesis based on new log evidence.

### Step 4: Resolution & Escalation

- **If Task-Local Bug:** Fix the underlying contract error cleanly. Do not mask symptoms with silent fallbacks.
- **If Toolchain/Systemic:** Execute the `fix command` output by `munsu doctor`.
- **If Unresolvable Dependency:** Call `munsu report blocked "{root cause evidence}"` to escalate to parent Captain.
