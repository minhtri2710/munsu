# 0005. Runtime Bindings, Supervision, Recovery, and Mutation Fencing

* **Status:** Accepted; partially implemented — immutable Endpoint/Worktree bindings, typed 7-state observation (ADR-0021), per-home watcher lease + degraded mode, `.soldier-brief.md`+manifest, and Captain-side recovery (relaunch/nudge/relaunch-guard) all landed. §3 (Captain-scoped recovery is the accepted boundary) and §4 (recovery-series/circuit + LaunchDiagnostic) are retired by ADR-0023. Remaining residual work (§5 General→Captain relay, §6 Git-fencing tier decision) is tracked in the [owner-clean residual roadmap](../plans/2026-09-03-owner-clean-residual-roadmap.md).
* **Date:** 2026-07-30
* **Extends:** ADR-0002 (Resource Lease, quarantine, durable lifecycle)
* **Triggered by:** `munsu-workflow-incident-report-2026-07-30.md`

## Context

The incident showed Herdr-backed Soldiers reported dead while still running, Captain recovery repeatedly created panes after one deterministic startup failure, launch stderr disappeared with stale panes, and a Captain accidentally committed and reset the primary checkout while intending to patch a Soldier worktree. Generated `.soldier-md` also blocked ordinary teardown despite being runtime-owned.

Current liveness collapses pane and agent observations into one boolean, mutable task metadata acts as endpoint identity, and Git commands execute through ambient process state. These interfaces cannot distinguish absence, unresponsiveness, probe failure, stale identity, or wrong worktree ownership.

## Decision

### 1. Immutable runtime bindings

Successful spawn persists immutable generation-bound bindings before the task becomes working.

```go
type EndpointBinding struct {
    TaskID, TaskGeneration string
    Backend, Handle        string
    SessionOwner           string
    WorkspaceID, TabID     string
    LeaseID                string
    BoundAt                time.Time
}

type WorktreeBinding struct {
    TaskID         string
    TaskGeneration uint64
    LeaseID        string
    RepositoryID   string
    WorktreePath   string
    GitDir         string
    CommonDir      string
    InitialHead    string
    BoundAt        time.Time
}
```

Mutable `.meta` fields are compatibility projections, not authority. Probes use the stored Endpoint Binding directly and never re-select or fall back to another backend. Retirement releases the exact lease and never disposes an arbitrary matching handle. Recycled endpoints/worktrees receive successor bindings.

### 2. Typed endpoint observation

Liveness is a typed observation, not a boolean. Canonical states are:

* `alive` — endpoint identity matches and the expected agent is alive.
* `starting` — endpoint exists and remains within startup grace.
* `unresponsive` — endpoint exists after grace but the agent is absent or non-responsive.
* `dead` — the backend authoritatively proves endpoint absence.
* `unknown` — query failed or timed out.
* `stale-identity` — a handle exists but lease/session/workspace identity differs.
* `unresolved` — binding is incomplete, unsupported, or not yet migrated.

`dead` is reserved for authoritative absence. Probe errors never become dead. Watcher, snapshot, Soldier state, Captain recovery, and teardown consume the same `EndpointObservation` contract and evidence.

### 3. State-specific recovery

Recovery is bounded and state-specific:

* `alive`: no action.
* `starting`: read-only probe during grace.
* `unresponsive`: bounded non-destructive nudge, then operator attention; never launch a duplicate automatically.
* `dead`: verify absence, then bounded relaunch using a successor endpoint binding.
* `unknown`: read-only retry only.
* `unresolved`: validate/migrate, then quarantine if unresolved.
* `stale-identity`: read-only lease reconciliation, then quarantine; no blind disposal or relaunch.

Probe, nudge, and mutation budgets are separate and durable. Concurrent recovery uses a lock/CAS. Teardown may dispose an exact unresponsive binding after retirement authorization, skips disposal for verified dead endpoints, and refuses stale-identity disposal.

### 4. Durable recovery series and diagnostics (retired — ADR-0023)

Recovery attempts persist across watcher, converge, and manual invocations. A series is keyed by target/endpoint generation, launch-input digest, and normalized failure signature. The launch-input digest includes adapter/version, launch template, model/effort, integration and resolved-config digests, charter/prompt digest, executable identity, capability attestation, and backend.

The first attempt may run immediately, the second after backoff, and repeated identical deterministic failure opens a circuit. Changed launch inputs create a new series. Success requires stable `alive` during the observation window, not pane creation. Failed partial endpoints are disposed through exact bindings before another attempt; cleanup failure blocks retry.

