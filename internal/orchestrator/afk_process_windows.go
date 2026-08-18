//go:build windows

package orchestrator

import "os"

// stopProcess terminates the AFK daemon identified by pid.
//
// Windows has no SIGTERM, so the unix half's graceful signal has no equivalent
// here: Kill is a hard terminate and the daemon gets no chance to clear its own
// consent flag and identity lock. That is safe at the only production call site
// because Return (afk_return.go) removes the identity lock and clears the flag
// itself once the stop is confirmed, and drains the durable digest queue after.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it, and
// afk_process_windows_test.go binds it at the shape stopProcess call sites use;
// its effect on a live daemon stays unproven in this repository.
func stopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
