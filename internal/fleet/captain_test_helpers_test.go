package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func safeStr(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

type noopCaptainContinuity struct{}

func (noopCaptainContinuity) Reconcile(string, CaptainEndpoint) (CaptainContinuityResult, error) {
	return CaptainContinuityResult{}, nil
}

type noopCaptainMessaging struct{}

func (noopCaptainMessaging) ReconcilePending(string, CaptainEndpoint, home.BoundSender) error {
	return nil
}

type noopCaptainWatcher struct{}

func (noopCaptainWatcher) Status(string) WatcherStatus      { return WatcherAbsent }
func (noopCaptainWatcher) LeaseStatus(string) WatcherStatus { return WatcherAbsent }
func (noopCaptainWatcher) Ensure(string, bool) error        { return nil }

type fakeIntegrationPort struct{}

func (fakeIntegrationPort) EnsureCaptain(string, string) error            { return nil }
func (fakeIntegrationPort) CaptainPaths(string, string) ([]string, error) { return nil, nil }
func (fakeIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return IntegrationStatus{State: "installed"}, nil
}

// seedWithParentTest seeds a managed-worktree captain home from a fresh
// fixture project repo.
func seedWithParentTest(t *testing.T, id, captainHome, parentHome, charter string) error {
	t.Helper()
	return SeedCaptain(CaptainSeedOptions{ID: id, Home: captainHome, Repo: newWorktreeFixture(t), ParentHome: parentHome, Charter: charter, Integration: fakeIntegrationPort{}})
}

// seedTest seeds a managed-worktree captain home under its own General home.
func seedTest(t *testing.T, id, captainHome, charter string) error {
	t.Helper()
	parentHome := t.TempDir()
	if _, err := home.Init(parentHome); err != nil {
		t.Fatal(err)
	}
	return seedWithParentTest(t, id, captainHome, parentHome, charter)
}

func seedFromWorktreeTest(id, h, repo, parent, charter string, force bool, ref string) error {
	return seedFromWorktree(id, h, repo, parent, charter, force, ref, fakeIntegrationPort{})
}

type countingIntegrationPort struct {
	calls int
	err   error
}

func (p *countingIntegrationPort) EnsureCaptain(string, string) error { p.calls++; return p.err }
func (p *countingIntegrationPort) CaptainPaths(string, string) ([]string, error) {
	return nil, nil
}
func (p *countingIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return IntegrationStatus{}, nil
}
