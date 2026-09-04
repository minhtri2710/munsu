# 0005. Runtime Bindings, Supervision, Recovery, and Mutation Fencing

* **Status:** Accepted; partially implemented — immutable Endpoint/Worktree bindings, typed 7-state observation (ADR-0021), per-home watcher lease + degraded mode, `.soldier-brief.md`+manifest, and Captain-side recovery (relaunch/nudge/relaunch-guard) all landed. §3 (Captain-scoped recovery is the accepted boundary) and §4 (recovery-series/circuit + LaunchDiagnostic) are retired by ADR-0023, and §5 (the General→Captain watcher-lease relay) is superseded by the running status-signal model (2026-09-04). §6 (Git-fencing) is ratified as the running flat allowlist — its six-tier ladder, CAS lease, and force-with-lease exception, none ever wired into the fence, are retired under ADR-0023 (2026-09-04); the fence is enforced by a spawn-provisioned `git` shim on the launch PATH (2026-09-04), so the shell-wrapper residual is closed; the remaining residuals are unlisted mutating verbs, absolute-path git, and agent PATH reset — a security boundary recorded in §6 itself, not tracked repair work.
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

### 5. Per-home watcher topology (General→Captain relay superseded by the running status-signal model, 2026-09-04)

There is one watcher per authoritative home. The General watcher mutates only General-owned lifecycle; each Captain watcher mutates that Captain's tasks, Soldier receipts, provider polling, and Captain-to-General relay obligations.

The original "General observes typed Captain `WatcherLease` records and requests recovery through the Captain control plane" relay was never built and is superseded here (ADR-0023 retire-unbuilt / ratify-running pattern) by the status-signal model the code runs. The General does not read a Captain's `WatcherLease`: `WatcherLease` (`internal/home/watcher_lease.go`) is a purely intra-home single-writer lock. Same-home lease lifecycle fencing reads it through `ClaimWatcherLease` and `ReleaseWatcherLeaseIfMatches`, while lease health is consumed only by that home's own dispatch gate (`IsWatcherLeaseHealthy`); it is never read across homes. The General health-observes each Captain as typed status: `checkAliveWithProbe` (`internal/fleet/captain_captain.go`) verifies Captain-home identity (canonical path plus `.munsu-captain-home` provenance marker, `ProbeLiveness`) and derives endpoint state from its own projection task-meta plus pane/agent probing; it reads no Captain `WatcherLease`, watcher-beat, or Task-lifecycle state and mutates nothing. Watcher status is observed separately by converge and by `RecoverTransaction`'s watcher-ensure step. This preserves ADR-0008 §4's single-writer watcher ownership and typed-health observation; §4 ownership is role/authority ownership, not filesystem isolation. The separate fleet-owned `RecoverTransaction` recovery workflow intentionally reads and mutates Captain-home state under Fleet authority. The running model therefore supersedes ADR-0008 §4's unbuilt "canonical control/Uplink interface" recovery clause with a multiple-entry-point contract. Running recovery has multiple entry points: General-scoped `RecoverTransaction` (`internal/fleet/captain_recover.go`), the CLI/session `fleet.Recover` sweep, and converge's strict-dead-only relaunch path; none is described here as the sole canonical recovery boundary.

Watcher-health readiness is a Captain's own intra-home self-gate, not a General observation: handoff/start/spawn are gated by the acting home on its own lease-and-beat health (`CheckWatcherHealthForDispatch`, `internal/home/dispatch_degraded.go`). If its own watcher is unhealthy the Captain enters capability-specific degraded dispatch:

* Block new ownership, start/claim, spawn, and new mode transitions that require watcher obligations.
* Allow diagnostics, watcher repair, durable receipt reconciliation, already-authorized delivery verification/merge, evidence-preserving teardown, pause/hold, and config/integration repair.

Existing Soldiers continue running undisturbed; the gate blocks only new dispatch (there is no `supervision=degraded` Soldier state). After watcher recovery, pending receipts drain and projections converge before dispatch reopens. Watcher startup failure is an AXI error, never success with `state=failed` nested in data.

### 6. Task-bound Git mutation fencing

