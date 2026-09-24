//go:build windows

package home

import "testing"

// TestIsProcessAliveMatchesContract pins the windows half of the
// process-liveness split at the only level this repo measures it: the goos-vet
// lane compiles this file, so the binding proves IsProcessAlive exists on
// windows with the shape ClaimWatcherLease, ReadWatcherLease and orchestrator's
// exit waits take. No lane runs it — whether OpenProcess reports a real live
// process as alive stays unproven here.
func TestIsProcessAliveMatchesContract(t *testing.T) {
	var _ func(int) bool = IsProcessAlive
}
