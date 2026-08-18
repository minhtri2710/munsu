//go:build windows

package orchestrator

import "os"

// stopProcess terminates the AFK daemon identified by pid.
//
// Windows has no SIGTERM, so the unix half's graceful signal has no equivalent
// here: Kill is a hard terminate, and Daemon.Start's deferred and post-signal
// shutdown steps (afk_daemon.go) never run. Four things the daemon holds are
// skipped, and Return (afk_return.go) compensates for two of them:
//
//   - identity lock state/.lock       -- compensated: afk_return.go removes it
//   - consent flag state/.afk         -- compensated: Disable, afk_return.go
//   - writer identity "afk"           -- compensated since the processIdentity
//     fix: the deferred clearDaemonIdentity (afk_daemon.go) still never runs, but
//     Return calls it explicitly once the exit is confirmed, using the identity
//     it already read to authorize the stop
//   - unflushed digest entries        -- NOT compensated: Digester holds entries
//     in memory (afk_digester.go) and writes state/.afk-digest only when the
//     window expires (60s by default) or at Start's step 7 flush. Kill skips that
//     flush, so up to one window of escalations is lost, and Return then drains a
//     file missing exactly those entries -- ReturnReport can read "all clear" over
//     a state that is not
//
// So this is a lossy stop, not an equivalent of the unix half. It was inert
// while processIdentity had no windows half -- Daemon.Start aborted before the
// lock survived, readDaemonPID returned 0, and Return's stop branch never ran.
// afk_process_identity_windows.go removed that gate, so this path is now live on
// windows and the remaining loss is real: up to one digest window of escalations
// can be dropped by a stop that Return then reports as "all clear". That is
// accepted deliberately and not silently -- see ReturnReport (afk_return.go).
// Closing it means giving windows a soft stop the daemon can observe (a job
// object, a named event, or a control message) so step 7's flush runs, which is
// a design change, not a fix to this function.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it; its effect on a
// live daemon stays unproven in this repository -- read and compile only, no
// windows execution.
func stopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
