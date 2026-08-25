# munsu architecture

munsu is a standalone Go CLI for soldier orchestration capabilities, usable from
any project directory without requiring a specific checkout.

## Module layout

```text
cmd/munsu/main.go       Entrypoint — Cobra command execution
internal/
  cli/                  Composition root, Cobra commands, self-update, stow, AGENTS.md helpers
  fleet/                Captain, spawn, delivery, soldier state, project registry
  orchestrator/         Watcher, AFK, wake delivery, lifecycle and turn-end coordination
  home/                 Canonical home resolution plus domain-neutral durable mechanics (identity, journaled commit, locks, leases)
  taskauthority/        Canonical Task documents: lifecycle, dispatch, binding, delivery and transfer rules
  backend/              Session adapters, endpoint observation, worktree and home-tag mechanics
  domain/               Typed identities, operations/preconditions and shared business rules
  harness/              Harness detection, verified adapters and dispatch profiles
  bootstrap/            Toolchain diagnostics and native harness integration
  config/               Typed settings, Project Overlays and resolved Config Snapshots
  testutil/             Shared test helpers
```

Each module follows a deep-module pattern: a small interface hides implementation
detail. `internal/cli` is the composition root; business lifecycle rules remain
in their authoritative module rather than in command wiring.

## Module responsibilities

| Module | Responsibility |
|---|---|
| `cli` | Wire Cobra commands to modules; own CLI-local self-update, stow, AGENTS.md and output helpers |
| `fleet` | Orchestrate Captain lifecycle, Soldier spawn/state, Task Authority composition, project registry and delivery operations |
| `orchestrator` | Coordinate supervision, AFK, wakes, turn-end obligations and cross-process lifecycle |
| `home` | Resolve the munsu home and own domain-neutral durable mechanics: verified identity/roots, containment, scoped fenced locks and leases, atomic journaled change-set commits; retains generic `.meta`/`.status` primitives and the durable mailbox |
| `taskauthority` | Own canonical Task documents — Aggregate, Generation/Revision, lifecycle, Dispatch Holds, delivery authorization/outcomes, transfer reservations, launch and retirement evidence — as named operations on one `Canonical` surface (ADR-0008 §2) |
| `backend` | Provide tmux/herdr/zellij/cmux/orca adapters, endpoint observation, worktree and home-tag mechanics |
| `domain` | Own pure business rules and value types such as `PR.CanMerge` and `Review.IsApproving` |
| `harness` | Detect and verify harnesses; resolve launch templates and dispatch profiles |
| `bootstrap` | Diagnose toolchain readiness and install, repair or inspect native harness integration |
| `config` | Own typed settings, defaults, validation, Project Overlays and immutable resolved Config Snapshots (ADR-0008 §6) |

The command-to-module mapping is maintained in
[`docs/port-mapping.md`](port-mapping.md).

## Home directory data model

Default home: `~/.munsu` (overridable via `MUNSU_HOME` or `--home`).

```text
~/.munsu/
  state/                   Task state, canonical Task Authority documents, locks, wakes and receipts
    task-authority/        Canonical Task Authority documents (tasks/, holds/, receipts/)
    <durable-stem>.meta    Task metadata projection (logical IDs are encoded per platform)
    <durable-stem>.status  Append-only status/event projection (logical IDs are decoded at read boundaries)
  data/
    projects.md            Project registry
    captains.md            Captain registry
    <id>/brief.md          Per-task brief
  config/                  Harness, backend, dispatch and project settings
  projects/                Project worktrees
  captains/                Captain homes
```

### Task state

Authoritative task state — the Task Aggregate (`munsu.task-authority/v1`), its
Generation/Revision, Dispatch Holds, delivery authorization and outcomes,
transfer reservations and retirement evidence — is owned by
`internal/taskauthority` and persisted as Task documents under
`state/task-authority/` (`tasks/<id>/current.json` plus per-generation records,
`holds/`, `receipts/`). Every write goes through `internal/home` durable
mechanics as an atomic journaled change-set under the smallest scoped fenced
lock (ADR-0008 §2, §5); there is no Store interface, in-memory fake, adapter or
projection seam. `.meta` and `.status` remain `internal/home` primitives written
by fleet/CLI projection writers after authoritative commits (ADR-0007 §7); they
are never authoritative and do not decide lifecycle.

