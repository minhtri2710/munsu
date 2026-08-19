//go:build windows

package cli

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// readParentPID reads the parent PID of the given PID on Windows.
//
// It uses the Toolhelp32 process-snapshot API, which needs no handle to the
// target process and therefore no elevation. The snapshot lists every process
// once; the entry whose ProcessID matches pid carries the parent in
// ParentProcessID. A missing process or a snapshot failure returns -1 so
// callers fail closed rather than report a parent they did not observe.
func readParentPID(pid int) int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return -1
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return -1
	}
	for {
		if entry.ProcessID == uint32(pid) {
			return int(entry.ParentProcessID)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return -1
		}
	}
}
