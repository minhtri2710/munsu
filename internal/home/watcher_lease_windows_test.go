//go:build windows

package home

import "testing"

// TestIsProcessAliveMatchesLeaseContract pins the windows half of the
// process-liveness split at the only level this repo measures it: the goos-vet
// lane compiles this file, so the binding proves isProcessAlive exists on
// windows with the shape ClaimWatcherLease and ReadWatcherLease take. No lane
// runs it — whether OpenProcess reports a real live lease holder as alive stays
// unproven here.
func TestIsProcessAliveMatchesLeaseContract(t *testing.T) {
	var _ func(int) bool = isProcessAlive
}
