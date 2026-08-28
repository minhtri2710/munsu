//go:build windows

package testutil

import (
	"golang.org/x/sys/windows"
)

// stillActiveWindows (259, 0x103) is the Win32 STILL_ACTIVE status code indicating
// that a process is still running. Note that 259 is also a legal exit code if a process
// terminates with ExitCode 259, so an exited process returning 259 reads as active;
// this is a known, accepted limitation of the GetExitCodeProcess Win32 API.
const stillActiveWindows = 259

// IsProcessAlive reports whether a process with the given PID is running.
func IsProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == stillActiveWindows
}
