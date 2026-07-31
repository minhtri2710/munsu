# 0006. Explicit State Migration, Build Provenance, and Compatibility Gates

* **Status:** Accepted; implementation pending
* **Date:** 2026-07-30
* **Extends:** ADR-0002 §11–12, ADR-0003 migration policy
* **Triggered by:** `munsu-workflow-incident-report-2026-07-30.md`

## Context

A legacy `state/.wake-resolutions` regular file blocked acknowledgement after the runtime changed that path into a directory of JSON records. The source checkout, running CLI, watcher, Captain homes, and generated integrations also ran at different revisions, obscuring diagnosis. Existing version comparisons do not prove which executable is running, whether it was built from dirty source, whether PATH selects another binary, or whether a mismatched component is contract-compatible.

Migration and compatibility must be explicit, typed, and operation-specific. Runtime load paths must not accumulate permanent dual-read behavior.

## Decision

### 1. Explicit single-home migration primitive

State migration is an explicit idempotent transaction scoped to one exact home. Read paths and session diagnostics detect legacy state but do not mutate it. An affected mutation fails with an exact command such as:

```text
munsu state migrate --home <exact-home>
```

For wake-resolution migration the transaction:

1. Acquires an exclusive home migration lock.
2. Detects path type and source format without mutation.
3. Parses and validates all legacy records before writing.
4. Builds the current representation in a staging path.
5. Writes typed records carrying source format, migration time, and legacy-record digest.
6. Round-trips and verifies staged count and aggregate digest.
7. Renames the original to `.wake-resolutions.legacy-<timestamp>-<digest>`.
8. Atomically installs the staged directory.
9. Writes a durable receipt containing source/target schema, counts, digests, archive path, and tool provenance.

Failure before installation leaves the original untouched and returns line-specific diagnostics. Re-running a completed migration verifies the receipt and returns `already_migrated`. The normal runtime does not permanently dual-read or re-import archives.

### 2. Explicit fleet plan/apply wrapper

The single-home primitive is composed by an explicit fleet workflow:

```text
munsu state migrate plan --scope fleet
munsu state migrate apply --plan <plan-id>
```

The immutable plan records home identities and paths, detected schemas/path types, source digests, required target schemas, dependency ordering, and tool provenance. Apply revalidates identity, provenance marker, path, and source digest before each independent home transaction. It never discovers new homes during apply. Per-home outcomes include `migrated`, `already-current`, `source-changed`, `corrupt`, `unreachable`, and `skipped`; partial unrelated success is retained and aggregate exit is non-zero while any home remains required/corrupt.

The same framework serves config and future state migrations. Config migration remains the hard JSON cutover from ADR-0003. No migration silently crosses from General to Captain homes.

### 3. Complete build provenance and runtime observations

Every built executable embeds structured provenance:

```go
type BuildProvenance struct {
    Version           string
    CommitSHA         string
    SourceDirty       bool
    BuildTimestamp    time.Time
    GoVersion         string
    ModuleVersion     string
    BuildMode         string
    IntegrationDigest string
    ContractVersion  string
}
```

Runtime identity adds canonical executable path and executable digest. Diagnostics observe, rather than conflate, the running executable, PATH-resolved executable, install-root source, General source, Captain sources, watcher runtime, and generated integration artifacts.

Typed skew classifications include `match`, `path-shadowed`, `binary-behind-source`, `binary-ahead-of-source`, `source-dirty`, `unverifiable-build`, `watcher-mismatch`, `captain-source-behind`, `integration-mismatch`, and `contract-incompatible`. Full SHAs are authoritative; short SHAs are display-only. Packaged installs without Git source are valid and use module/build provenance. Dirty builds are unverifiable unless a source-state digest was stamped.

Session start and doctor return structured observations, prominent actionable warnings, and exact rebuild/update/select-executable commands. Captain and Soldier launch-input digests include runtime identity, integration digest, and accepted compatibility observations.

### 4. Capability-specific compatibility gates

Version inequality alone does not block mutation. Each operation declares a `CompatibilityRequirement` covering minimum contract, required schemas, optional exact integration digest, and verified/exact build needs.

Examples:

* Diagnostics, fleet view, and backlog reads never block.
* Task mutation requires lifecycle schema compatibility.
* Soldier spawn requires Task, config, envelope, endpoint, and worktree-binding compatibility.
* Captain launch/recovery requires exact canonical integration digest and supported endpoint/config contracts.
* Delivery merge requires delivery/Decision/IssueLink compatibility, not exact source equality.
* Migration requires a verified build and explicit supported from/to schemas.
* Teardown retains an evidence-preserving safe path but refuses incompatible interpretation of ownership or merge proof.

`path-shadowed` blocks self-update/recovery until the intended executable is explicitly selected. Incompatible decoding or corrupt schema is never force-overridable. Other gates may be overridden only by an explicit durable operator authorization naming the waived requirement.

### 5. Context-scoped delivery capability attestation

Delivery-mode readiness is recorded as a typed attestation scoped to project, execution home, worktree class, harness, gate agent, binary identity, capabilities, missing capabilities, resolved-config digest, observation time, and expiry.

For no-mistakes, readiness proves compatible protocol, selected gate-agent availability, instruction neutralization support (or explicit project disabling), provider/auth requirements, and required daemon reachability. Presence on PATH is not sufficient. Cached attestations are reusable only for the same context, unchanged input digest, and valid TTL. They are revalidated before irreversible mode-specific operations.

Capability loss preserves implementation work and emits a typed mode block. Any transition to another Delivery Plan follows ADR-0004 authorization policy; it is never a silent relabel.

## Consequences

* Legacy state is preserved and migrated predictably instead of failing inside unrelated acknowledgement paths.
* Fleet migration is ergonomic without making broad implicit mutations.
* Operators can distinguish source, executable, watcher, Captain, and integration skew.
* Compatible mixed versions remain usable while contract-incompatible mutations fail closed.
* Recovery can tell whether changed binary/config/capability inputs justify a new attempt series.
* Build stamping, migration receipts, compatibility declarations, and typed diagnostics add implementation and release-process work.

## Rejected Alternatives

* Lazy migration in `ResolveWake`: hides mutation in an acknowledgement path and complicates partial failure.
* Session-start auto-migration: makes diagnostics unexpectedly mutate state.
* Fleet-wide implicit migration: broad scope and weak per-home failure isolation.
* Commit/version string comparison alone: misses dirty builds, PATH shadowing, and different executables with the same label.
* Warning-only skew handling: allows known-incompatible mutation.
* Exact-version equality for every mutation: unnecessarily blocks packaged and contract-compatible mixed versions.
* A global capability cache: execution context and selected harness/gate agent differ by project and home.
