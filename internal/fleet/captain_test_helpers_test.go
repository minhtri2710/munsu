package fleet

import "github.com/minhtri2710/munsu/internal/home"

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
func (noopCaptainContinuity) ReconcileTerminal(string, CaptainEndpoint) (CaptainContinuityResult, error) {
	return CaptainContinuityResult{}, nil
}

type noopCaptainMessaging struct{}

func (noopCaptainMessaging) ReconcilePending(string, CaptainEndpoint, home.BoundSender) error {
	return nil
}

type noopCaptainWatcher struct{}

func (noopCaptainWatcher) Status(string) WatcherStatus { return WatcherAbsent }
func (noopCaptainWatcher) Ensure(string, bool) error   { return nil }

type fakeIntegrationPort struct{}

func (fakeIntegrationPort) EnsureCaptain(string) error { return nil }
func (fakeIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return IntegrationStatus{State: "installed"}, nil
}
func seedWithParentTest(id, captainHome, parentHome, charter string) error {
	return SeedCaptain(CaptainSeedOptions{ID: id, Home: captainHome, ParentHome: parentHome, Charter: charter, Integration: fakeIntegrationPort{}})
}
func seedTest(id, captainHome, charter string) error {
	return SeedCaptain(CaptainSeedOptions{ID: id, Home: captainHome, Charter: charter, Integration: fakeIntegrationPort{}})
}
