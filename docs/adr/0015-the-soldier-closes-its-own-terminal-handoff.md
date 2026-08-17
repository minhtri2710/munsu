# 0015. The Soldier Closes Its Own Terminal Handoff

* **Status:** Accepted
* **Date:** 2026-08-17
* **Extends:** ADR-0008 (one owner and one canonical implementation path)
* **Triggered by:** BEO-67 deadcode triage, batch 5 (#506) → BEO-112 verdict

## Context

A soldier's terminal report used to reach the General over a second, hand-rolled uplink:

```
DeliverWake writes a receipt into the captain home
  → ReconcilePending sweeps captain-owned pending receipts
  → raw AppendStatus into the General home
  → a hand-written state/<relay-task>.turnend file in the General home
  → WriteAck in the captain home
  → CompleteTaskObligation(ReportRelay) in the captain home
```

#417 (`5cca5b2`) deleted that sweep's only call site — `stepTerminalReconcile` in
`RecoverTransaction`, plus the `ReconcileTerminal` capability behind it — under ADR-0008's
hard cut, with the diff reason:

> "legacy read compatibility: drain terminal receipts created before the mailbox-only
> uplink path. New reports no longer create these artifacts."

**That premise is false in the current tree.** `DeliverWake` still writes those receipts
(`internal/orchestrator/wakedelivery_deliver.go:143`) for every material soldier report
that carries a `ParentHome`, and it self-acks only when the parent is **not** a captain
home (`:156`), on the explicit comment that "Captain-backed soldiers retain the
asynchronous receipt/ack/reconcile path". That path has not existed since #417.

The live producer is narrow but real: `munsu report` routes material soldier/captain
reports through the canonical mailbox uplink (`orchestrator.Report`), **except** a
soldier's terminal scout `done`, which still goes through `DeliverWake`
(`internal/cli/report_cmd.go:128`). A scout soldier under a Captain therefore writes a
receipt and an open `ReportRelay` obligation into the captain home that nothing in the
binary can ever close:

* `checkPendingRelayObligations` (`internal/cli/session_cmd.go:741`) reads pending
  receipts from `MUNSU_PARENT_STATUS` as well as the own home, so the guard answers
  `{"decision":"continue"}` on **every** subsequent turn — the session can never end.
* `VerifyRetirementContinuity` (`internal/orchestrator/retirement_journal.go:16`) sees the
  open `ReportRelay` obligation and refuses teardown without `--force`.
* The guard's remedy text points at `munsu turnend obligations`, which lists and closes
  obligations **by role** and writes no receipt ack at all. An operator who follows the
  instruction exactly stays stuck.

The bug survived review because `captainMessagingAdapter.ReconcilePending`
(`internal/cli/captain_ports.go:30`) shares the name but not the subject — it delegates to
`fleet.ReconcileMailboxPending`, which reconciles General→Captain *mailbox envelopes*. A
name grep shows the call site as live; only following the type reveals two different
things.

## Decision

The relay is **not** reconnected. `DeliverWake` acks its own terminal receipt and closes
its own `ReportRelay` obligation unconditionally, whether or not the parent is a captain
home. `ReconcilePending`, `reconcileOne`, `ReconcileResult` (with `Relayed`/`Failed`) and
`ReconcileOutcome` are deleted, along with `isCaptainHome`, whose only caller was the
branch being removed.

The terminal receipt file survives, because it has a live consumer that is not the relay:
`ActivateOnReceipt` (`captain_relay.go:55`, on the captain watcher's activation hook)
scans receipts regardless of ack state to nudge the captain's pane. The receipt is a
notification artifact; the ack is the writer's own handoff-closed marker.

### Why reconnecting is the wrong answer

1. **It would restore a second uplink into the General home.** `reconcileOne` appends raw
   status lines and writes a `.turnend` file into the parent home by hand, bypassing
   `orchestrator.Report` / `Recover`, the mailbox envelope, and the `ProcessingAck`
   contract. ADR-0008 §"Terminal Receipt, Wake, Captain relay ... are not public or
   durable alternatives" cut exactly that. Wiring it back re-creates the dual-write the
   hard cut removed, and the repository rule of one live contract and one implementation
   path forbids it.
2. **It delivers nothing the canonical path does not already deliver.** The captain is
   already notified synchronously by `ActivateOnReceipt`; the General is already notified
   by the captain's own mailbox uplink (`ReconcileCaptainHook` → `Recover` →
   `NotifyParentWithTransport`). The relay's only unique effect on the tree today is
   writing the ack that unblocks a local gate — it exists to close a gate, not to carry a
   report.
3. **It restores an asynchronous close for a synchronous failure.** The receipt and the
   obligation are written inside `DeliverWake`, fail-closed. Deferring their close to a
   sweep that runs on some later captain-converge cycle means the soldier's own turn ends
   in an indeterminate state that only another process can resolve. The writer closing its
   own handoff is fail-closed in one place.

### Consequences for the gates

Neither gate is deleted. Both become correct instead of unsatisfiable:

* A pending receipt can now only exist if the process died between `WriteReceipt` and
  `WriteAck`. That is a genuine fail-closed condition and the guard should still refuse.
* The guard's remedy text is corrected: re-running `munsu report` for the task rewrites
  the receipt and its ack, or `--force` overrides. `munsu turnend obligations` is no
  longer advertised as a fix for something it cannot fix.
* `VerifyRetirementContinuity` needs no change. It reads the `ReportRelay` obligation from
  the same home `DeliverWake` now closes it in, so the open obligation is gone by the time
  teardown looks.

## Consequences

`internal/orchestrator/wakedelivery_deliver.go` loses the four functions and two types
above plus `isCaptainHome`; `readCaptainID` stays, still used by the activation path. The
four `.github/deadcode.allow` lines that waived them are removed — the waiver cannot
outlive the function.

A scout soldier under a Captain can now end its turn and be retired without `--force`.
This is the first configuration in which those two gates were ever satisfiable for a
captain-backed soldier.

**Not fixed here, and still standing on #417's false premise** (BEO-112 §5 audit, filed
rather than patched so this PR keeps one subject):

* `.agents/skills/captain-provisioning/REFERENCE.md:254` and its mirror
  `internal/cli/skills/captain-provisioning/REFERENCE.md:254` still document a
  "Terminal-reconcile" step 10 of captain recovery. That step (`stepTerminalReconcile`)
  was deleted by #417 and does not exist.
* `.agents/skills/munsu-ops/SKILL.md:92`, `.agents/skills/munsu-ops/COMMANDS.md:71` and
  their `internal/cli/skills/...` mirrors still claim `munsu captain converge` reconciles
  "terminal receipts". Since #417 it reconciles mailbox pending records, continuity,
  nudges and inherited config — terminal receipts are not among them, and after this ADR
  there is nothing there to reconcile.
* `internal/home/dispatch_degraded.go:50` lists "terminal receipt relay" as an example of
  reconciliation work in a comment. Stale by the same cut.

ADR-0008 is **not** overruled. Its claim that "terminal receipts ... are deleted" was
executed only in part: the reconciliation and the relay compatibility went, the receipt
write and its pane-activation consumer stayed. This ADR records that split as the intended
resting state — receipt as notification, no receipt as transport.
