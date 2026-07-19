# Firstmate parity audit — 2026-07-18

> **Status (resolved):** All confirmed gaps below were closed in PRs
> [#197](https://github.com/minhtri2710/munsu/pull/197)–[#216](https://github.com/minhtri2710/munsu/pull/216)
> (core parity + worktree fix + native Pi/Claude/Grok/Codex/OpenCode adapters).
> The audit text below is the historical point-in-time snapshot.
> Sole remaining open item: agy has no hook surface (tracked in [#206](https://github.com/minhtri2710/munsu/issues/206)).

## Scope and method

This audit compares the authoritative checkouts:

- firstmate `bc1a21b`
- munsu `f80ab60`

The comparison is behavioral rather than command-name based. A difference is a defect only when munsu fails an operational contract that firstmate demonstrates or munsu's own documentation promises. Shell-versus-Go implementation differences and deliberate persistent-process architecture are not defects by themselves.

Evidence came from current source, tests, and runtime checks. The following verification passed before this report was written:

- firstmate: `tests/fm-pi-watch-extension.test.sh`
- firstmate: `tests/fm-sessionstart-nudge.test.sh`
- firstmate: `tests/fm-gate-refuse.test.sh`
- firstmate: `tests/fm-turnend-guard.test.sh`
- munsu: `go build ./...`
- munsu: `go vet ./...`
- munsu: `go test ./...` — 1,149 tests passed
- munsu persistent watcher: attached and heartbeat healthy

Three independent audits—supervision/runtime, delivery lifecycle, and whole-repository port mapping—were normalized against direct source inspection. Overclaims and obsolete findings are called out below rather than carried into the remediation plan.

## Executive result

Munsu has strong parity in core fleet state, persistent watch operation, delivery pipelines, harness detection, self-update rebuilding, and basic secondmate lifecycle commands. The remaining material gaps cluster around boundaries where a compiled fleet manager must integrate with external agent harnesses or reconstruct Git/delivery state after it has changed.

The highest-risk gaps are:

1. no installed native bridge from watcher events to the captain's harness context;
2. no guaranteed captain session-start nudge or harness turn-end seatbelt;
3. secondmate launch passes a literal `$(cat .../AGENTS.md)` argument because `exec.Command` does not invoke a shell;
4. no runtime gate-agent capability refusal equivalent to firstmate's scoped containment;
5. teardown can falsely refuse a safely squash-merged branch after the remote branch is deleted;
6. watcher updates lack executable/version ownership and restart verification;
7. PR-head metadata and parts of the brief protocol are not durable enough for reliable delivery recovery;
8. the repository development backlog and `$MUNSU_HOME` runtime backlog are not explicitly separated by tracked configuration, making accidental cross-domain task mutation possible.

## Classification

### True parity

| Capability | Munsu evidence | Assessment |
|---|---|---|
| Persistent deduplicating watcher | `internal/supervision/watcher.go`, `internal/cli/watch_cmd.go` | Implemented and runtime-verified. Persistence independent of the captain process is intentional. |
| Session-start fleet digest | `internal/session/sessionstart.go`, `internal/cli/session_cmd.go` | Core digest and watcher ensure exist. This is CLI parity, not native harness-injection parity. |
| Atomic self-update rebuild | `internal/selfupdate/update.go` | Fast-forward-only update and atomic replacement already exist, including version ldflags. Earlier claims that update only ran `git pull` were false. |
| Harness detection and launch templates | `internal/harness/adapter.go`, `internal/spawn/spawn.go` | Registry-driven support is materially broader than firstmate's original Pi path. |
| Delivery pipeline abstraction | `internal/delivery/domain.go`, `internal/delivery/pipeline.go` | Munsu has a deeper adapter-based delivery module; lack of shell-script resemblance is not a gap. |
| AFK injection seam | `internal/afk/injector.go`, `internal/afk/triage.go` | Existing send-key injection provides a reusable base, though target discovery is incomplete. |
| Mode-aware briefs and no-mistakes spawn preflight | `internal/brief/brief.go`, `internal/spawn/spawn.go` | Implemented by PR #196. Preflight is not a substitute for runtime gate-agent containment. |
| Core secondmate lifecycle surface | `internal/secondmate/secondmate.go`, `internal/cli/secondmate_cmd.go` | Seed, launch, retire, handoff, list, and config-push verbs exist; several implementations need hardening below. |
| Runtime backlog path | `internal/backlog/backlog.go`, `internal/cli/backlog_cmd.go` | Munsu correctly treats `$MUNSU_HOME/data/backlog.md` as the fleet-runtime queue. This must remain distinct from repository development planning. |

### Intentional, justified divergence

| Difference | Why it is not a defect |
|---|---|
| Watcher survives the captain process | Munsu intentionally uses a persistent watcher with lock/heartbeat state. Firstmate's shell process topology is not the required design. The real requirement is explicit ownership and version identity. |
| Compiled adapters instead of checkout-local hook scripts | Munsu should generate/install adapters through a stable integration boundary, not copy firstmate's repository-specific files into every project. |
| Adapter-based delivery instead of direct `gh` shell functions | The abstraction is deeper and supports multiple delivery modes. Only behavior gaps should be fixed. |
| No unconditional harness hook installation during `munsu init` | `docs/orchestration-contract.md` correctly places this under opt-in `munsu integrate install|repair`. Repository mutation must remain explicit. |

### Specification-only future work

`docs/orchestration-contract.md` defines Phase 3 `munsu integrate install|repair`, but no `internal/integrate` package or CLI command exists. This is not evidence that current code regressed; it is an unimplemented contract required to close the native harness gaps. Until implemented, `docs/port-mapping.md` must not claim installed follow-up/session/turn-end behavior.

## Confirmed operational gaps

### P0 — Development and runtime backlog domains are not explicitly separated

**Evidence**

- Munsu runtime code consistently uses `$MUNSU_HOME/data/backlog.md` through `internal/backlog/backlog.go` and `munsu backlog`.
- The development repository currently has an untracked root `backlog.md` used by direct `tasks-axi`, but no tracked `.tasks.toml` pins that path or defines retention/archive policy.
- Firstmate can use one `data/backlog.md` because its checkout is also its fleet home. Munsu separates a compiled product repository from one or more runtime homes, so copying firstmate's single-path layout would collapse two distinct domains.

**Impact**

An agent can run the correct tool from the wrong context and mutate engineering work when it intended fleet work, or mutate a runtime home when it intended repository planning. The identical Markdown schema makes such mistakes difficult to notice.

**Required behavior**

Establish two explicit sources of truth: repo `backlog.md` for munsu engineering work via direct `tasks-axi`, and `$MUNSU_HOME/data/backlog.md` for fleet runtime work via `munsu backlog --home`. Add tracked repository configuration, gitignored local archive state, path diagnostics, cross-boundary tests, and no implicit synchronization.

### P0 — Native captain integration is described but not installed

**Evidence**

- Firstmate injects Pi watcher exits with `pi.sendUserMessage(..., { deliverAs: "followUp" })` in `/Users/beowulf/Work/firstmate/.pi/extensions/fm-primary-pi-watch.ts`.
- Firstmate enforces session-start behavior in `bin/fm-sessionstart-nudge.sh` and documents harness-specific installation in `docs/sessionstart-nudge.md`.
- Firstmate provides a Pi turn-end guard in `.pi/extensions/fm-primary-turnend-guard.ts`.
- The same firstmate integration also applies scoped `PreToolUse` seatbelts, including watcher-arm misuse prevention and blocking navigation into managed project clones.
- Munsu's `internal/harness/adapter.go` advertises Pi `agent_settled`/`followUp` behavior and a `pi-ext.ts` state artifact, but the repository has no extension template, generator, installer, repairer, or tracked equivalent.
- `docs/orchestration-contract.md` specifies `munsu integrate install|repair`; the CLI has no `integrate` command.

**Impact**

A healthy watcher can write wakes while the captain receives no native follow-up. Session-start relies on the operator invoking the command, and the captain can finish a turn while actionable fleet events remain. The fleet can therefore be healthy on disk but unsupervised in the active agent context.

**Required behavior**

Create an opt-in integration subsystem that:

- detects the selected harness through the existing registry;
- installs only owned, marker-delimited artifacts;
- wires exactly-once session-start invocation;
- converts watcher exits/wakes into native follow-up delivery where supported;
- installs a turn-end guard that checks scoped actionable state;
- installs harness-native pre-tool safety checks for unsafe watcher control and navigation into managed project clones, where the harness exposes such hooks;
- supports dry-run, idempotent install, drift detection, and repair;
- fails closed for unknown/unverified harnesses;
- never overwrites unrelated user hooks or extensions.

Pi should be the first complete adapter. Claude, Codex, OpenCode, and Grok should be added only from verified native hook contracts, not inferred from command names.

### P0 — Secondmate launch sends a literal shell expression instead of the charter

**Evidence**

`internal/secondmate/secondmate.go::buildLaunchArgs` appends:

```go
"$(cat "+filepath.Join(secondmateHome, "AGENTS.md")+")"
```

`Launch` executes this with `exec.Command`, which does not perform shell expansion. The test `TestBuildLaunchArgs_VerifiedHarnesses` only asserts that an argument contains `AGENTS.md`, so it validates the bug rather than the intended prompt content.

**Impact**

A secondmate may start without its charter and therefore without its supervision boundaries. The same generic argv shape is also applied to every verified harness even though prompt/path semantics differ by adapter.

**Required behavior**

Read `AGENTS.md` in Go, pass its content through a harness-owned secondmate launch template, and test exact argv for every supported harness. A harness without a verified noninteractive secondmate launch contract must fail closed.

### P0 — Runtime no-mistakes gate containment is missing

**Evidence**

- Firstmate uses `bin/fm-gate-refuse-lib.sh` plus the shared primary-scope predicate in `bin/fm-primary-scope-lib.sh` to prevent gate-capable agents from acting as ordinary crewmates in the same checkout.
- Munsu's PR #196 adds no-mistakes preflight in `internal/spawn/spawn.go`, but there is no runtime equivalent using `NO_MISTAKES_GATE` or `.no-mistakes/repos/*.git`.

**Impact**

A gate/reviewer agent can be accidentally enlisted into the fleet it is supposed to judge, weakening delivery separation and creating recursion or self-approval risk.

**Required behavior**

Centralize primary/project scope classification and add refusal at every relevant entry point: session-start, spawn, native integration callbacks, and turn-end guard. Test environment markers, repository identity, worktrees, symlinks, unrelated repositories, and fail-closed error paths.

### P0 — Teardown safety fails after squash merge plus remote branch deletion

**Evidence**

- Firstmate `bin/fm-teardown.sh` recognizes merged PR topology even when the branch no longer has an upstream.
- `internal/teardown/teardown.go::safetyCheck` relies on upstream tracking/ancestry and can refuse after a squash merge because the feature commit is not an ancestor of the target branch and the remote head is gone.
- Munsu's delivery domain already has PR/check abstractions, but teardown does not use durable merged-PR evidence.

**Impact**

Normal successful delivery can leave worktrees and fleet state undeletable without a force path, encouraging unsafe manual cleanup.

**Required behavior**

Resolve teardown safety from multiple proofs in order: clean worktree, explicit recorded PR identity/head, provider merged status, local ancestry where meaningful, and conservative refusal when evidence is ambiguous. Never treat mere branch deletion as proof of merge.

### P0 — PR-head identity is not reliably durable across the delivery lifecycle

**Evidence**

`internal/delivery/prcheck.go` and `internal/delivery/prmerge.go` depend on a `pr_head` argument/metadata path, but the lifecycle does not guarantee that the exact remote head is captured and preserved before branch topology changes. Audit evidence also found command paths where an empty or reconstructed head can reach merge/check logic.

**Impact**

Checks may query or merge the wrong PR, and teardown loses the strongest link between a task and provider-side merge evidence.

**Required behavior**

Capture provider repository, PR number/URL, base, exact head ref, and head SHA as one validated delivery record. Consumers must reject missing or inconsistent identity rather than guess from the current checkout.

### P1 — Watcher ownership/version identity and update handshake are incomplete

**Evidence**

- `internal/selfupdate/update.go` atomically installs the rebuilt binary but does not restart or verify a running watcher.
- `internal/supervision/watcher.go` records liveness/heartbeat but not a sufficient executable build identity tied to the installed binary.
- `internal/lifecycle/lifecycle.go::AcquireSession` clears dead-PID lock records, but an alive reused or unrelated PID is not proven to be the watcher for this home.
- Both `watch ensure` and legacy `watch-arm` expose watcher startup paths.

**Impact**

After `munsu update`, an old watcher may continue running indefinitely while the CLI reports a new version. Duplicate launch logic increases the chance of divergent locking, logs, or restart behavior.

**Required behavior**

Record PID, process start identity, executable path, build version/commit, protocol version, and home. After a successful binary swap, perform a bounded stop/restart/heartbeat handshake when a watcher is active. Consolidate startup into one implementation and make `watch-arm` a deprecated compatibility alias before removal.

### P1 — Captain target discovery is incomplete

**Evidence**

`internal/afk/target.go::ResolveTarget` explicitly states runtime target detection is not implemented; normal operation depends on `config/captain-pane`.

**Impact**

Harness-neutral fallback notification cannot reliably locate the captain, and stale pane configuration can silently misdirect messages.

**Required behavior**

Define a target resolver chain with explicit config first, verified runtime/session metadata second, and a clear unsupported result. Validate target ownership before injection and expose the selected target in doctor output.

### P1 — Brief protocol omits terminal resolution records

**Evidence**

`internal/brief/brief.go` documents/handles working and paused supervision language but does not require or explain `resolved [key=...]`, which firstmate uses to close durable wake keys and avoid repeated handling.

**Impact**

Operators and agents can acknowledge a wake without durably closing its deduplication key, causing repeated supervision or ambiguous completion.

**Required behavior**

Add the terminal `resolved [key=...] <summary>` protocol to generated briefs, parser/validation tests, and supervision guidance. Preserve compatibility with existing status files.

### P1 — Secondmate registry, handoff, and inheritance do not match their stated contracts

**Evidence**

- `internal/secondmate/secondmate.go::List` scans `<parentHome>/secondmates` despite its comment saying the full implementation reads `data/secondmates.md`; it therefore omits registered homes outside that directory and loses scope/project/added metadata.
- `Seed` creates directories and `AGENTS.md` but no provenance marker equivalent to firstmate's `.fm-secondmate-home`.
- `Handoff` copies `.meta` and `.status` runtime files rather than moving complete queued backlog items through the authoritative backlog backend. It is not atomic across a multi-item set: earlier destination writes remain if a later write fails.
- `ConfigPush` propagates selected config but not `data/captain-shared.md`; writes are not atomic and destination path/provenance checks are weaker than firstmate's `fm-config-inherit-lib.sh`.
- Firstmate's `fm-backlog-handoff.sh` delegates complete block moves to `tasks-axi mv`, validates a seeded secondmate home, and refuses cross-home dependency damage.

**Impact**

Registry output can be wrong, task ownership can split between homes, handoff can lose backlog semantics/dependencies, and secondmates can drift from primary-authoritative captain preferences.

**Required behavior**

Make `data/secondmates.md` the authoritative registry, introduce a safe seeded-home marker, delegate backlog moves to the configured backend, make multi-key moves atomic, and push `captain-shared.md` read-only with atomic replacement and strict destination validation. Add a locked live-secondmate convergence sweep that validates homes, checks real agent liveness, propagates inherited material, and nudges only when the instruction surface changed.

### P2 — Repetitive guard warnings can create alert fatigue

**Evidence**

`internal/cli/guard.go::guardWarnWatcher` can emit the same watcher warning repeatedly without stateful suppression or transition-based reporting.

**Impact**

Frequent non-actionable warnings teach operators to ignore supervision diagnostics.

**Required behavior**

Report watcher degradation on state transition or with bounded cooldown, while keeping explicit `doctor` output unsuppressed.

### P2 — Session-start omits the compact backlog digest

**Evidence**

Firstmate `bin/fm-session-start.sh` prints a bounded, metadata-only backlog listing using `tasks-axi` when compatible and a title-line fallback otherwise. `internal/session/sessionstart.go` prints selected context files and in-flight fleet state but does not include queued backlog identities, blockers, or decision holds.

**Impact**

The captain starts with active fleet state but can miss dispatchable, blocked, or decision-held work unless it performs an additional backlog query.

**Required behavior**

Add a bounded compact backlog section to session-start. Use the configured backlog backend, omit task bodies, preserve blocker/hold metadata, and print a pointer for targeted full reads.

### P2 — Live secondmate convergence is not enforced at session start

**Evidence**

Firstmate `bin/fm-bootstrap.sh::secondmate_sync` validates live secondmate homes, fast-forwards them to the primary's local default-branch commit when safe, propagates inherited material, checks agent liveness, and sends a reread nudge only when the instruction surface changed. Munsu exposes individual secondmate commands and status helpers, but session-start/bootstrap does not orchestrate an equivalent locked convergence sweep.

**Impact**

Persistent secondmates can continue running stale instructions or inherited configuration while the primary reports a healthy session.

**Required behavior**

Add an idempotent locked convergence operation over the authoritative registry. Validate provenance, avoid network fetches during session start, update only safe fast-forwardable homes, propagate inheritance, verify agent—not merely pane—liveness, and retain retry evidence for failed reread nudges.

### P2 — Embedded decision-hold guidance contains stale implementation wording

**Evidence**

`internal/cli/skills/decision-hold-lifecycle/SKILL.md` correctly uses current `munsu` commands in its procedure, but its See also section says the Go decision-hold module "will add" commands even though `internal/decisionhold` already exists.

**Impact**

The embedded skill can make agents doubt the availability or authority of current commands.

**Required behavior**

Update the stale sentence when the related behavior PR lands; keep embedded skill references aligned with actual CLI capability.

### P2 — Diagnostics and documentation overstate integration readiness

**Evidence**

- `docs/port-mapping.md` maps nominal commands/modules but currently implies some harness follow-up/session behavior that has no installer or runtime artifact.
- Bootstrap/doctor output does not provide a complete integration capability matrix, artifact drift state, watcher build identity, or resolved captain target.

**Impact**

Operators cannot distinguish "harness recognized" from "native integration installed and healthy."

**Required behavior**

Doctor should report harness, adapter verification, integration installed/drifted/unsupported state, watcher CLI-versus-process version, captain target, gate-scope result, and actionable repair commands. Update port mapping only after behavior is tested.

## Rejected, corrected, or downgraded audit claims

1. **"Munsu update is only a pull and lacks atomic rebuild."** False. `internal/selfupdate/update.go` already performs ff-only update, atomic binary replacement, and version stamping. Only watcher restart/version convergence is missing.
2. **"The persistent watcher is inherently an orphan-process bug."** False. Persistence is intentional. Missing ownership/version identity and duplicate launch paths are the actionable issues.
3. **"Briefs do not describe paused state."** False. Paused behavior is present. The confirmed omission is terminal `resolved [key=...]` guidance.
4. **"Harness hook files should be copied during init."** Rejected. Integration must be opt-in and ownership-safe under `munsu integrate install|repair`.
5. **"Every firstmate script without a same-named Go file is a parity gap."** Rejected. Munsu's deeper modules and adapter boundaries are often intentional improvements.
6. **"Secondmate launch supports only Pi."** Obsolete as a nominal claim: the current registry accepts six harnesses. However, exact secondmate argv semantics are not verified, and the literal prompt bug makes current operational support unsafe.

## Priority order

1. Establish and verify the separate development/runtime backlog contracts before any implementation work.
2. Fix secondmate launch prompt correctness and fail-closed harness contracts.
3. Build the shared primary-scope/gate containment predicate.
4. Introduce durable delivery identity and topology-aware teardown.
5. Implement the integration framework and complete the Pi adapter end to end.
6. Add watcher build identity and update restart handshake; consolidate watcher launch paths.
7. Complete brief resolution and target discovery.
8. Repair secondmate registry/handoff/inheritance semantics.
9. Add doctor capability reporting and correct documentation claims.
10. Add remaining harness adapters only when each native contract is verified.

This order addresses immediate correctness and safety defects before expanding integration surface.