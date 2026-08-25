package bootstrap

import (
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
)

// AssertSupportedHarness checks that name is a known harness with supported
// integration capabilities (test helper).
func AssertSupportedHarness(name string) error {
	if name == "" {
		return fmt.Errorf("no harness specified and automatic detection failed")
	}
	if !harness.IsKnownHarness(name) {
		return fmt.Errorf("unknown harness %q: must be one of %v", name, harness.KnownHarnesses)
	}
	caps := EnabledCapabilities(name)
	if len(caps) == 0 {
		return fmt.Errorf("harness %q is recognised but has no integration capabilities yet", name)
	}
	return nil
}

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

// SetCapabilityCommandRunner overrides the capability command runner (for tests).
func SetCapabilityCommandRunner(fn func(name string, args []string, dir string, timeout time.Duration) (string, error)) func(name string, args []string, dir string, timeout time.Duration) (string, error) {
	prev := runCapabilityCommand
	runCapabilityCommand = fn
	return prev
}

// ResetCapabilityCommandRunner restores the default runner (for tests).
func ResetCapabilityCommandRunner() {
	runCapabilityCommand = runCapabilityCommandDefault
}
