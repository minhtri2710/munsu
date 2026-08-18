//go:build windows

package orchestrator

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// processIdentity reports the executable path and an opaque start token for pid.
//
// The token exists to separate "this PID" from "this process": PIDs are reused,
// and both callers treat a match as permission to act on the process -- publish
// an identity artifact (publishDaemonIdentity) or terminate it (Return). A blind
// half here means the daemon cannot start at all on windows, which is how this
// half was found: an unconditional error made AcquireLock's only production call
// site roll back its own lock, so state/.lock never named a live PID and Return's
// stop branch was unreachable.
//
// This follows the pattern already in the tree at
// internal/cli/watch_process_windows.go: open a limited-information handle and
// ask the kernel. QueryFullProcessImageName gives the path the unix halves read
// from /proc/<pid>/exe (linux) or kern.procargs2 (darwin);
// GetProcessTimes' creation FILETIME gives what /proc/<pid>/stat field 22 and
// kern.proc.pid's P_starttime give there. The token is the raw 100ns FILETIME as
// a decimal string -- opaque, compared only against another token produced by
// this same function, never parsed or ordered.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it; whether these two
// syscalls return what this comment claims for a real windows process is
// unproven in this repository -- read and compile only, no windows execution.
func processIdentity(pid int) (string, string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", "", fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", "", fmt.Errorf("reading executable path of process %d: %w", pid, err)
	}
	executable := windows.UTF16ToString(buf[:size])
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", "", fmt.Errorf("reading process times of process %d: %w", pid, err)
	}
	start := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if start == 0 {
		return "", "", fmt.Errorf("process %d reported no creation time", pid)
	}
	return executable, fmt.Sprintf("%d", start), nil
}
