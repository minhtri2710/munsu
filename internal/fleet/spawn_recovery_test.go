package fleet

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestLaunchRecoveryCrashBoundariesNoDuplicates drives a crash at every
// launch boundary and proves recovery with the same Operation ID/generation
// continues the same launch without duplicate worktree, endpoint, launch
// submission, or working records: exactly one Worktree/Endpoint/
// AcquiredEndpoint/Launch/LaunchEvidence record, one endpoint creation, one
// submission, and one queued → working transition.
func TestLaunchRecoveryCrashBoundariesNoDuplicates(t *testing.T) {
	boundaries := []struct {
		crashAfter string
		wantErr    bool // re-acquisition at the pre-bind boundary re-calls the deterministic git-fallback provider
	}{
		{"begin", false},           // before acquisition
		{"acquire", false},         // after worktree acquisition, before bind
		{"bind-worktree", false},   // after worktree bind
		{"create-session", false},  /* after endpoint create */
		{"attach-endpoint", false}, // after durable attach
		{"submit", false},          // after launch submit
		{"meta", false},            // after metadata, before final bind
		{"confirm", false},         // after final bind
	}
	for _, b := range boundaries {
		t.Run(b.crashAfter, func(t *testing.T) {
			f := newLaunchFixture(t, "recover-"+strings.ReplaceAll(b.crashAfter, "-", ""))

			err := runLaunchPhases(f, b.crashAfter)
			if !errors.Is(err, errCrashSimulated) {
				t.Fatalf("first run stopped at %q with %v, want simulated crash", b.crashAfter, err)
			}
			first := f.aggregate()

			// Recovery: re-run the full launch sequence.
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatalf("recovery run failed: %v", err)
			}
			agg := f.aggregate()
			if agg.Phase != taskauthority.PhaseWorking {
				t.Fatalf("phase = %q, want working", agg.Phase)
			}
			if agg.Worktree == nil || agg.Endpoint == nil || agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil || agg.Launch == nil {
				t.Fatalf("launch records incomplete: %+v", agg)
			}
			if f.endpoints.createCount() != 1 {
				t.Fatalf("endpoint creates = %d, want 1 (no duplicate endpoint)", f.endpoints.createCount())
			}
			if f.endpoints.submitCount() != 1 {
				t.Fatalf("launch submits = %d, want 1 (no duplicate submission)", f.endpoints.submitCount())
			}
			// One queued -> working transition: revision 6 (create, begin,
			// bind worktree, attach endpoint, record launch, bind endpoint).
			if agg.Revision != 6 {
				t.Fatalf("revision = %d, want 6 (one launch through working)", agg.Revision)
			}
			// The re-adopted records are the FIRST attempt's records, never
			// fresh ones (where the first attempt committed them).
			if first.AcquiredEndpoint != nil && agg.AcquiredEndpoint.Handle != first.AcquiredEndpoint.Handle {
				t.Fatalf("acquired endpoint replaced on recovery: %s -> %s", first.AcquiredEndpoint.Handle, agg.AcquiredEndpoint.Handle)
			}
			if first.LaunchEvidence != nil && agg.LaunchEvidence.CommandDigest != first.LaunchEvidence.CommandDigest {
				t.Fatalf("launch evidence replaced on recovery")
			}
		})
	}
}

// TestLaunchRecoveryAfterLaunchRecordSkipsSubmission proves the evidence-first
// launch guard: a failure after the durable launch record but before the
// submission is delivered never re-submits — recovery skips the submission
// under the exact launch identity (launch submit <= 1, no at-least-once
// duplicate launch).
func TestLaunchRecoveryAfterLaunchRecordSkipsSubmission(t *testing.T) {
	f := newLaunchFixture(t, "recover-after-record")
	f.endpoints.submitErr = errors.New("delivery failed after record")
	if err := runLaunchPhases(f, "submit"); err == nil {
		t.Fatal("first run must fail at the submission")
	}
	agg := f.aggregate()
	if agg.LaunchEvidence == nil {
		t.Fatalf("launch evidence must be durably recorded before the submission: %+v", agg)
	}
	// Recovery: the recorded evidence skips the submission entirely.
	f.endpoints.submitErr = nil
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recovery run failed: %v", err)
	}
	final := f.aggregate()
	if final.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase = %q, want working", final.Phase)
	}
	if f.endpoints.submitCount() != 1 {
		t.Fatalf("launch submits = %d, want 1 (submission never re-issued)", f.endpoints.submitCount())
	}
	if final.LaunchEvidence.CommandDigest != agg.LaunchEvidence.CommandDigest {
		t.Fatalf("launch evidence identity changed on recovery")
	}
}