Only fresh initialization into an empty home, or operation on a home already
matching the current `v1` schema, is supported. Old development homes carry no
compatibility promise and are discarded externally and initialized again
(ADR-0008 §11); there are no schema migrate, upgrade, or reset commands.

### Configuration

Flat key files live under `config/`. `internal/config` owns typed settings,
defaults, validation, Project Overlays and immutable resolved Config Snapshots
(ADR-0008 §6); `internal/cli` translates process environment and flags into
typed boundary overrides. Dispatch profiles live in `config/soldier-dispatch.json`
and are interpreted by `internal/harness` and the spawn orchestration in
`internal/fleet`.

## Key interfaces and seams

### Session backend (`internal/backend`)

`internal/backend` hides terminal-specific behavior behind session and endpoint
operations. Verified implementations include tmux and herdr; zellij, cmux and
orca are explicit experimental adapters. Resolution rejects unknown backends
rather than silently selecting an unverified implementation.

Endpoint observation (BEO-16/P1a) is the typed, orthogonal runtime diagnostic
of one exact bound endpoint: `Lifecycle` (starting/alive/dead/unknown),
`Responsiveness`, `Freshness`, `Activity`, `Source`, `ObservedAt`, opaque
`Incarnation`, and diagnostic `Detail`. It is NOT Task lifecycle truth — the
canonical Task phase stays in `internal/taskauthority` and a probe never
mutates it. The crossing guards encode the policy invariants: `unknown !=
idle`, `unknown != dead`, `unresponsive != dead`, `starting != dead`, `stale !=
dead`. A backend adapter reports only what it directly observes (lifecycle and
responsiveness) and never fabricates freshness or incarnation: it always
returns `FreshnessUnknown`, so an adapter probe alone is never `Live()`/`Absent()`.
Freshness current-ness and authoritative absence are concluded ONLY by Fleet,
and the two are separate authorities (`fleet.authorizeAbsence` vs
`fleet.authorizeLive`): negative exact absence is granted only for a narrowly
classified structured absence (dead + probe/derived source) of the exact bound
handle revalidated under the current canonical generation/revision/fence;
positive liveness is promoted to `Live()` only WITH explicit acquisition
/creation evidence tying the exact handle to the incarnation (the in-process
creation receipt, the durable `AcquiredEndpoint`, or the canonical
`EndpointBinding` evidence) — P1a adapters cannot attest incarnation, so a
probe of an expected handle with no acquisition record (e.g. a mutable `.meta`
projection) is never promoted and fails closed. An incomplete/stale proof or an
ambiguous reading is demoted to `unknown`/`stale` and fails closed — nothing is
disposed or relaunched on ambiguous state. Retirement closes the probe→dispose
TOCTOU window with BOTH a durable canonical cleanup claim AND an authoritative
re-read/compare-and-fence: the retirement transition commits a durable
`CleanupClaim` (active) atomically with the retired phase, and every
Reopen/BindEndpoint/BindWorktree/acquisition/lifecycle mutation fails closed
while the claim is active — so even though each revalidation fence
(`CurrentLocked`) holds the task-scope lock only for the read, the durable
claim keeps the task pinned across the external backend/filesystem actions
(Dispose, worktree return, the projection multi-remove sequence) that follow
it; a concurrent reopen/rebind between a fence and an action is rejected, not
merely detected. The claim is reconciled by the cleanup continuation
operations: `CompleteCleanup` (all evidence-pinned releases + projection
removal succeeded — the task becomes reopenable), `AbortCleanup` (operator
escape hatch — the task becomes reopenable without cleanup; abort is
TERMINAL, so a later teardown retry never re-activates or resumes the aborted
cleanup against a reopened generation), and the idempotent `BeginCleanup`
assert (crash resume only), which is exact-generation/phase/evidence fenced:
it accepts only a current aggregate retired at exactly the claimed generation
with preserved retirement evidence matching the claim identity, and every
Begin/Complete/Abort path is identity-fenced even on completed/aborted
idempotency paths (a foreign continuation is never a no-op and never
overwrites the stored claim). Delivery mutations (`AuthorizeDelivery`/
`RevokeDeliveryAuthorization`/`CommitDeliveryOutcome`) are gated by the
active claim too, so the revision snapshot cleanup revalidates against cannot
move. The fences additionally fail
`cleanup-pending` without releasing anything when the claim is missing/
foreign/reconciled, when generation/revision advanced, when a reopen owns any
evidence-pinned identity, or when the preserved retirement evidence changed.
`Backend.Alive` (the
former boolean liveness surface) is fully removed, and the typed
`EndpointObservation.Alive()` compatibility helper is deleted as well: every
session adapter exposes a structured probe (`CheckAlive`/`CheckAgentAlive`);
the opaque launch incarnation is minted by Fleet and persisted in the
`LaunchIntent`/binding before acquisition. Adapter capability is enforced
structurally, never from a declared table: reservation-aware find-or-create is
an optional interface the spawn path type-asserts on
(`internal/cli/spawn_endpoints.go`), so a backend that does not implement
`FindOrCreateWindow` fails closed before acquisition; construction health is
verified on the one private path (`constructBackend`); and probe granularity is
whatever the adapter's own structured probe proves. The former static
descriptor table (`backend.Capabilities` / `CapabilityMatrix`) was removed in
BEO-67: nothing consulted it, so it could only ever drift away from the
adapters it claimed to describe.

