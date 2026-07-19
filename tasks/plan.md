# Firstmate parity remediation plan

> **Status: COMPLETE.** All planned PRs merged (#197–#216): backlog
> separation, scope refusal, delivery identity, watcher ownership, teardown
> topology, secondmate authority/handoff, wake target resolution, doctor
> diagnostics, reporting polish, worktree path fix, and native Pi/Claude/Grok/
> Codex/OpenCode/agy adapters. (agy's 'no hook surface' conclusion in #206 was
> wrong -- agy has a full hook system; its adapter landed in #218 and #206 was
> closed as completed.) The body below is
> the original plan, retained for reference.

## Goal

Close every confirmed operational parity gap from `docs/firstmate-parity-audit-2026-07-18.md` without copying checkout-specific firstmate scripts or weakening munsu's persistent-watcher and adapter-based architecture.

## Success criteria

- A watcher event reaches an integrated Pi captain as a native follow-up exactly once per durable key.
- Captain session start and turn end enforce the same scoped supervision contract as firstmate.
- Gate-capable agents cannot enter primary fleet work through supported entry points.
- Every supported secondmate harness receives the actual charter content; unverified launch contracts fail closed.
- Delivery identity survives branch deletion and supports safe teardown after squash merge.
- A binary update leaves any active watcher running the installed build and proves this by heartbeat/version handshake.
- Development work and fleet runtime each have one explicit backlog source of truth: repo `backlog.md` for engineering work, `$MUNSU_HOME/data/backlog.md` for orchestrated fleet work.
- `go build ./...`, `go vet ./...`, `go test ./...`, integration-tag tests, and targeted real-runtime checks pass.
- `docs/port-mapping.md` claims only behavior backed by tests and installable artifacts.

## Design constraints

- Integration is explicit: `munsu integrate install|repair`; `munsu init` must not silently modify harness configuration.
- Generated artifacts must be marker-owned, idempotent, dry-runnable, and non-destructive to user content.
- Unknown harnesses and ambiguous delivery/scope state fail closed.
- One internal service owns watcher launch/restart semantics.
- One durable delivery record owns PR identity.
- One authoritative registry owns secondmate identity and home paths.
- Development and runtime backlogs are intentionally separate; no implicit synchronization or fallback may cross the boundary.
- Use `tasks-axi` directly for repository development backlog state and `munsu backlog --home <home>` for fleet runtime state.
- Use existing seams: `internal/harness`, `internal/session`, `internal/supervision`, `internal/afk`, `internal/delivery`, and `session.Backend.SendKeys`.

## Dependency graph

```text
PR 0 development backlog foundation ─> every implementation PR
PR 1 secondmate launch correctness ───────────────────────────────┐
PR 2 primary-scope/gate predicate ───────┬───────────────────────┤
PR 3 delivery identity ──> PR 4 teardown ├─> PR 7 Pi integration ├─> PR 10 doctor/docs
PR 5 watcher identity ──> PR 6 update handshake ─────────────────┤
PR 8 brief resolution + target resolver ─────────────────────────┤
PR 9 secondmate registry/handoff/inheritance ────────────────────┘
PR 11 additional harness adapters depends on PR 7 and verified external contracts
```

## PR slices

### PR 0 — Establish separate development and runtime backlog contracts

**Scope**

- new tracked `.tasks.toml`
- `.gitignore`
- existing repo-root `backlog.md`
- backlog/bootstrap diagnostics and operator guidance where needed
- tests for path selection and accidental cross-boundary writes

**Implementation**

1. Keep repo-root `backlog.md` as the development backlog; do not move its current entries into `$MUNSU_HOME`.
2. Add tracked `.tasks.toml` that explicitly pins the repository development backend to `backlog.md`, archives old Done entries to a gitignored local path such as `.tasks/done-archive.md`, and keeps a bounded recent Done set.
3. Gitignore only generated development backlog archives/state; keep `backlog.md` local and untracked unless a later policy explicitly chooses to commit it.
4. Preserve `$MUNSU_HOME/data/backlog.md` as the sole fleet-runtime queue owned by `munsu backlog` and its backend selection.
5. Document and enforce command boundaries: direct `tasks-axi` in the repository manages development work; `munsu backlog --home <home>` manages runtime fleet work.
6. Add diagnostics that show both resolved paths and warn when a command would target the wrong backlog.
7. Reconcile the two existing development tasks using source/PR evidence, then file the remediation PR slices with dependency edges through `tasks-axi`; start only PR 1 after PR 0 lands.

**Acceptance criteria**

- `tasks-axi` run from the repo resolves to `<repo>/backlog.md` regardless of ambient `MUNSU_HOME`.
- `munsu backlog --home <fixture>` mutates only `<fixture>/data/backlog.md` and never repo `backlog.md`.
- A non-default runtime home continues forcing the existing leak-safe backend behavior.
- Existing task IDs, bodies, state, and dependency metadata survive configuration with byte-equivalent semantic output.
- Archive/prune operations cannot write into `$MUNSU_HOME` from repo development commands.
- Diagnostics print unambiguous development and runtime backlog paths.

**Migration/rollback**

- Before mutation, copy `backlog.md` to a timestamped file outside tracked product paths and capture `tasks-axi list`/`show --full` output for every active item.
- Apply `.tasks.toml`, run `tasks-axi render`, and compare item IDs, states, metadata, and full bodies.
- On mismatch, restore the byte-for-byte backup and remove `.tasks.toml`; do not continue to PR 1.

**Risk**

The two backlogs use the same Markdown vocabulary but represent different domains. Naming, diagnostics, and tests must prevent agents from treating them as synchronized mirrors.

### PR 1 — Correct and verify secondmate launch contracts

**Scope**

- `internal/secondmate/secondmate.go`
- `internal/secondmate/secondmate_test.go`
- `internal/harness/adapter.go`
- `internal/harness/*_test.go` as needed

**Implementation**

1. Read `AGENTS.md` before argv construction; never pass a shell expression.
2. Add an explicit secondmate launch contract to each adapter: binary, cwd semantics, prompt argument/flag, project/home argument, model flag, and supported boolean.
3. Build exact argv through the adapter contract.
4. Fail closed for adapters without a verified secondmate contract.
5. Keep process execution shell-free.

**Acceptance criteria**

- Tests assert exact charter bytes occur in argv/stdin as specified, not merely the string `AGENTS.md`.
- Table tests cover every registered harness.
- Unknown and nominal-but-unverified harnesses fail before process launch.
- A fake harness integration test captures argv/cwd and proves charter delivery.

**Risk**

Harness CLIs differ. Do not generalize the Pi convention to all adapters.

### PR 2 — Centralize primary scope and gate-agent refusal

**Scope**

- new `internal/scope` or similarly narrow package
- `internal/spawn/spawn.go`
- `internal/session/sessionstart.go`
- relevant CLI entry points
- tests

**Implementation**

1. Define canonical repository identity using resolved paths and Git common-dir/worktree metadata.
2. Detect gate capability from `NO_MISTAKES_GATE` and `.no-mistakes/repos/*.git` using explicit precedence.
3. Return structured decisions: primary, related worktree, unrelated, ambiguous/error.
4. Refuse gate agents in primary fleet scope at session-start and spawn.
5. Expose the predicate for integration and turn-end callbacks in PR 7.

**Acceptance criteria**

- Tests cover primary checkout, linked worktree, symlink, deleted path, unrelated repo, malformed marker, and unavailable Git.
- Ambiguous security-relevant state fails closed with a repairable error.
- Existing no-mistakes delivery preflight behavior remains intact.

**Risk**

Path-only comparison is insufficient; avoid rejecting unrelated repositories that share parent directories.

### PR 3 — Make delivery identity durable and validated

**Scope**

- `internal/delivery/domain.go`
- `internal/delivery/prcheck.go`
- `internal/delivery/prmerge.go`
- lifecycle/meta persistence call sites
- tests

**Implementation**

1. Introduce one delivery identity record containing provider/repo, PR number and URL, base ref, head ref, head SHA, and capture timestamp.
2. Capture it when a PR is created/discovered, before branch topology can change.
3. Validate identity consistency before checks or merge.
4. Persist it atomically in task metadata.
5. Stop reconstructing missing head identity from the current branch in destructive/merge paths.

**Acceptance criteria**

- Empty `pr_head`, missing SHA, and mismatched repository are rejected.
- Identity survives local checkout switch and remote head deletion.
- Existing adapters receive the same validated record.
- Backward compatibility has a read-only migration path that refuses destructive action when evidence is insufficient.

**Risk**

Metadata schema changes can strand in-flight tasks; include fixtures for old records.

### PR 4 — Make teardown topology-aware

**Depends on** PR 3.

**Scope**

- `internal/teardown/teardown.go`
- delivery provider query seam
- teardown tests/integration fixtures

**Implementation**

1. Separate cleanliness checks from merge-proof checks.
2. Accept provider-confirmed merged PR identity as proof after squash merge and branch deletion.
3. Retain ancestry proof for ordinary merges.
4. Refuse closed PRs, mismatched heads, dirty worktrees, and ambiguous provider failures.
5. Emit the exact proof used in dry-run/output.

**Acceptance criteria**

- Tests cover merge commit, squash merge, rebase merge, deleted remote head, unmerged deleted branch, wrong PR head, dirty worktree, and provider unavailable.
- Squash-merged/deleted branches can be safely removed without force.
- Branch deletion alone never authorizes teardown.

### PR 5 — Add watcher ownership and build identity

**Scope**

- `internal/supervision/watcher.go`
- `internal/cli/watch_cmd.go`
- legacy `watch-arm` registration
- tests

**Implementation**

1. Persist watcher identity: home, PID, process start token, executable path, build version/commit, protocol version, start time.
2. Validate PID reuse, executable mismatch, and alive-but-unrelated lock PID; recover only when ownership evidence proves the stale state belongs to this home.
3. Consolidate ensure/start logic into one internal service.
4. Make `watch-arm` call the same service and print a deprecation warning; do not remove it yet.
5. Include identity in watcher status.

**Acceptance criteria**

- Ensure is idempotent under concurrent calls.
- A stale PID file or reused PID is not treated as healthy.
- CLI and watcher build mismatch is visible and machine-readable.
- Both command paths produce identical lock/process behavior.

### PR 6 — Restart and verify watcher after update

**Depends on** PR 5.

**Scope**

- `internal/selfupdate/update.go`
- supervision restart API
- update tests

**Implementation**

1. Snapshot whether a watcher is active before binary replacement.
2. Preserve current ff-only and atomic rebuild/install behavior.
3. After swap, gracefully stop the old watcher, start through the canonical service, and wait for a heartbeat carrying the new build identity.
4. Roll back/report loudly if the installed binary cannot produce a healthy watcher; do not claim full success.

**Acceptance criteria**

- No active watcher means update performs no unsolicited start.
- Active watcher converges to the new build within a bounded timeout.
- Failed restart returns non-zero with old/new identity evidence.
- Integration test uses a controlled fake build/version transition.

**Risk**

Replacing the executable while its old process runs is platform-sensitive; test macOS and Linux behavior.

### PR 7 — Implement integration framework and Pi adapter

**Depends on** PR 2 and PR 5. PR 8 may land before or alongside it.

**Scope**

- new `internal/integrate/`
- CLI registration for `munsu integrate install|repair|status`
- generated Pi extension/template assets
- integration tests

**Implementation**

1. Define adapter capability model: session-start, wake follow-up, turn-end guard, pre-tool safety hooks, and optional fallback target injection.
2. Implement planner/dry-run, ownership markers, atomic writes, backups where replacement is owned, drift detection, repair, and uninstall metadata.
3. Pi adapter:
   - invoke `munsu session-start` exactly once per native session;
   - subscribe to watcher exits/wakes and deliver `followUp` messages;
   - include durable wake key and require `resolved [key=...]` completion;
   - run scoped gate refusal before fleet actions;
   - block/redirect turn end only for actionable scoped state;
   - apply harness-native pre-tool checks that refuse unsafe watcher-arm commands and navigation into managed project clones;
   - avoid recursive self-triggering and duplicate delivery.
4. Store installed artifact manifest/version under munsu home, not product runtime state directories prohibited by policy.

**Acceptance criteria**

- Fresh install, second install, drift detection, repair, and user-content coexistence tests pass.
- Pi runtime fixture proves one watcher wake becomes one native follow-up.
- Session-start is exactly once per session.
- Turn-end guard allows clean completion and stops only actionable unresolved state.
- Pre-tool fixtures allow ordinary commands while refusing unsafe watcher control and managed-clone navigation in primary scope.
- Unknown harness is reported unsupported without filesystem mutation.

**Risk**

Harness APIs can change independently. Version/capability checks must be explicit and diagnostics actionable.

### PR 8 — Complete wake resolution protocol and captain target resolver

**Scope**

- `internal/brief/brief.go`
- `internal/afk/target.go`
- `internal/afk/injector.go` if needed
- supervision tests

**Implementation**

1. Add `resolved [key=...] <summary>` to generated briefs and validation guidance.
2. Define target resolution precedence: explicit config, verified runtime metadata, unsupported.
3. Validate target/session ownership before send-key fallback.
4. Return structured target diagnostics; never silently guess.

**Acceptance criteria**

- Brief golden tests cover working, paused, and resolved states.
- Resolution closes a wake key idempotently.
- Stale or foreign pane targets are rejected.
- Doctor can display target source and validation result.

### PR 9 — Repair secondmate authority and transfer semantics

**Scope**

- `internal/secondmate/secondmate.go`
- backlog backend integration
- secondmate CLI/tests

**Implementation**

1. Parse `data/secondmates.md` as the authoritative registry and preserve ID/home/scope/project/added fields.
2. Seed a provenance marker with version/home identity; validate it before launch, retire, handoff, or config-push.
3. Replace runtime `.meta`/`.status` copying with complete queued-item moves through the configured backlog backend (`tasks-axi mv` where applicable).
4. Require all selected keys to classify before mutation and preserve dependency-connected moves atomically.
5. Push declared config plus `data/captain-shared.md` using atomic, content-aware replacement; mirror deletion and set the shared file read-only.
6. Add a locked convergence operation over registered live secondmates: validate provenance, perform safe local fast-forward, propagate inheritance, verify agent liveness, and persist/retry reread nudges only after instruction changes.
7. Reject destinations outside the validated secondmate home or tracked when the inheritance contract requires gitignored local state.

**Acceptance criteria**

- Registered homes outside `<parent>/secondmates` list correctly.
- Fake/project/primary homes are refused.
- Multi-item failure leaves both source and destination unchanged.
- Full backlog bodies and blocked-by relationships survive handoff.
- Config push is idempotent, mirrors deletion, and propagates `captain-shared.md` mode/content.
- Convergence leaves current homes untouched, refuses divergent/unsafe homes, and nudges a live secondmate exactly when its instruction surface changes.

**Risk**

Existing seeded homes lack markers. Provide an explicit validation/migration command; do not silently bless arbitrary directories.

### PR 10 — Doctor, bootstrap, warning policy, and truthful port mapping

**Depends on** PRs 2, 5, 7, 8, and 9.

**Scope**

- doctor/bootstrap diagnostics
- `internal/cli/guard.go`
- `docs/port-mapping.md`
- relevant embedded skills only if behavior instructions must change

**Implementation**

1. Report harness detected versus adapter verified versus integration installed/healthy/drifted.
2. Report watcher CLI/process build identities and protocol compatibility.
3. Report captain target source/health and gate-scope classification.
4. Report secondmate registry/marker migration issues.
5. Add a bounded metadata-only backlog digest to session-start using the configured backend, including blocker/decision-hold fields and no task bodies.
6. Make repetitive watcher warnings transition-based or cooldown-bounded; keep explicit doctor output complete.
7. Correct stale embedded skill wording and port mapping; distinguish nominal command/module presence from runtime/native parity.

**Acceptance criteria**

- Machine-readable and human-readable diagnostics agree.
- Every failure includes one repair command.
- Repeated healthy/unchanged guard calls do not spam the same warning.
- Session-start displays bounded queued/backlogged identity metadata without printing task bodies.
- Embedded skills describe currently available commands, and documentation claims are linked to passing tests or marked planned/unsupported.

### PR 11 — Add other native harness adapters one by one

**Depends on** PR 7 and official verified contracts for each harness.

Create separate PRs for Claude, Codex, OpenCode, and Grok. Each PR must include official API/hook evidence, install/repair fixtures, native session-start test, wake-delivery test, turn-end behavior test, version compatibility, and an unsupported-version path. Do not batch speculative adapters.

## Verification strategy

For every PR:

```sh
gofmt -w <changed-go-files>
go build ./...
go vet ./...
go test ./...
```

When integration-tag coverage is touched:

```sh
./scripts/test.sh
# or the repository's explicit integration-tag invocation
```

Runtime gates before the series is considered complete:

1. Start a clean Pi session with installed integration; observe exactly one session-start digest.
2. Spawn a controlled crewmate; produce one wake key; observe one native follow-up.
3. Attempt turn completion with unresolved actionable state; observe guard; resolve key; observe completion allowed.
4. Run as a simulated no-mistakes gate in primary scope; observe refusal before fleet mutation.
5. Deliver a PR by squash merge, delete its remote branch, then dry-run and execute safe teardown.
6. Start a watcher on build A, update to build B, and verify the same home reports a build-B heartbeat with no duplicate process.
7. Seed, launch, list, hand off a dependency-connected backlog set, config-push, and retire a secondmate through a fake harness/backend fixture.

## Rollout

- Land PRs 1–6 before enabling any new native integration by default.
- Ship Pi integration opt-in first and gather runtime evidence.
- Keep fallback/manual commands available during one compatibility window.
- Deprecate but retain `watch-arm` until integrations and operator skills no longer call it.
- Add additional harnesses independently; unsupported is safer than nominal-but-broken support.
