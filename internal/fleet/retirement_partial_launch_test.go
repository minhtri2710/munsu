//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// partialLaunchTeardown captures the metadata presented to the backend while
// reusing the ordinary retirement test backend's exact status and recording.
type partialLaunchTeardown struct {
	*recordingTeardown
	probed   *taskauthority.EndpointBinding
	disposed *taskauthority.EndpointBinding
}

func (t *partialLaunchTeardown) Probe(homeDir string, ep taskauthority.EndpointBinding, harness string) (RetirementEndpointStatus, error) {
	t.probed = &ep
	return t.recordingTeardown.Probe(homeDir, ep, harness)
}

func (t *partialLaunchTeardown) Dispose(req DisposeRequest) error {
	ep := req.Endpoint
	t.disposed = &ep
	return t.recordingTeardown.Dispose(req)
}

// TestRetirePartialLaunchUsesCanonicalResourceEvidence proves an unbound
// launch retires from canonical path, endpoint, and
// manifest evidence.
func TestRetirePartialLaunchUsesCanonicalResourceEvidence(t *testing.T) {
	f := newLaunchFixture(t, "partial-launch-retirement")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
	}

	agg := f.aggregate()
	if agg.Phase != taskauthority.PhaseQueued || agg.Worktree == nil || agg.Launch == nil ||
		agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil || agg.Endpoint != nil {
		t.Fatalf("fixture is not an unbound partial launch: %+v", agg)
	}
	wtPath := agg.Worktree.Path
	teardown := &partialLaunchTeardown{recordingTeardown: &recordingTeardown{alive: true}}
	teardown.recordingTeardown.onDispose = func() {
		if err := os.Remove(filepath.Join(wtPath, BriefName)); err != nil {
			t.Errorf("tamper artifact during disposal: %v", err)
		}
	}
	_, err := RetireTask(Options{HomeDir: f.homeDir, ID: f.taskID}, teardown, fakeRetirementJournals{}, f.auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) || !strings.Contains(err.Error(), "pre-return artifact verification failed") {
		t.Fatalf("RetireTask error = %v, want pending cleanup at the pre-return artifact gate", err)
	}
	want := acquiredBinding(*agg.AcquiredEndpoint)
	if teardown.probed == nil || *teardown.probed != want || teardown.disposed == nil || *teardown.disposed != want {
		t.Errorf("probed=%+v disposed=%+v, want the canonical acquired endpoint %+v", teardown.probed, teardown.disposed, want)
	}
	if len(teardown.recordingTeardown.disposed) != 1 {
		t.Fatalf("disposed=%+v, want only the exact acquired endpoint", teardown.recordingTeardown.disposed)
	}
	if len(teardown.returned) != 0 {
		t.Fatalf("returned=%v, worktree must remain leased after a post-retirement artifact mutation", teardown.returned)
	}

	after := f.aggregate()
	if after.Phase != taskauthority.PhaseRetired || after.CleanupClaim == nil || after.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("aggregate after incomplete cleanup = phase %q claim %+v, want retired with active claim", after.Phase, after.CleanupClaim)
	}
}

// A partial launch never carries a delivery identity; one that does is refused
// before any landed proof, while a partial launch without one goes on to the
// artifact checks.
func TestShipSafetyCheckPartialLaunchRefusesDeliveryIdentity(t *testing.T) {
	const refusal = "partial launch partial-identity cannot carry delivery identity"
	for _, tc := range []struct {
		name    string
		ident   *domain.DeliveryIdentity
		refused bool
	}{
		{name: "partial launch with a delivery identity refused", ident: &domain.DeliveryIdentity{URL: "https://github.com/minhtri2710/munsu/pull/42"}, refused: true},
		{name: "partial launch without a delivery identity checked further"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := testWorktreeBinding(t.TempDir(), strings.Repeat("a", 64))
			_, err := shipSafetyCheck(Options{ID: "partial-identity"}, tc.ident, fakeTeardown{}, nil, binding, true)
			if tc.refused != (err != nil && strings.Contains(err.Error(), refusal)) {
				t.Fatalf("shipSafetyCheck error = %v, want refused=%v", err, tc.refused)
			}
			if !tc.refused && (err == nil || !strings.Contains(err.Error(), "launch artifact verification failed")) {
				t.Fatalf("shipSafetyCheck error = %v, want the artifact check", err)
			}
		})
	}
}

// TestRetireCompletedLaunchStillRequiresLandedBranchProof pins the completed
// launch safety gate: canonical bindings supply the path, but do not replace
// the ordinary landed-branch proof.
func TestRetireCompletedLaunchStillRequiresLandedBranchProof(t *testing.T) {
	f := newLaunchFixture(t, "completed-launch-retirement")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("complete launch: %v", err)
	}

	teardown := &recordingTeardown{alive: true}
	_, err := RetireTask(Options{HomeDir: f.homeDir, ID: f.taskID}, teardown, fakeRetirementJournals{}, f.auth)
	if err == nil || !strings.Contains(err.Error(), "no task branch can be proven pushed") {
		t.Fatalf("RetireTask error = %v, want completed launch refused by the landed-branch gate", err)
	}
	if got := f.aggregate(); got.Phase != taskauthority.PhaseWorking {
		t.Fatalf("completed launch phase = %q, want working after refusal", got.Phase)
	}
	if len(teardown.disposed) != 0 || len(teardown.returned) != 0 {
		t.Fatalf("completed launch refusal released resources: disposed=%v returned=%v", teardown.disposed, teardown.returned)
	}
}