### Delivery acceptance (`internal/domain`)

`internal/domain/domain.go` is the single owner of `PR`, `Review`, `CheckRun`,
`PR.CanMerge`, and `Review.IsApproving`. `internal/fleet/delivery_*.go` owns
provider interaction, identity capture and delivery orchestration;
delivery-invariant and git-authorization operations execute as
`internal/taskauthority` named operations.

### Task Authority (`internal/taskauthority`)

`internal/taskauthority` is the deep module owning the canonical Task documents
— Aggregate, Generation/Revision, lifecycle phases, Dispatch Holds, delivery
authorization and outcomes, transfer reservations, and launch/retirement
evidence — exposed as named semantic operations on one `Canonical` surface
(ADR-0008 §2, extending ADR-0007). Every mutation takes a `domain.Operation`
(stable Operation ID + typed intent digest) and a `domain.Precondition`
(expected Home/Task identity, generation/revision) and commits as an atomic
journaled change-set through `internal/home` durable mechanics under the
smallest scoped fenced lock; replayed operations with the same digest are
idempotent, and changed digests or stale preconditions fail closed as typed
conflicts. There is no Store interface, adapter, projection layer, or migration
seam. `internal/fleet` and `internal/cli` consume the Authority and retain
orchestration (Task Transfer is a Fleet-owned journaled operation over two
local Authority surfaces); `internal/home` no longer owns task lifecycle,
dispatch or binding authority.

### Current-state read path (single query)

