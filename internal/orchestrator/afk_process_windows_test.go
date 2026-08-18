//go:build windows

package orchestrator

import "testing"

// TestStopProcessMatchesReturnContract pins the windows half of the AFK stop
// split at the only level this repo measures it: the goos-vet lane compiles
// this file, so the binding proves stopProcess exists on windows with the shape
// Return calls (afk_return.go). No lane runs it — whether the hard terminate
// actually stops a live AFK daemon stays unproven here.
func TestStopProcessMatchesReturnContract(t *testing.T) {
	var _ func(int) error = stopProcess
}

// TestStopProcessIsLossyMatchesReturnContract pins the windows half of the
// lossiness split the same way: stopProcessIsLossy exists on windows with the
// shape Return consults. Its VALUE on windows is asserted by the
// platform-neutral TestStopProcessLossinessMatchesPlatform
// (afk_process_lossy_test.go), which required lanes compile
// (GOOS=windows go vet ./...) and only the windows-observation lane runs.
func TestStopProcessIsLossyMatchesReturnContract(t *testing.T) {
	var _ func() bool = stopProcessIsLossy
}
