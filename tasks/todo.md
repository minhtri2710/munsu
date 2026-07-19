# Firstmate parity remediation checklist

> **Status: COMPLETE.** Every item below was implemented across merged PRs
> [#197](https://github.com/minhtri2710/munsu/pull/197)–[#216](https://github.com/minhtri2710/munsu/pull/216).
> The unchecked boxes are the original planning checklist; do not read them as
> open work. (agy's 'no hook surface' conclusion was wrong -- agy has a full hook system;
> its adapter landed in [#218](https://github.com/minhtri2710/munsu/pull/218) and
> [#206](https://github.com/minhtri2710/munsu/issues/206) was closed as completed. No open items remain.)

## PR 0 — Development backlog foundation

- [ ] Keep repo `backlog.md` as the development-work source of truth.
- [ ] Add tracked `.tasks.toml` pinning `tasks-axi` to repo `backlog.md`.
- [ ] Configure bounded Done retention and a gitignored local archive path.
- [ ] Add only generated tasks archive/state paths to `.gitignore`.
- [ ] Preserve `$MUNSU_HOME/data/backlog.md` as the separate runtime fleet queue.
- [ ] Document direct `tasks-axi` for development versus `munsu backlog --home` for runtime.
- [ ] Add diagnostics that print both resolved backlog paths.
- [ ] Add tests proving development commands cannot mutate runtime backlog.
- [ ] Add tests proving runtime commands cannot mutate repo backlog.
- [ ] Back up the current root backlog before configuration.
- [ ] Reconcile `munsu-persistent-watcher` against PR #196 evidence.
- [ ] Reconcile `munsu-test-process` against PR #195 evidence.
- [ ] File all remediation PR slices in `tasks-axi` with dependency edges.
- [ ] Start only the first unblocked implementation slice after PR 0 passes.

## P0 correctness and safety
### Secondmate launch

- [ ] Replace literal `$(cat <home>/AGENTS.md)` argv with actual charter content.
- [ ] Add explicit secondmate launch capability/configuration to harness adapters.
- [ ] Verify exact argv/cwd/prompt behavior for each registered harness.
- [ ] Fail closed for unverified secondmate harness contracts.
- [ ] Add fake-binary process test proving charter delivery.

### Gate containment

- [ ] Implement canonical primary/repository scope classifier.
- [ ] Detect `NO_MISTAKES_GATE` and `.no-mistakes/repos/*.git` capability evidence.
- [ ] Add refusal to session-start.
- [ ] Add refusal to spawn.
- [ ] Expose refusal predicate to native integration callbacks.
- [ ] Test worktrees, symlinks, unrelated repositories, malformed state, and Git failures.

### Delivery identity

- [ ] Define durable delivery identity: provider/repo/PR/base/head/head SHA.
- [ ] Capture identity at PR creation/discovery.
- [ ] Persist identity atomically in task metadata.
- [ ] Reject missing or inconsistent `pr_head`/SHA before check and merge.
- [ ] Add old-metadata compatibility fixtures that refuse unsafe guessing.

### Safe teardown

- [ ] Separate clean-worktree validation from merge proof.
- [ ] Accept provider-confirmed squash/rebase/merge completion.
- [ ] Support deleted remote heads when durable PR identity proves merge.
- [ ] Refuse unmerged deleted branches and mismatched PR heads.
- [ ] Print the proof used during teardown dry-run.

## P1 supervision and integration

### Watcher identity

- [ ] Persist watcher executable path, build commit/version, protocol version, PID, and process-start identity.
- [ ] Detect PID reuse, executable mismatch, and alive-but-unrelated lock owners.
- [ ] Consolidate `watch ensure` and `watch-arm` behind one start service.
- [ ] Mark `watch-arm` deprecated without removing it yet.
- [ ] Add concurrent ensure and stale-state tests.

### Update handshake

- [ ] Detect whether a watcher is active before update.
- [ ] Restart only previously active watchers after binary replacement.
- [ ] Wait for heartbeat from the newly installed build.
- [ ] Return non-zero and identity evidence if restart/verification fails.
- [ ] Add controlled build-A to build-B integration test.

### Integration framework

- [ ] Add `internal/integrate` package.
- [ ] Add `munsu integrate status`.
- [ ] Add `munsu integrate install --dry-run`.
- [ ] Add idempotent `munsu integrate install`.
- [ ] Add drift-aware `munsu integrate repair`.
- [ ] Define owned artifact markers and manifest/version storage.
- [ ] Ensure install never overwrites unrelated user hooks/extensions.
- [ ] Fail without mutation for unknown/unverified harnesses.

### Pi native adapter

- [ ] Generate/install Pi extension through the integration framework.
- [ ] Invoke session-start exactly once per Pi session.
- [ ] Deliver watcher wakes with native `followUp` messages.
- [ ] Include durable wake key in every follow-up.
- [ ] Prevent duplicate/self-recursive wake delivery.
- [ ] Add scoped turn-end guard.
- [ ] Add harness-native pre-tool guard for unsafe watcher-arm commands.
- [ ] Add harness-native pre-tool guard for navigation into managed project clones.
- [ ] Scope pre-tool checks so unrelated repositories and ordinary commands remain unaffected.
- [ ] Run gate-agent refusal before fleet action.
- [ ] Add real/fake Pi integration tests for install, wake, resolve, turn end, and pre-tool refusal.

### Brief and target protocol

- [ ] Add `resolved [key=...] <summary>` to generated briefs.
- [ ] Add parser/golden tests for working, paused, and resolved states.
- [ ] Implement captain target resolver precedence.
- [ ] Validate pane/session ownership before send-key fallback.
- [ ] Return explicit unsupported/stale-target diagnostics.

## P1 secondmate lifecycle

### Registry and provenance

- [ ] Make `data/secondmates.md` authoritative for `secondmate list`.
- [ ] Preserve ID, home, scope, project, and added metadata.
- [ ] Add seeded-home provenance marker.
- [ ] Validate marker before launch, handoff, config-push, retire, or removal.
- [ ] Provide explicit migration for existing unmarked homes.

### Backlog handoff

- [ ] Replace `.meta`/`.status` copying with authoritative backend item moves.
- [ ] Move complete queued item bodies.
- [ ] Preserve blocked-by relationships and connected sets.
- [ ] Classify all requested keys before mutating either backlog.
- [ ] Guarantee multi-key all-or-nothing behavior.
- [ ] Refuse in-flight/done items and unsafe cross-home dependencies.

### Config inheritance

- [ ] Propagate declared config with atomic content-aware replacement.
- [ ] Mirror primary-side deletion.
- [ ] Propagate `data/captain-shared.md` read-only.
- [ ] Validate destination remains inside a genuine secondmate home.
- [ ] Preserve/verify gitignored local-state requirements.
- [ ] Add idempotence, deletion, symlink, and tracked-destination tests.

### Live secondmate convergence

- [ ] Iterate the authoritative registry under the session lock.
- [ ] Validate provenance before touching a secondmate home.
- [ ] Fast-forward only to an already-local primary default-branch commit.
- [ ] Refuse divergent, dirty, or otherwise unsafe homes.
- [ ] Propagate inherited config and `captain-shared.md` during convergence.
- [ ] Verify agent liveness rather than assuming a recorded pane/window is alive.
- [ ] Nudge only after the instruction surface changes.
- [ ] Persist retry evidence when a reread nudge fails.

## P2 operability and truthfulness

### Doctor/bootstrap

- [ ] Report detected harness separately from verified adapter.
- [ ] Report integration installed/healthy/drifted/unsupported.
- [ ] Report watcher CLI and running-process build identities.
- [ ] Report captain target source and validation status.
- [ ] Report gate-scope classification.
- [ ] Report secondmate registry/marker migration issues.
- [ ] Include one actionable repair command for every failure.

### Session-start digest

- [ ] Add bounded compact backlog identities to session-start.
- [ ] Use the configured backlog backend with a safe title-line fallback.
- [ ] Include blocked-by and decision-hold metadata.
- [ ] Omit task bodies and print a targeted full-read pointer.

### Warning policy

- [ ] Make watcher degradation warnings transition-based or cooldown-bounded.
- [ ] Keep explicit doctor output unsuppressed.
- [ ] Test repeated guard invocation does not create warning fatigue.

### Documentation and capability claims

- [ ] Correct `docs/port-mapping.md` to separate nominal mapping from runtime parity.
- [ ] Mark unimplemented harness integrations planned/unsupported.
- [ ] Update operator skill text only where command behavior changed.
- [ ] Remove stale future-tense wording from the embedded decision-hold skill.
- [ ] Link capability claims to tests or runtime verification.

## Later: verified harness expansion

- [ ] Research official Claude native lifecycle hooks and version contract.
- [ ] Implement/test Claude adapter in its own PR.
- [ ] Research official Codex native lifecycle hooks and version contract.
- [ ] Implement/test Codex adapter in its own PR.
- [ ] Research official OpenCode native lifecycle hooks and version contract.
- [ ] Implement/test OpenCode adapter in its own PR.
- [ ] Research official Grok native lifecycle hooks and version contract.
- [ ] Implement/test Grok adapter in its own PR.

## Final release gate

- [ ] `gofmt` clean.
- [ ] `go build ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `go test ./...` passes.
- [ ] Integration-tag suite passes.
- [ ] Pi session-start runs exactly once.
- [ ] One wake produces one native follow-up and one resolution.
- [ ] Turn-end guard releases after resolution.
- [ ] Gate agent is refused in primary scope.
- [ ] Squash-merge plus branch deletion tears down safely.
- [ ] Watcher converges to new binary after update.
- [ ] Secondmate charter, registry, handoff, inheritance, and retirement runtime checks pass.
