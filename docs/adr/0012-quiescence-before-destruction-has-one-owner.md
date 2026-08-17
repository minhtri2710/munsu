# 0012. Quiescence Before a Destructive Operation Has One Owner: Task Authority

* **Status:** Accepted
* **Date:** 2026-08-17
* **Extends:** ADR-0008 (one owner and one canonical implementation path per lifecycle)
* **Upholds:** ADR-BEO-40-01 §5 (orphan scanning is report only)
* **Triggered by:** BEO-67 deadcode triage → BEO-102 verdict

## Context

`internal/fleet` carried two implementations of one rule: "may this home's state be
destroyed yet".

**The live one** — `fleet.RetireTask` (`internal/fleet/retirement_task.go:709`, `:775`),
reachable from the binary through `internal/cli/spawn_cmd.go:445`. It builds an
`exactEndpointProof` from the **canonical aggregate of Task Authority**:
`cur.Generation` / `cur.Revision` re-read under the task lock *after* the probe,
`acquired: true` taken from the canonical `EndpointBinding` (a real acquisition
receipt), then compare-and-fence once more immediately before `Dispose`. Ambiguity
fails closed into cleanup pending: nothing disposed, no lease released.

**The dead one** — a cluster of 12 functions across `writer_fence.go`,
`endpoint_fence.go`, `writer_runtime.go`. It called the *same* authority
(`authorizeAbsence` / `authorizeLive` in `internal/fleet/endpoint_observation.go`) but
built its proof from the **`.meta` projection**.

`.github/scripts/deadcode.sh` filed the cluster under `Known open bugs, NOT accepted
debt` on the premise that it computed exactly the quiescence proof
`captain retire --remove-home` needs and that nothing called it. That premise is wrong
in three places.

## Evidence

### 1. `captain retire --remove-home` does not exist

`internal/cli/captain_cmd.go:107` passes the constant `false`:

```go
return fleet.Retire(args[0], ctx.Home, false, retireForce, newSessionRetireEndpoint())
```

The binary confirms it: `munsu captain retire --help` offers only `--force` and
`--home`. `docs/port-mapping.md:98` records `captain retire --remove-home` as a
**deliberately removed** capability — "Deliberate safety choice — home directory
persists by default".

So `os.RemoveAll(captainHome)` in `captain_captain.go` is unreachable from the binary
too; it is reached only from tests. Wiring a fence into that branch would be wiring a
guard into dead code.

### 2. The endpoint invariant already has an owner, and that owner is stronger

`RetireTask` anchors its proof in the canonical aggregate. `fenceEndpoints` anchored
its proof in `.meta`. The two are not equivalent: the `.meta` version has no canonical
access, so it cannot revalidate. A projection is not an authority.

### 3. The OS-process half had the opposite policy decision on record

ADR-BEO-40-01 §5, quoted verbatim in `internal/fleet/orphan_scan.go` and
`internal/cli/doctor_orphans.go`:

> Orphan scanning is REPORT ONLY. […] Automatic cleanup stays out until an explicit
> adoption registry exists, because no passive signal distinguishes a leftover from a
> daemon a member started on purpose.

`FenceOSWriters` did the opposite: `TerminateExact` on every correlated process.
Wiring it into a destructive path would have quietly reversed a decision this repo
settled and shipped in BEO-56.

### 4. The dead half could not be wired up even if we wanted it

`TaskEndpointScanner.ScanEndpoints` built its proof from five `.meta` keys:
`endpoint_lease_id`, `endpoint_fence_token`, `endpoint_incarnation`, `task_generation`,
`task_revision`. None is in `home.ValidMetaFields` (`internal/home/taskmeta.go`), and no
production code writes them: `endpoint_fence_token` / `endpoint_incarnation` exist only
as JSON tags of the canonical Task document (`internal/taskauthority/model.go`), and
`task_generation` / `task_revision` appeared nowhere but that one scanner line.

So in production `proof.authorized()` and `proof.current()` were both false for **every**
endpoint → `authorizeAbsence` demoted → `fenceEndpoints` returned an error for **every**
`.meta` carrying `window` or `herdr_pane_id`. The source comment said as much. That is
the dangerous direction — a false refusal hangs teardown — and it was not a tuning
problem: the only source of proof is Task Authority, and the path that already has
canonical access already implements this correctly.

### 5. History: two different shapes in one cluster

* `FenceWriters` **did** once have a call site: `internal/home/migrate.go:44`
  (`MigrateAndActivate`) at `eb30f30`, lost when `a8bd316` deleted migration. But
  `MigrateAndActivate` was itself never called by any command — the fence died with
  migration because it was built *for* migration.
* `FenceBoundEndpoints` never had a caller at all, not even a test.

### 6. Mutation check

Baseline `go test ./... -count=1`: green.

