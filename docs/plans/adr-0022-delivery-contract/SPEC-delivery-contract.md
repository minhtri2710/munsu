# Spec: durable-delivery-contract

ADR: 0022 (Durable Per-Task Delivery Contract) · gap G5 · single capability (no map)

## Objective

Fix a task's delivery mode (no-mistakes / direct-PR / local-only) to the task
**durably**, so it cannot silently change across re-spawns. Before D1,
`fleet.ResolveDeliveryMode` re-resolved the mode fresh every generation from
`--mode` + registry default + auto-detect, and projected the result only into
ephemeral home meta (`meta["mode"]`) — never onto the canonical task. D1 records
the first-spawn resolution on the canonical task and reads it back on later
spawns; D2 keeps munsu's authorized mid-spawn fallback (no-mistakes → direct-PR
via `fleet.preflightNoMistakes` and late capability loss) but reconciles it into
that contract as a recorded transition.

Decision (Middle path): record the resolved mode on the canonical task the first
time it is resolved; later generations read the recorded contract; keep the
fallback but as an **explicitly recorded transition**, not a silent
re-resolution.

Success = a task's mode is stable across re-spawns unless an explicit `--mode`
re-scaffold or a recorded fallback changes it; the contract always states the
mode in force and how it got there.

## Tech Stack

Go (module `munsu`), stdlib + cobra. Durable truth lives in
`internal/taskauthority` (ADR-0008); no new storage tech (ADR-0019).

## Commands

```sh
go build ./...
go vet ./...
go test ./internal/fleet/... ./internal/taskauthority/...
go test ./...
gofmt -l .
```

## Project Structure

```
internal/fleet/spawn_config_snapshot.go  → ResolveDeliveryMode call site (first-spawn resolution)
internal/fleet/delivery_preflight.go     → preflightNoMistakes (authorized fallback path)
internal/fleet/brief.go                  → ScaffoldOptions.Mode IS the delivery mode (shipBriefTemplate renders "Delivery mode: %s"); no rename needed — keep projecting the resolved mode into the brief
internal/taskauthority/                   → canonical task aggregate: new durable delivery-contract field + a recorded-transition op
internal/fleet/delivery_attestation.go    → existing ephemeral CapabilityAttestation{RequestedMode,EffectiveMode,FallbackReason,FallbackPolicy} + HandleLateCapabilityLoss (fleet runtime record, NOT canonical; D1/D2 must relate, not parallel)
```

## Code Style

Match `taskauthority` operation style: every mutation is a typed
`domain.Operation` with a `domain.Precondition`, committed under the scoped
fenced lock. The fallback is such an operation, recording from/to/reason/generation —
not a re-resolution:

```go
// First spawn: resolve once, record on the canonical task; later generations
// read the contract instead of re-resolving.
Canonical.RecordDeliveryContract(op, CanonicalRecordDeliveryContractRequest{…Mode})

// Fallback is a recorded transition, not a silent re-resolve: it moves the
// contract's Mode to the mode in force and stamps DeliveryFallback with
// from/to/reason/generation plus the recording operation and timestamp.
Canonical.RecordDeliveryFallback(op, CanonicalRecordDeliveryFallbackRequest{…From, To, Reason})
```

## Testing Strategy

Go `testing`, table-driven, tests beside code. Cover:
- first spawn records the resolved mode; a second generation reads the recorded
  contract instead of re-resolving (registry change between spawns does **not**
  silently flip it).
- authorized no-mistakes → direct-PR fallback mutates the contract via the
  recorded-transition op (from/to/reason/generation captured).
- `--mode` re-scaffold is the explicit way to change a recorded contract.
- `+yolo` still does not relax `RequireNoMistakes` (preserve
  `TestYoloDoesNotRelaxRequireNoMistakes`).
- meta `mode` is now a projection of the durable contract, not its source.

Guard-coverage: the "registry cannot silently override a recorded contract"
refusal and the recorded-fallback branch must each be entered by a test.

## Boundaries

- **Always:** record the contract durably on the canonical task via a
  task-authority op; keep no-mistakes gate semantics (ADR-0016).
- **Ask first:** changing delivery-mode value set; changing the fallback trigger
  conditions.
- **Never:** adopt firstmate's registry-advisory-only / hard refuse-to-guess
  (munsu keeps auto-detect first-spawn default); let mode live only in ephemeral
  meta; produce a divergent fresh resolution per spawn.

## Success Criteria

1. Canonical task carries a durable delivery contract; second-generation spawns
   read it, not a fresh resolution.
2. Authorized fallback is recorded as a transition (from/to/reason/generation)
   in the same op that moves the contract's mode, so the contract never states a
   mode without stating how it got there. Only the no-mistakes → direct-PR
   direction is accepted; a divergence with no fallback reason, or a recording
   that fails, aborts the launch instead of delivering under an unrecorded mode.
3. Registry feeds only the first resolution; it cannot silently override a
   recorded contract.
4. Misleading `ScaffoldOptions.Mode` struct comment and `brief_test.go` fixtures corrected to real delivery-mode values (no rename needed).
5. `go test ./...`, `go vet`, `gofmt -l .` clean; deadcode and guards green.

## Resolved Questions

- Field home + op names: resolved as `Aggregate.DeliveryContract` (a
  `DeliveryContract` pointer carrying an optional `DeliveryFallback`), written by
  `Canonical.RecordDeliveryContract` and `Canonical.RecordDeliveryFallback` —
  the illustrative names held.
- `local-only` participates in the contract like the other two modes (D1). It is
  not a fallback *target*: only no-mistakes → direct-PR is an authorized
  transition (D2).
- Relationship to `CapabilityAttestation`: resolved as FEED FROM, not subsume.
  The attestation stays the ephemeral capability snapshot behind the late
  capability-loss fallback; the effective mode and accumulated fallback reason
  the fleet runtime leaves on the launch are what `reconcileDeliveryFallback`
  reconciles into the canonical contract, which is the single durable record of
  the transition.
