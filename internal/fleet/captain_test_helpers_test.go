package fleet

import "github.com/minhtri2710/munsu/internal/home"

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

func seedFromWorktreeTest(id, h, repo, parent, charter string, force bool, ref string) error {
	return seedFromWorktree(id, h, repo, parent, charter, force, ref, fakeIntegrationPort{})
}
