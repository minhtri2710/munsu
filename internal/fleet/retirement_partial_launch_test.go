//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// partialLaunchTeardown captures the metadata presented to the backend while
// reusing the ordinary retirement test backend's exact status and recording.
type partialLaunchTeardown struct {
	*recordingTeardown
	probedMeta   map[string]string
	disposedMeta map[string]string
}

func (t *partialLaunchTeardown) Probe(homeDir string, meta map[string]string) (RetirementEndpointStatus, error) {
	t.probedMeta = cloneRetirementMeta(meta)
	return t.recordingTeardown.Probe(homeDir, meta)
}

func (t *partialLaunchTeardown) Dispose(homeDir string, meta map[string]string, req DisposeRequest) error {
	t.disposedMeta = cloneRetirementMeta(meta)
	return t.recordingTeardown.Dispose(homeDir, meta, req)
}

func cloneRetirementMeta(meta map[string]string) map[string]string {
	cloned := make(map[string]string, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

// TestRetirePartialLaunchUsesCanonicalResourceEvidence proves an unbound
// launch can retire from canonical path, endpoint, and manifest evidence while
// .meta has no resource projections.
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
	if err := home.WriteMeta(f.homeDir, f.taskID, map[string]string{
		"project":     "test-proj",
		"generation":  "1",
		"state":       "queued",
		"repo":        f.repoPath,
		"owner":       "general",
		"description": "Ready",
		"kind":        taskauthority.KindShip,
	}); err != nil {
		t.Fatal(err)
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
	for key, want := range map[string]string{
		"backend":            agg.AcquiredEndpoint.Backend,
		"window":             agg.AcquiredEndpoint.Handle,
		"herdr_session":      agg.AcquiredEndpoint.SessionOwner,
		"herdr_workspace_id": agg.AcquiredEndpoint.WorkspaceID,
		"herdr_tab_id":       agg.AcquiredEndpoint.TabID,
	} {
		if teardown.probedMeta[key] != want || teardown.disposedMeta[key] != want {
			t.Errorf("backend metadata %s: probed=%q disposed=%q, want canonical %q", key, teardown.probedMeta[key], teardown.disposedMeta[key], want)
		}
	}
	if len(teardown.disposed) != 1 || teardown.disposed[0].Handle != agg.AcquiredEndpoint.Handle ||
		teardown.disposed[0].Backend != agg.AcquiredEndpoint.Backend {
		t.Fatalf("disposed=%+v, want only the exact acquired endpoint", teardown.disposed)
	}
	if len(teardown.returned) != 0 {
		t.Fatalf("returned=%v, worktree must remain leased after a post-retirement artifact mutation", teardown.returned)
	}

	after := f.aggregate()
	if after.Phase != taskauthority.PhaseRetired || after.CleanupClaim == nil || after.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("aggregate after incomplete cleanup = phase %q claim %+v, want retired with active claim", after.Phase, after.CleanupClaim)
	}
	meta, err := home.ReadMeta(f.homeDir, f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"worktree", "launch_manifest_sha256", "backend", "window"} {
		if _, ok := meta[key]; ok {
			t.Errorf("retirement rewrote missing projection field %q into .meta: %v", key, meta)
		}
	}
}

// TestRetireCompletedLaunchStillRequiresLandedBranchProof pins the completed
// launch safety gate: canonical bindings supply the path, but do not replace
// the ordinary landed-branch proof.
func TestRetirePartialLaunchRefusesDeliveryIdentityBeforeMutation(t *testing.T) {
	f := newLaunchFixture(t, "partial-launch-with-identity")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
	}
	if err := home.WriteMeta(f.homeDir, f.taskID, map[string]string{
		"project": "test-proj", "generation": "1", "state": "queued", "repo": f.repoPath,
		"owner": "general", "description": "Ready", "kind": taskauthority.KindShip,
		"pr_url": "https://github.com/minhtri2710/munsu/pull/42",
	}); err != nil {
		t.Fatal(err)
	}

	teardown := &recordingTeardown{alive: true}
	_, err := RetireTask(Options{HomeDir: f.homeDir, ID: f.taskID}, teardown, fakeRetirementJournals{}, f.auth)
	if err == nil || !strings.Contains(err.Error(), "partial launch partial-launch-with-identity cannot carry delivery identity") {
		t.Fatalf("RetireTask error = %v, want partial-launch identity refusal", err)
	}
	if len(teardown.disposed) != 0 || len(teardown.returned) != 0 {
		t.Fatalf("identity refusal released resources: disposed=%v returned=%v", teardown.disposed, teardown.returned)
	}
	after := f.aggregate()
	if after.Phase != taskauthority.PhaseQueued || after.CleanupClaim != nil {
		t.Fatalf("identity refusal mutated canonical state: phase=%q claim=%+v", after.Phase, after.CleanupClaim)
	}
}