`soldier-state`, `fleet snapshot`, and `guard` read task state through one
canonical-first query so agents and the CLI receive the same Task truth.
`internal/fleet.ReadWithProbe` is the shared soldier current-state query: it
resolves the authoritative `taskauthority` Aggregate first (Task 7.8) and the
canonical phase is the only lifecycle authority (clean break). `fleet snapshot`
receives the current-state query as an explicit `fleet.SnapshotDependencies`
dependency (`fleet.NewCanonicalCurrentState()`); the contract row derives its
status from `fleet.PhaseFromProjection`; `guard` consumes `fleet.Snapshot` for
its in-flight count and fails closed when Task truth is unreadable. Guard
invocation semantics: the pre-run middleware (`guardWarnWatcher`) is
**advisory** — it surfaces unreadable Task truth or a watcher warning to stderr
but never blocks an arbitrary command; the structured `munsu guard` command and
the harness stop-hook guards (agy/claude/codex/opencode/grok) are **blocking
enforcement** and return a structured `invalid_state` error or exit code 2 on
unreadable Task truth. A
canonical/current-state failure fails closed instead of silently projecting the
`.status` tail; a task-facing `.meta` without a canonical record is rejected
(legacy/meta-only tasks are not authoritative), while captain metadata
(kind=captain) is exempt. Endpoint/pane probing is diagnostic only and never
changes lifecycle state. Lifecycle, delivery, worktree/endpoint binding, and
handoff mutations all go through `internal/taskauthority`; every remaining
`.meta`/`.status` write is a post-commit projection, a runtime
(transport/ready/mailbox) marker, or captain metadata (ADR-0007 §7).

### Captain lifecycle (`internal/fleet`)

`internal/fleet/captain_captain.go` owns seed, launch, retire, handoff,
config-push, update and worktree migration. `captain_recover.go` owns the recovery
transaction. CLI adapters in `internal/cli` compose verified harness, backend and
integration capabilities into those operations.

### Supervision and wake delivery (`internal/orchestrator`)

`internal/orchestrator` owns watcher and AFK coordination plus the wake-delivery
module. `DeliverWake` handles Soldier-side status, receipt, event and wake
coordination, and closes its own acknowledgement and report-relay obligation
(ADR-0015) — there is no Captain-side relay of terminal receipts; the receipt is
a notification artifact for the captain pane nudge, not a transport. Uplink to a
parent goes through the mailbox path (`Report` / `Recover`) alone.
Durable filesystem primitives remain behind `internal/home`
and adjacent orchestrator lifecycle files.

## Task lifecycle

| Phase | Command | Authoritative module |
|---|---|---|
| Create / lifecycle | `munsu task add/start/done/block/unblock/reopen` | `internal/taskauthority`, `internal/fleet` |
| Brief | `munsu brief` | `internal/fleet` |
| Spawn | `munsu spawn` | `internal/fleet`, composed by `internal/cli` |
| Supervise | `munsu watch`, `munsu watch-arm`, `munsu afk` | `internal/orchestrator` |
| Interact | `munsu send`, `munsu peek`, `munsu soldier-state` | `internal/cli`, `internal/fleet`, `internal/home` |
| Deliver | `munsu delivery ...` | `internal/fleet` (orchestration), `internal/taskauthority` (invariant ops), acceptance rules in `internal/domain` |
| Teardown | `munsu teardown` | `internal/cli`, `internal/fleet`, `internal/orchestrator` |

## Rank hierarchy and identity

General orchestrates the fleet. A Captain is a persistent domain supervisor
implemented by `internal/fleet`; a Soldier executes one bounded task. Runtime
identity is declared through `MUNSU_ROLE=general|captain|soldier`. Captain labels
use `captain-<id>-<hometag>`, windows use `mu-captain-<id>`, provenance is stored
in `.munsu-captain-home`, and registration lives in `data/captains.md`.

Mailbox receiver identity and rank are derived from durable local provenance, not
from caller-provided values. General and Captain receivers use their home
provenance; a Soldier receiver uses the durable task record in its dispatcher's
home because a Soldier has no separate home. `Receive` and creation of a new ack
check the one-hop rank transition against the sender rank proven by the
receiver's locally readable provenance. The envelope's `SenderRank` is only a
claim that selects the provenance proof obligation; it is never the authority
for the transition. Missing, mismatched, or ambiguous provenance fails closed.
A replay of an already persisted accepted ack still validates that ack and its
exact envelope match, but does not require current sender provenance.

## Architectural enforcement

`internal/testutil/architecture_policy_test.go` enforces the allowed package topology
and forbidden import directions. When adding or moving a module, update that gate
in the same change and run:

```sh
go build ./...
go vet ./...
go test ./...
```
