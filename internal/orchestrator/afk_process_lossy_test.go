package orchestrator

import (
	"runtime"
	"testing"
)

// TestStopProcessLossinessMatchesPlatform pins stopProcessIsLossy to the GOOS
// split that defines it: true where the stop mechanism cannot run the daemon's
// deferred digest flush (windows, TerminateProcess), false where it can (unix,
// SIGTERM). It runs on every platform that compiles it, so the ubuntu lanes
// and this machine execute the unix half; the windows half (true) executes
// only when the windows-observation lane
// (.github/workflows/windows-observation.yml, workflow_dispatch, not
// required) is dispatched -- on this repository's required lanes the windows
// build is compiled by `GOOS=windows go vet ./...` and never executed, exactly
// like stopProcess itself.
func TestStopProcessLossinessMatchesPlatform(t *testing.T) {
	want := runtime.GOOS == "windows"
	if got := stopProcessIsLossy(); got != want {
		t.Fatalf("stopProcessIsLossy() = %v on %s, want %v", got, runtime.GOOS, want)
	}
}
