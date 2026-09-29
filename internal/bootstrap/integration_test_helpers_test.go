package bootstrap

import "time"

// SetMunsuPathResolver sets a custom resolver (for testing).
func SetMunsuPathResolver(r MunsuPathResolver) { munsuResolver = r }

// ResetMunsuPathResolver restores the default resolver (for testing).
func ResetMunsuPathResolver() { munsuResolver = defaultMunsuResolver{} }

type testMunsuResolver struct {
	path string
}

func (r testMunsuResolver) Resolve() (string, error) {
	return r.path, nil
}

// SetProbeTimeout sets the capability probe timeout for testing.
func SetProbeTimeout(d time.Duration) time.Duration {
	prev := capabilityProbeTimeout
	capabilityProbeTimeout = d
	return prev
}

// SetCapabilityCommandRunner overrides the capability command runner (for
// tests) and returns a func that restores the previous one.
func SetCapabilityCommandRunner(fn func(name string, args []string, dir string, timeout time.Duration) (string, error)) func() {
	prev := runCapabilityCommand
	runCapabilityCommand = fn
	return func() { runCapabilityCommand = prev }
}

// ResetCapabilityCommandRunner restores the default runner (for tests).
func ResetCapabilityCommandRunner() {
	runCapabilityCommand = runCapabilityCommandDefault
}