// TestLaunchRecoveryCallCountsAfterAttach proves the required call counts on
// same-operation/generation re-entry after a mid-flow crash (after the durable
// endpoint attach): worktree acquisition <= 1, endpoint create <= 1, launch
// submit <= 1, final working transition <= 1, and exactly one
// endpoint/Soldier record.
func TestLaunchRecoveryCallCountsAfterAttach(t *testing.T) {
	f := newLaunchFixture(t, "recover-counts")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// After the attach the worktree is durably bound: recovery must NOT call
	// the worktree provider again. Prove it by breaking the repo so a second
	// acquisition would fail — recovery must still succeed by adopting the
	// bound worktree.
	if err := breakWorktreeRepo(t, f); err != nil {
		t.Fatal(err)
	}
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recovery run failed (worktree must be adopted, not re-acquired): %v", err)
	}
	agg := f.aggregate()
	if f.endpoints.createCount() != 1 {
		t.Fatalf("endpoint create = %d, want 1", f.endpoints.createCount())
	}
	if f.endpoints.submitCount() != 1 {
		t.Fatalf("launch submit = %d, want 1", f.endpoints.submitCount())
	}
	if agg.Phase != taskauthority.PhaseWorking || agg.Endpoint == nil {
		t.Fatalf("one final working transition expected: %+v", agg)
	}
	if agg.Endpoint.Handle != "pane-1" {
		t.Fatalf("endpoint record = %+v, want exactly the single created pane", agg.Endpoint)
	}
}

// breakWorktreeRepo makes a second worktree acquisition fail (the repo is
// removed) so recovery provably skips the provider when the worktree is
// durably bound.
func breakWorktreeRepo(t *testing.T, f *launchFixture) error {
	t.Helper()
	// Remove the primary repo: a re-acquisition through the git fallback
	// would fail (git worktree add needs the repo). The bound worktree path
	// itself is a separate directory under the home and stays usable.
	if err := os.RemoveAll(f.repoPath); err != nil {
		return fmt.Errorf("removing repo for acquisition probe: %w", err)
	}
	return nil
}

// TestLaunchRecoveryAfterFinalBindReplaysIdempotently proves recovery after
// the final bind does not duplicate any record: the launch is complete under
// the identical identity and re-entry replays idempotently (task working, one
// of every record, revision unchanged).
func TestLaunchRecoveryAfterFinalBindReplaysIdempotently(t *testing.T) {
	f := newLaunchFixture(t, "recover-final")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("first full run: %v", err)
	}
	first := f.aggregate()
	if first.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase = %q, want working", first.Phase)
	}
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("idempotent re-entry failed: %v", err)
	}
	second := f.aggregate()
	if second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("re-entry mutated the aggregate: %+v vs %+v", second, first)
	}
	if second.Endpoint.Handle != first.Endpoint.Handle || second.AcquiredEndpoint.Handle != first.AcquiredEndpoint.Handle {
		t.Fatalf("records replaced on idempotent re-entry")
	}
	if f.endpoints.createCount() != 1 || f.endpoints.submitCount() != 1 {
		t.Fatalf("duplicate create/submit on idempotent re-entry: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
	}
}

