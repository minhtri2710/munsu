# Spec: durable-delivery-contract

ADR: 0022 (Durable Per-Task Delivery Contract) · gap G5 · single capability (no map)

## Objective

Fix a task's delivery mode (no-mistakes / direct-PR / local-only) to the task
**durably**, so it cannot silently change across re-spawns. Today
`fleet.ResolveDeliveryMode` (called at `spawn_config_snapshot.go:76`) re-resolves
the mode fresh every generation from `--mode` + registry default + auto-detect,
and projects the result only into ephemeral home meta (`meta["mode"]`) — never
onto the canonical task. munsu also keeps an authorized mid-spawn fallback
(no-mistakes → direct-PR via `fleet.preflightNoMistakes` and late capability
loss).

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
// First spawn: resolve once, record on the canonical task.
mode := ResolveDeliveryMode(args.Mode, defaultMode, requireNoMistakes)
task.RecordDeliveryContract(mode)          // durable; later generations read this

// Fallback is a recorded transition, not a silent re-resolve:
task.RecordDeliveryFallback(from, to, reason, generation)
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
2. Authorized fallback is recorded as a transition (from/to/reason/generation).
3. Registry feeds only the first resolution; it cannot silently override a
   recorded contract.
4. Misleading `ScaffoldOptions.Mode` struct comment and `brief_test.go` fixtures corrected to real delivery-mode values (no rename needed).
5. `go test ./...`, `go vet`, `gofmt -l .` clean; deadcode and guards green.

## Open Questions

- Field home on the aggregate + exact op names (`RecordDeliveryContract` /
  `RecordDeliveryFallback` are illustrative) — settle in Plan against
  `taskauthority` naming.
- Does `local-only` participate in the contract, or is it spawn-scoped only?
- Does the durable canonical contract SUBSUME the existing
  `CapabilityAttestation` mode fields (RequestedMode/EffectiveMode/FallbackReason)
  or FEED FROM them — decide at D2 planning.
