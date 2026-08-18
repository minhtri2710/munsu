//go:build windows

package orchestrator

import "testing"

// TestIsProcessAliveMatchesPolicyContract pins the windows half of the
// process-liveness split at the only level this repo measures it: the goos-vet
// lane compiles this file, so the binding proves isProcessAlive exists on
// windows with the shape home.WatcherLockPolicy.ProcessAlive and the AFK lock
// reclaim path both take. No lane runs it — whether OpenProcess reports a real
// live watcher as alive stays unproven here.
func TestIsProcessAliveMatchesPolicyContract(t *testing.T) {
	var _ func(int) bool = isProcessAlive
}