// TestLaunchRecoveryEndpointIdentitySubstitutionFailsClosed proves a recorded
// acquired endpoint can never be substituted: an attach attempt carrying a
// different endpoint identity fails closed (canonical conflict), and a
// tampered recorded identity fails closed on the runner's identity check.
func TestLaunchRecoveryEndpointIdentitySubstitutionFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-ep-sub")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// Substitution: the runner presents a DIFFERENT created endpoint than the
	// recorded one; attach must fail closed (never overwrite the record).
	f.runner.endpoint.Handle = "pane-substituted"
	if err := f.runner.attachEndpoint(); err == nil {
		t.Fatal("endpoint identity substitution must fail closed")
	} else if !strings.Contains(err.Error(), "does not match created endpoint") {
		t.Fatalf("error = %v, want identity-mismatch refusal", err)
	}

	// Tampered durable record: the committed acquired endpoint carries a
	// different handle; the runner's verification refuses.
	f2 := newLaunchFixture(t, "recover-ep-tamper")
	if err := runLaunchPhases(f2, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	tamperTaskAggregate(t, f2.homeDir, f2.taskID, func(agg *taskauthority.Aggregate) {
		if agg.AcquiredEndpoint != nil {
			agg.AcquiredEndpoint.Handle = "pane-tampered"
		}
	})
	if err := f2.runner.attachEndpoint(); err == nil {
		t.Fatal("tampered acquired endpoint must fail closed")
	}
}

// TestLaunchRecoveryWorktreeIdentitySubstitutionFailsClosed proves the
// intent-owned worktree fence is exact: a retry whose launch derivation
// differs from the committed worktree binding fails closed instead of
// re-binding or adopting a foreign worktree.
func TestLaunchRecoveryWorktreeIdentitySubstitutionFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-wt-sub")
	if err := runLaunchPhases(f, "bind-worktree"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// The retry derives a DIFFERENT intent (changed snapshot): the committed
	// binding's fence can never match the new derivation.
	f.runner.projectConfig.SnapshotDigest = strings.Repeat("c", 64)
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("changed intent after binding must fail closed")
	}
	// A direct substitution of the intent fence (simulating a regression that
	// mints a different reservation) fails closed at the bind verification.
	f2 := newLaunchFixture(t, "recover-wt-fence")
	if err := runLaunchPhases(f2, "bind-worktree"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	f2.runner.launch = &taskauthority.LaunchIntent{
		LaunchID:              f2.runner.launch.LaunchID,
		WindowLabel:           f2.runner.launch.WindowLabel,
		WorktreeReservationID: "wtres-foreign",
		WorktreeFenceToken:    "wtfence-foreign",
		EndpointReservationID: f2.runner.launch.EndpointReservationID,
		EndpointFenceToken:    f2.runner.launch.EndpointFenceToken,
		Backend:               f2.runner.launch.Backend,
	}
	if err := f2.runner.bindWorktree(); err == nil {
		t.Fatal("worktree fence substitution must fail closed")
	} else if !strings.Contains(err.Error(), "reservation fence") {
		t.Fatalf("error = %v, want worktree reservation fence refusal", err)
	}
}

// TestLaunchRecoveryReentrantEndpointRequiredOnReentry proves the
// DEPENDENCY_REQUEST fail-closed contract: when recovery needs an endpoint
// and the selected capability cannot prove reservation-aware find-or-create,
// creation fails closed instead of silently creating a second endpoint.
func TestLaunchRecoveryReentrantEndpointRequiredOnReentry(t *testing.T) {
	f := newLaunchFixture(t, "recover-noreentrant")
	// A capability WITHOUT reservation support: the first attempt creates
	// normally; a re-entry (intent pre-existed) must fail closed.
	f.runner.endpoints = &plainEndpointCapabilities{inner: f.endpoints}
	if err := runLaunchPhases(f, "create-session"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// Re-entry: beginLaunchIntent re-adopts the committed intent (re-entry
	// detection), then endpoint creation cannot prove find-or-create.
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("re-entry beginLaunchIntent: %v", err)
	}
	if err := f.runner.createSession(); err == nil {
		t.Fatal("re-entry without reservation-aware find-or-create must fail closed")
	} else if !strings.Contains(err.Error(), "DEPENDENCY_REQUEST") {
		t.Fatalf("error = %v, want DEPENDENCY_REQUEST fail-closed", err)
	}
}