Managed task sessions route Git resolved through the launch `PATH` through a spawn-provisioned `git` shim. The soldier and captain launch scripts prepend a munsu-owned shim directory (`GitShimBinRelPath`, `<home>/state/shim/bin`) holding a `git` shim that re-invokes `munsu git-guard`, which evaluates the argv through the same fence core the string path enforces (`evaluateGitArgvSafety` in `internal/cli/git_worktree_safety.go`) and, when allowed, hands off to the real git. Soldier mutations are checked against the immutable Worktree Binding; Captain sessions have no worktree binding, so harness-issued mutating Git commands fail closed. The shim is harness-independent, lives under the home's untracked `state/` tree (never in a worktree, so no manifest entry), and is provisioned fail-closed — a home where the shim cannot be written aborts the launch. munsu's own Git runs unfenced because the root command strips the shim directory from its PATH before any repository mechanics (`stripShimDirFromPath`, matched on the shim path suffix so the strip never depends on the agent's `MUNSU_HOME`); munsu is the authority. Read-only operations remain available. Mutating operations verify target path (including `git -C`, `--git-dir`, and `--work-tree`), Git directory, common repository identity, current task/lease generation, branch/ref pre-state, and non-primary-checkout status. Git invoked by absolute path is outside this PATH-based entry point and remains a stated residual below.

The ADR-0014 file-write safety hook remains available as an opt-in integration through `munsu integrate`; it is not installed during spawn and is separate from this Git fence core.

The default and only Ship authority is a flat allowlist, enforced in `internal/cli/git_worktree_safety.go`. On a mutating command the fence fails closed unless an active task worktree binding exists, the target resolves to exactly the bound worktree (the primary checkout, a different repository, and a mismatched `--git-dir`/`--work-tree`/common-dir are all refused — so an absolute path buys nothing), and the worktree is on the task-local branch `mu/<task>`. It then permits only:

* `add` and `commit` (any form — `commit --amend` is not separately restricted, since the six-tier `history-rewrite` gate was never built);
* creating the task-local branch with the exact direct form `git branch mu/<task>`, or with `checkout`/`switch` using a recognized `-b`/`-B`/`-c`/`-C` creation flag naming `mu/<task>`; every other `git branch` mutation form, including unlisted benign flags and delete/move/copy/force forms, fails closed;
* a normal `push` to `origin` of exactly one task ref (`mu/<task>`, `HEAD`, `HEAD:mu/<task>`, or `HEAD:refs/heads/mu/<task>`), with only the allowlisted flags `-u`, `--set-upstream`, `-q`, `--quiet`, `-v`, `--verbose`, and `--porcelain`; every other flag, refspec, or argument arrangement fails closed.

Every other operation — `reset`, `clean`, `restore`, `rm`, `merge`, `rebase`, `cherry-pick`, `revert`, `worktree`, `tag`, branch/ref deletion, rename or copy, force push, `push --delete`, and pushing any other ref — is unconditionally denied. There is no tiered authority ladder, no elevated capability, no compare-and-swap / expected-HEAD lease, and no `--force-with-lease` authorization: the earlier six-tier design (`read` / `worktree-mutation` / `history-rewrite` / `destructive-clean` / `remote-push` / `force-push`) and its narrowly-bound force-with-lease exception were never wired into the fence, and are retired here under ADR-0023 in favor of the running flat allowlist. Internal munsu Git mutation likewise uses explicit scoped paths rather than ambient cwd.

The shim is an operational safeguard against accidental primary-checkout, wrong-worktree, and casual force/delete mutation — not a security sandbox. Because it is a real `git` on PATH, the guard receives the same argv any invocation would after the shell has word-split it, so a Git invocation wrapped in another program (`sh -c "git push --force …"`, a script, or an alias — anything the shell resolves to `git`) is now fenced too; that residual, open when the fence was a harness hook, is closed. Residuals that remain: an unlisted mutating verb (for example `stash`, `config`, `update-ref`, `symbolic-ref`) is outside the recognized set and passes; a Git invoked by absolute path (`/usr/bin/git`) never consults PATH and bypasses the shim; and an agent that resets its own PATH removes the shim directory. Closing those classes is out of scope for the ratified allowlist and would require argv interception below PATH resolution. The default delivery path is unaffected either way: the no-mistakes gate runs its Git in a launchd/systemd daemon at PPID 1 with the login PATH, which carries no shim directory, so gate pushes never reach the fence.

Proof is intentionally bounded: evaluator denials are unit-tested directly, and shim content, launch-PATH prepending, fail-closed provisioning, and the no-recursion handoff to a real Git are tested. The full shell → provisioned shim → `git-guard` → real-Git chain against a real munsu binary is not exercised end to end.

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
