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
//   - writer identity "afk"           -- NOT compensated: the artifact published
//     by publishDaemonIdentity is cleared only by the deferred
//     clearDaemonIdentity (afk_daemon.go:80), so a killed daemon leaves it behind
//   - unflushed digest entries        -- NOT compensated: Digester holds entries
//     in memory (afk_digester.go) and writes state/.afk-digest only when the
//     window expires (60s by default) or at Start's step 7 flush. Kill skips that
//     flush, so up to one window of escalations is lost, and Return then drains a
//     file missing exactly those entries -- ReturnReport can read "all clear" over
//     a state that is not
//
// So this is a lossy stop, not an equivalent of the unix half. Both uncompensated
// losses are inert today only because nothing reaches this function on windows:
// Daemon.Start aborts before the lock survives, since publishDaemonIdentity calls
// processIdentity, whose !darwin && !linux half (afk_process_identity_other.go)
// always errors and triggers d.lock.Release(). readDaemonPID therefore returns 0
// and Return's stop branch is skipped. Whoever fixes processIdentity makes this
// path live and inherits both losses; they are the reason to fix them there.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it; its effect on a
// live daemon stays unproven in this repository.
func stopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
