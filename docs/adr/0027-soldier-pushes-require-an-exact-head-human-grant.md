# 0027. Soldier Pushes Require an Exact-Head Human Grant

* **Status:** Superseded: the Human dropped the push grant on 2026-10-09 (open-workflow audit, decision A1 = c, ledger G926); Soldiers push their task branch when committed, and the merge gate (ADR-0025, ADR-0026) is unchanged.
* **Date:** 2026-10-09
* **Extends:** ADR-0005 §6 (the git shim is a workflow fence, not a sandbox), ADR-0022 (the durable delivery contract), ADR-0024 (new refusals require the owner's words), ADR-0025 (the Words record), ADR-0026 (the landing gate record)
* **Triggered by:** Human decision G878 (Lead gate ledger), option A, answered “a”

## Context

ADR-0026 gates merge of an open PR at its exact head. It does not gate how the
branch first reaches the provider. In `direct-PR`, a Soldier pushes to `origin`
and creates the PR. In `no-mistakes`, the Soldier pushes to the gate remote;
the no-mistakes pipeline forwards the branch and creates the PR. Both pushes
run through the munsu Git shim when the launched Soldier uses the supported
path (`internal/fleet/soldier_launch.go`, `internal/cli/git_guard_cmd.go`).
General decision holds only gate Fleet dispatch and do not stop a running
Soldier (`internal/cli/decisionhold_cmd.go`, `internal/taskauthority/dispatch.go`).

## Decision

Before either supported Soldier publication path, the Soldier commits, obtains
the full commit SHA, reports `munsu report needs-decision "push <sha>"`, and
stops. The General routes the exact head to the Human. After the Human approves,
the General records the grant with:

```sh
munsu delivery push-grant <task-id> --head <full-40-hex-sha> \
  --grantor <human> --channel <channel> --quote <verbatim-human-words>
```

The canonical Task Authority push-grant record binds the task ID, its current
Generation, one full Git commit SHA, the typed `domain.Words`, and the named
`domain.Operation`. The commit is journaled atomically; replay with the same
operation is idempotent and a reused operation ID with changed intent conflicts.
Reopening the task starts a new Generation, so grants from its prior Generation
do not carry. Merge authorization remains separate and unchanged: the reviewed
exact-head PASS verdict and ADR-0026 landing gate still apply.

`git-guard` refuses a normal task-branch push to `origin` or the configured
`no-mistakes` remote unless the current task Generation has a grant for the
exact commit being pushed. The gate remote accepts a bare `git push no-mistakes`
only when `remote.no-mistakes.push` explicitly maps one task-local branch;
otherwise the destination cannot be resolved deterministically and the guard
refuses. A granted push is passed to Git unchanged. On refusal, the Soldier
reports `needs-decision` with the named head and waits for the General to record
the grant before retrying. A later commit requires its own grant.

The Human answered “a” to G878, choosing option A, whose text reads:
“A (Lead recommends). Supported-path gate, both modes. The Soldier commits, reports needs-decision with the exact
head, and stops. The General routes it to the Human. A munsu command records the
Human grant bound to the task, its generation, and that exact SHA. git-guard
refuses the Soldier's push (to origin or to the gate remote) unless a matching
grant exists. Merge stays the existing Human gate on its exact head. A new ADR
(v1) records the contract.” The accepted ceilings from that decision are:

* It stops a Soldier that follows its brief, not a hostile process that bypasses
  the shim or writes the grant (ADR-0005 §6, ADR-0025 §3).
* In no-mistakes mode the grant covers the head entering the pipeline; gate fix
  commits are seen by the Human at the merge gate, not before the PR opens.
* PR creation via `gh` is not intercepted.

These are workflow controls, not authentication or a security sandbox. The
`domain.Words` record stores the General's attribution of the Human's decision;
it does not authenticate the grantor. No GitHub PR-create or no-mistakes source
integration is added.

## Consequences

Both remote delivery modes require a Human decision for the committed head
before the first branch push. Local-only delivery remains unchanged. Existing
provider merge refusal, review-verdict requirements, and the ADR-0026 landing
gate are unchanged.