// plainEndpointCapabilities is an EndpointCapabilities WITHOUT
// ReentrantEndpointCapabilities support (delegates to the inner capability).
type plainEndpointCapabilities struct {
	inner *reentrantEndpointCapabilities
}

func (p *plainEndpointCapabilities) Create(req CreateRequest) (CreatedEndpoint, error) {
	return p.inner.Create(req)
}
func (p *plainEndpointCapabilities) Submit(ep CreatedEndpoint, text string) error {
	return p.inner.Submit(ep, text)
}
func (p *plainEndpointCapabilities) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	return p.inner.Probe(ep)
}
func (p *plainEndpointCapabilities) Capture(ep CreatedEndpoint, n int) (string, error) {
	return p.inner.Capture(ep, n)
}
func (p *plainEndpointCapabilities) Dispose(ep CreatedEndpoint) error {
	return p.inner.Dispose(ep)
}

// TestLaunchArtifactReentrantGuardRealPath exercises the REAL production
// launch artifact path: buildLaunchArtifact produces the identical artifact
// (same command digest) for the identical immutable inputs, an artifact whose
// content differs fails closed (identity mismatch, never overwritten), and
// the guard binds the exact submission digest.
func TestLaunchArtifactReentrantGuardRealPath(t *testing.T) {
	f := newLaunchFixture(t, "artifact-real")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	if err := f.runner.bindWorktree(); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := f.runner.buildSoldierPrompt(); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}

	first, err := buildLaunchArtifact(f.runner.wtPath, f.homeDir, f.taskID, f.runner.projectConfig.SnapshotDigest, f.runner.launchBin, f.runner.launchArgs)
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	// Identical immutable inputs -> identical artifact and digest.
	second, err := buildLaunchArtifact(f.runner.wtPath, f.homeDir, f.taskID, f.runner.projectConfig.SnapshotDigest, f.runner.launchBin, f.runner.launchArgs)
	if err != nil {
		t.Fatalf("second buildLaunchArtifact: %v", err)
	}
	if first.Command != second.Command || first.CommandDigest != second.CommandDigest {
		t.Fatalf("artifact not deterministic: %+v vs %+v", first, second)
	}
	if !domain.IsSHA256(first.CommandDigest) {
		t.Fatalf("command digest not a sha256: %q", first.CommandDigest)
	}

	// Different immutable inputs (a different prompt) -> the existing artifact
	// fails closed instead of being overwritten: identity mismatch.
	diffArgs := append(append([]string{}, f.runner.launchArgs...), "DIFFERENT PROMPT CONTENT")
	if _, err := buildLaunchArtifact(f.runner.wtPath, f.homeDir, f.taskID, f.runner.projectConfig.SnapshotDigest, f.runner.launchBin, diffArgs); err == nil {
		t.Fatal("changed artifact identity must fail closed")
	} else if !strings.Contains(err.Error(), "different content") {
		t.Fatalf("error = %v, want artifact identity mismatch", err)
	}
}

// TestLaunchRecoveryDuplicateLiveTaskFailsClosed proves a duplicate spawn of
// an already-working task fails closed: the committed launch no longer
// matches a re-derived intent (changed snapshot digest) and no second
// endpoint or working record is ever created.
func TestLaunchRecoveryDuplicateLiveTaskFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-duplicate")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("full run: %v", err)
	}
	before := f.aggregate()
	// A NEW spawn invocation derives a different intent (the operator's
	// snapshot/config changed): the live launch identity cannot match.
	f.runner.projectConfig.SnapshotDigest = strings.Repeat("d", 64)
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("duplicate live spawn must fail closed")
	}
	after := f.aggregate()
	if after.Revision != before.Revision {
		t.Fatalf("duplicate spawn mutated the aggregate: rev %d -> %d", before.Revision, after.Revision)
	}
	if after.Endpoint == nil || after.Endpoint.Handle != before.Endpoint.Handle {
		t.Fatalf("duplicate spawn must not create a second endpoint record")
	}
	if f.endpoints.createCount() != 1 {
		t.Fatalf("duplicate spawn created %d endpoints", f.endpoints.createCount())
	}
}
