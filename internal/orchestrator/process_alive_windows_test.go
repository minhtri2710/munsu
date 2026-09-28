//go:build windows

package orchestrator

import "testing"

// TestIsProcessAliveMatchesContract pins the windows half of the
// process-liveness split at the only level this repo measures it: the goos-vet
// lane compiles this file, so the binding proves isProcessAlive exists on
// windows for the AFK lock reclaim path. No lane runs it — whether OpenProcess
// reports a real live process as alive stays unproven here.
func TestIsProcessAliveMatchesContract(t *testing.T) {
	var _ func(int) bool = isProcessAlive
}
