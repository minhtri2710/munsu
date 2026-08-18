package fleet

// Test-only surface. These helpers have no production caller: keeping them in
// a _test.go file is what stops them from re-entering the binary's reachable
// set (and the deadcode ledger) while the tests that need them keep compiling.

// SetDeliveryCrashHookForTest installs a crash-boundary hook for subprocess
// tests. Boundary names are the durable stages of one delivery: journal,
// authorized, mutating, outcome, completed.
func SetDeliveryCrashHookForTest(hook func(string)) func() {
	previous := deliveryCrashHook
	if hook == nil {
		deliveryCrashHook = func(string) {}
	} else {
		deliveryCrashHook = hook
	}
	return func() { deliveryCrashHook = previous }
}

// bindingOwnerOf is the reverse binding lookup (project → captain) that the
// registry tests assert on. Production only ever reads the forward direction,
// Registry.ProjectOf; the reverse one exists for assertions, not for the
// binary, so it lives here.
func bindingOwnerOf(r *Registry, projectID string) (string, error) {
	doc, err := r.readBindingDoc()
	if err != nil {
		return "", err
	}
	for _, b := range doc.Bindings {
		if b.ProjectID == projectID {
			return b.CaptainID, nil
		}
	}
	return "", nil
}

// configPush copies inheritable config from the parent home to the captain
// home, discarding the generation-tracking result. Production callers use
// configPushWithResult, which is the only variant that can report the
// committed generation.
func configPush(parentHome, captainHome string) error {
	_, err := configPushWithResult(parentHome, captainHome)
	return err
}