| Mutant | Location | Result |
|---|---|---|
| M1 | `writer_fence.go` — drop the post-rescan refusal guarding `VerifiedQuiescent = true` | exactly one red, `TestCompositeWriterFenceFailsWhenProcessAppearsAfterRescan`, inside the cluster's own unit file |
| M2 | `endpoint_fence.go` — drop the `if !auth.Absent()` fail-closed | exactly one red, `TestFenceEndpointsRetainsMetadataOnLiveWithoutAcquisitionEvidence`, also inside the cluster |
| M3 | `endpoint_observation.go` — drop the proof-completeness gate of `authorizeAbsence` (**live code**) | red in `TestAuthorizeObservationFailsClosedOnIncompleteProof` (`incarnation_authorize_test.go`), i.e. outside the cluster |
| M3b | `endpoint_observation.go` — drop the same gate in `authorizeLive` (**live code**) | **survived the whole repo suite** before this change |

M1/M2 survive everything except the cluster's own unit tests, which is what supports
deleting them. M3 does have an owner outside the cluster: the first reading, which put
that coverage inside `endpoint_fence_test.go`, came from a mutant that made
`TestFenceEndpointsFailsClosedWithoutCurrentGenerationRevision` panic and abort the test
binary before the rest of the package ran.

M3b was the real hole: the proof-completeness gate of `authorizeLive` — the positive
authority `spawn_runner.go` and `retirement_backend.go` depend on — was pinned by no
test anywhere. It is now covered in `incarnation_authorize_test.go`
(`TestAuthorizeLiveRequiresAcquisitionEvidence`), and M3b is red after this change.

## Decision

**Delete the cluster.** Quiescence before a destructive operation has exactly one owner:
**Task Authority**, through `authorizeAbsence` / `authorizeLive` with the proof anchored
in the canonical aggregate, as `fleet.RetireTask` implements it. `.meta` is a
projection, never an authority, and is never used as a source of proof.

The level of assurance this repo accepts for destructive operations:

* **Task teardown** (`RetireTask`): canonical endpoint authorization plus a TOCTOU
  revalidation. This is the highest level and the reference standard.
* **`captain retire`**: the `inFlightSoldierIDs` artifact scan. `--force` skips that
  level outright. It does not delete the home, so the exposure is limited to endpoint
  teardown.
* **No OS-process-level enforcement anywhere**, deliberately, per ADR-BEO-40-01 §5.

### What was removed

| File | Removed |
|---|---|
| `internal/fleet/endpoint_fence.go` | whole file: `fenceEndpoints`, `validBoundEndpoint`, `sameBoundEndpoint`, `readEndpointMeta`, `metaUint`, `ServiceEndpointController.{Probe,Dispose}BoundEndpoint`, `TaskEndpointScanner.ScanEndpoints`, plus `BoundEndpoint`, `EndpointScanner`, `EndpointService`, `EndpointController`, `ServiceEndpointController`, `TaskEndpointScanner` |
| `internal/fleet/writer_fence.go` | `FenceWriters`, `FenceOSWriters`, `correlateWriters`, `writerPair`; interfaces `EndpointDrainer`, `ExactProcessController`; fields `Endpoints`, `Controller`, `EndpointArtifacts`, `EndpointController` |
| `internal/fleet/writer_runtime.go` | `FenceBoundEndpoints`, `NoEndpointDrainer`, `OSProcessController` (`TerminateExact`, `RetireArtifact`) |
| `internal/fleet/process_runtime_{unix,windows}.go` | `terminateProcess` — orphaned by the removal above; nothing else signals a process |

**Kept** in `CompositeWriterFence`: `Artifacts`, `Processes`, `Verifier`, `Marked`,
`Oracle` — all five feed `InspectOrphans` / `appendWriterEvidence` behind
`munsu doctor --orphans`. The struct is now evidence sources only, with no way to
terminate or dispose anything.

`home.WriterInventory` (`internal/home/writers.go`) dies with this: its only producer
was `FenceOSWriters` and `VerifiedQuiescent` has no reader. `internal/home` is out of
scope for BEO-102 and is handed to BEO-67.

## Consequences

* The repo has exactly one implementation of the quiescence rule. No second, weaker
  copy reading from a different source of truth.
* `Known open bugs, NOT accepted debt` in `.github/deadcode.allow` is empty again; the
  header stays (BEO-96). No line moved to accepted debt, and no new line was added.
* There is still no OS-process-level assurance. That is policy, not an oversight. If it
  is ever wanted, the path is the adoption registry of ADR-BEO-40-01 §5, not a revival
  of this cluster.

## The real gap, tracked elsewhere

The captain-home destruction path that genuinely has no guard is not `retire`, it is:

`munsu captain seed --repo <r> --force <home>` → `seedFromWorktree`
(`captain_seed_worktree.go`) → `removeExistingWorktree` (`captain_captain.go`) →
`git worktree remove --force` / `os.RemoveAll(absHome)`.

Its guards are `isManagedWorktree`, `isStateOnlyHome`, `validateWorktreeRemote`. No
in-flight soldier check, no endpoint probe, no process check — weaker than `Retire`, and
it never calls `inFlightSoldierIDs`. That belongs in `captain_seed_worktree.go` (BEO-67
scope) and the right guard there is `inFlightSoldierIDs` plus the canonical endpoint
authority the live retirement path already owns — not this cluster.
