package fleet

import (
	"strings"
	"testing"
)

// TestSoldierHarnessPreflightRefusesUnknownHarness pins that the shared harness
// readiness refuses a name that is not a known adapter, before any launch.
func TestSoldierHarnessPreflightRefusesUnknownHarness(t *testing.T) {
	err := SoldierHarnessPreflight(t.TempDir(), "no-such-harness")
	if err == nil || !strings.Contains(err.Error(), "not a known adapter") {
		t.Fatalf("err = %v, want not a known adapter", err)
	}
}
