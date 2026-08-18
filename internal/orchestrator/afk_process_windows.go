//go:build windows

package orchestrator

import "os"

// stopProcess terminates the AFK daemon identified by pid.
//
// Windows has no SIGTERM, so the unix half's graceful signal has no equivalent
// here: Kill is a hard terminate, and Daemon.Start's deferred and post-signal
// shutdown steps (afk_daemon.go) never run. Four things the daemon holds are
// skipped, and Return (afk_return.go) compensates for three of them and
// surfaces the fourth:
//
//   - identity lock state/.lock       -- compensated: afk_return.go removes it
//   - consent flag state/.afk         -- compensated: Disable, afk_return.go
//   - writer identity "afk"           -- compensated since the processIdentity
//     fix: the deferred clearDaemonIdentity (afk_daemon.go) still never runs, but
//     Return calls it explicitly once the exit is confirmed, using the identity
//     it already read to authorize the stop
//   - unflushed digest entries        -- surfaced, not compensated: Digester
//     holds entries in memory (afk_digester.go) and writes state/.afk-digest
//     only when the window expires (60s by default) or at Start's step 7
//     flush. Kill skips that flush, so up to one window of escalations never
//     reaches the digest and cannot be drained. stopProcessIsLossy reports
//     this, Return carries it into the report, and ReturnReport refuses to
//     claim "All clear" over a state the digest does not reflect
//     (afk_return.go)
//
// So this is a lossy stop, not an equivalent of the unix half. It was inert
// while processIdentity had no windows half -- Daemon.Start aborted before the
// lock survived, readDaemonPID returned 0, and Return's stop branch never ran.
// afk_process_identity_windows.go removed that gate, so this path is now live
// on windows and the loss it causes is real. Closing it means giving windows a
// soft stop the daemon can observe (a job object, a named event, or a control
// message) so step 7's flush runs; #530 ruled that out in favor of surfacing
// the loss (stopProcessIsLossy + ReturnReport refusing "All clear"), and the
// removal condition for that choice is recorded next to the wiring in
// afk_return.go.
//
// No required lane runs this. `GOOS=windows go vet ./...` compiles it; the
// windows-observation lane (.github/workflows/windows-observation.yml,
// workflow_dispatch, not required) is the only thing that executes it when
// dispatched. Compile is not execution -- its effect on a live daemon stays
// unproven in this repository.
func stopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

// stopProcessIsLossy reports whether stopProcess can lose daemon state that a
// graceful shutdown would have written. On windows it can: Kill is
// TerminateProcess, which skips the daemon's deferred flush (afk_daemon.go
// step 7), so up to one digest window of entries never reaches
// state/.afk-digest and cannot be drained by Return. Return consults this
// predicate to decide whether the stop must be surfaced as lossy
// (afk_return.go). The value here -- true -- is asserted by
// TestStopProcessLossinessMatchesPlatform (afk_process_lossy_test.go), which
// required lanes compile (GOOS=windows go vet ./...) and only the
// windows-observation lane runs; like stopProcess above it is never executed
// on this repository's required lanes.
func stopProcessIsLossy() bool { return true }
