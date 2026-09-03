# 0013. AFK Is Diagnosis and Manual Action, Not Self-Repair

* **Status:** Accepted
* **Date:** 2026-08-17
* **Extends:** ADR-0008 (one owner and one canonical implementation path)
* **Triggered by:** BEO-67 deadcode triage → BEO-106 verdict

## Context

The AFK daemon detects wedges, triages wakes, and accumulates a durable digest — all of
that is live. The repair action (injecting a nudge into the general pane) was written in
full at phase 2.3/2.4 (#191/#192) but was never armed: `d.injector` is assigned only by
`maybeInitInjector`, callable only from `SetBackend`/`SetPaneCapture`, and no commit in
the history has ever contained a call site for either — `git log --all -G '\.SetBackend\('`
and `-G '\.SetPaneCapture\('` both return zero commits. Not even the tests went through
the setters: `afk_24_test.go:525` constructed `Daemon{injector: NewInjector(...)}` directly.

Four days after #192, PR #318 (`f98b73a`) replaced raw `SendKeys` with the
`backend.SubmitPrompt` seam — a typed prompt submission with `PromptResult.Acknowledged()`
— and moved every live call site onto it. The two remaining raw-`SendKeys` inject paths in
the tree were both AFK's dead ones (`Injector.InjectIfSafe`, `DirectInject`).

Because `d.capture` was also always nil, the target-safety block never ran either: the
`afk: target safety: ...` line never printed, `Digester.SetTargetSafety` was never called,
and `safe_target` / `target_verdict` (both `omitempty`) never appeared in
`state/.afk-digest`. The digester's self-handle branch, gated on that same never-set flag,
was equally unreachable in production.

## Decision

AFK **diagnoses and accumulates**; it **does not repair itself**. The entire AFK
inject/recovery/circuit cluster is deleted. Getting information to the General while they
are away has exactly two owners:

1. the uplink notify path (`resolveReceiverTarget` → `IsSafeInjectTarget` →
   `SubmitPrompt`) for immediate notice, and
2. `munsu afk return` / `return check` for reconciliation when the General is back.

There is no third path, and no raw `SendKeys` writer into a human's pane.

## Consequences

The 13-function cluster, `afk_circuit.go`, `Digester.SetTargetSafety` and the
`safe_target` / `target_verdict` digest fields are gone; ~926 lines of production code and
~2.8k lines of test go with them. The digester's self-handle branch goes too — it was
gated on the safety flag no live path set. This changes the `state/.afk-digest` contract,
but both fields were `omitempty` and were never written in production, so no real data is
lost. Documentation and the embedded skill drop every inject promise.

`RedactSensitive` is removed with the cluster. It was the only redactor in the tree, and ADR-0005 §4 (`LaunchDiagnostic` redaction) was its only planned consumer; §4 is now **retired (won't-build) by ADR-0023**, so no redaction bundle will be built and the red-flag concern is resolved. Deleting `RedactSensitive` removed no live guard — it was dead at runtime — and launch failures surface through the typed errors in `internal/harness`/`internal/fleet`.

ADR-0005 §3 (bounded nudge for `unresponsive` endpoints) is **not** overruled by this ADR.
It speaks the `EndpointObservation` vocabulary of soldier/captain endpoint recovery, while
`ResolveRecovery` keyed off `InjectOutcome` — a third vocabulary nobody called.
ADR-0005 §3 is **closed by ADR-0023** as the accepted Captain-scoped recovery boundary (the bounded nudge is the shipped `stepNudgeRetry`); it does not belong to AFK.

## Alternatives rejected

**Wire the injector up at `session_cmd.go` before `d.Start`.** Rejected because (a) it
would rebuild the only raw-`SendKeys` writer into a human's working session, on a seam the
project deliberately left; (b) it would not be sufficient — `nudgeTracker` and
`ResolveRecovery` would still have no caller, so the ADR-0005 §3 nudge contract (now closed by ADR-0023 as the accepted boundary) still would not be the enforcement path; (c) it would wake ~900 lines that have never run in production, all at
once, pointed at a terminal a person is using.

If the owner still wants AFK digests delivered automatically, that is a **new feature**,
not a bug fix: a fresh arming seam at the composition root, delivery through
`backend.SubmitPrompt`, reuse of `resolveReceiverTarget`, two-way evidence on a real pane,
and a mutation check that removing the safety gate turns the tests red.