func TestRetireTaskAuthoritativelyRefusesPartialLaunchDeliveryIdentity(t *testing.T) {
	f := newLaunchFixture(t, "partial-launch-authoritative-identity")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
	}
	if err := home.WriteMeta(f.homeDir, f.taskID, map[string]string{
		"project": "test-proj", "generation": "1", "state": "queued", "repo": f.repoPath,
		"owner": "general", "description": "Ready", "kind": taskauthority.KindShip,
		"pr_url": "https://github.com/minhtri2710/munsu/pull/42",
	}); err != nil {
		t.Fatal(err)
	}

	meta, err := home.ReadMeta(f.homeDir, f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = retireTaskAuthoritatively(Options{HomeDir: f.homeDir, ID: f.taskID}, meta, f.auth)
	if err == nil || !strings.Contains(err.Error(), "partial launch partial-launch-authoritative-identity cannot carry delivery identity") {
		t.Fatalf("retireTaskAuthoritatively error = %v, want partial-launch identity refusal", err)
	}
	if after := f.aggregate(); after.Phase != taskauthority.PhaseQueued || after.CleanupClaim != nil {
		t.Fatalf("refusal mutated canonical state: phase=%q claim=%+v", after.Phase, after.CleanupClaim)
	}
}

func TestShipSafetyCheckPartialLaunchRefusesDeliveryIdentity(t *testing.T) {
	meta := map[string]string{"pr_url": "https://github.com/minhtri2710/munsu/pull/42"}
	binding := testWorktreeBinding(t.TempDir(), strings.Repeat("a", 64))
	_, err := shipSafetyCheck(Options{ID: "partial-identity"}, meta, fakeTeardown{}, nil, binding, true)
	if err == nil || !strings.Contains(err.Error(), "partial launch partial-identity cannot carry delivery identity") {
		t.Fatalf("shipSafetyCheck error = %v, want partial-launch identity refusal", err)
	}
}

func TestRetireCompletedLaunchStillRequiresLandedBranchProof(t *testing.T) {
	f := newLaunchFixture(t, "completed-launch-retirement")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("complete launch: %v", err)
	}
	if err := home.WriteMeta(f.homeDir, f.taskID, map[string]string{
		"project":     "test-proj",
		"generation":  "1",
		"state":       "working",
		"repo":        f.repoPath,
		"owner":       "general",
		"description": "Ready",
		"kind":        taskauthority.KindShip,
	}); err != nil {
		t.Fatal(err)
	}

	teardown := &recordingTeardown{alive: true}
	_, err := RetireTask(Options{HomeDir: f.homeDir, ID: f.taskID}, teardown, fakeRetirementJournals{}, f.auth)
	if err == nil || !strings.Contains(err.Error(), "branch has no remote tracking branch") {
		t.Fatalf("RetireTask error = %v, want completed launch refused by the landed-branch gate", err)
	}
	if got := f.aggregate(); got.Phase != taskauthority.PhaseWorking {
		t.Fatalf("completed launch phase = %q, want working after refusal", got.Phase)
	}
	if len(teardown.disposed) != 0 || len(teardown.returned) != 0 {
		t.Fatalf("completed launch refusal released resources: disposed=%v returned=%v", teardown.disposed, teardown.returned)
	}
}