Each attempt references a bounded structured `LaunchDiagnostic`: redacted stdout/stderr tails, safe command descriptor, executable identity, timing, exit/signal, endpoint evidence, truncation marker, and failure signature. Raw environment and raw sensitive command values are not stored. Redaction occurs before owner-only atomic write; redaction failure stores metadata/signature only. Pane cleanup waits until evidence is durable or explicitly waived. Retention preserves evidence referenced by unresolved attention while bounding unreferenced history.

### 5. Per-home watcher topology

There is one watcher per authoritative home. The General watcher mutates only General-owned lifecycle; each Captain watcher mutates that Captain's tasks, Soldier receipts, provider polling, and Captain-to-General relay obligations. The General observes typed Captain `WatcherLease` records and requests recovery through the Captain control plane rather than directly processing Captain task state.

A Captain with active ownership is operationally ready only when its watcher lease is healthy. Handoff/start/spawn require watcher health. If the watcher is unhealthy, the Captain enters capability-specific degraded mode:

* Block new ownership, start/claim, spawn, and new mode transitions that require watcher obligations.
* Allow diagnostics, watcher repair, durable receipt reconciliation, already-authorized delivery verification/merge, evidence-preserving teardown, pause/hold, and config/integration repair.

Existing Soldiers continue running with `supervision=degraded`. After watcher recovery, pending receipts drain and projections converge before dispatch reopens. Watcher startup failure is an AXI error, never success with `state=failed` nested in data.

### 6. Task-bound Git mutation fencing

Managed task sessions prepend a Git wrapper bound to the immutable Worktree Binding. Read-only operations remain available. Mutating operations verify target path (including `git -C`), Git directory, common repository identity, current task/lease generation, branch/ref pre-state, and non-primary-checkout status.

Git authority is tiered:

* `read`
* `worktree-mutation`
* `history-rewrite`
* `destructive-clean`
* `remote-push`
* `force-push`

A default Ship Soldier may create/switch its task branch, add, commit, and perform a normal push only to the bound task ref when its Delivery Plan requires it. Reset-hard, clean, broad restore, branch/tag deletion, worktree mutation, repository/global config mutation, force push, and pushing another ref require stronger authority or remain forbidden. Elevated capability binds Task Generation, expected HEAD/ref SHA, exact remote/ref, and durable authorization; mutations use CAS. Plain `--force` is denied; narrowly bound `--force-with-lease` may be authorized.

The wrapper is an operational safeguard, not a security sandbox; absolute-path Git execution remains a possible bypass. Internal munsu Git mutation also uses explicit scoped capabilities rather than ambient cwd.

### 7. Runtime artifact ownership manifest

The canonical Soldier brief artifact is `.soldier-brief.md`; spawn stops creating legacy `.soldier-md`. A versioned launch-envelope manifest lists every lifecycle-owned artifact with a canonical slash-format relative path, digest, and disposable policy. Teardown ignores/removes only untracked artifacts whose digest matches the manifest. Modified, tracked, or unlisted files remain genuine dirt and block retirement.

The envelope itself is owned by canonical path plus valid schema and identity binding because it cannot hash itself. Harness adapters may contribute verified manifest entries; teardown does not grow an independent filename allowlist.

## Consequences

* Backend-specific liveness becomes accurate and diagnosable without leaking backend details to operators.
* Recovery cannot create unbounded duplicate panes after deterministic failures.
* Watcher failure blocks only unsafe new dispatch, not repair or evidence preservation.
* Task sessions gain strong protection against mutating the primary checkout or wrong ref.
* Runtime artifacts no longer block teardown merely because a writer and cleanup allowlist drifted.
* Spawn, watcher, recovery, snapshot, teardown, and Git execution require coordinated interface changes and migration of mutable meta authority.

## Rejected Alternatives

* Boolean liveness with better Herdr probing: still overloads dead, unknown, and unresponsive.
* Reconstructing endpoint identity from current config or process discovery: can attach to the wrong runtime.
* Relaunching every non-alive endpoint: creates duplicate workers and split brain.
* Retry budgets scoped to one invocation or Captain ID alone: either loop forever or remain blocked after a real fix.
* Persisting raw unbounded terminal logs: unsafe and operationally unbounded.
* One central General watcher or duplicate hybrid watchers: violates ownership locality or creates mutation races.
* Prompt-only worktree safety: does not prevent operator or agent mistakes.
* Filename-only or prefix-wide artifact allowlists: drift-prone or unsafe.
