//go:build linux

package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listMarkedProcesses walks the whole process table (not a process group), so
// a process that called setsid() out of its run's session is still seen.
// Processes owned by another user, and processes that exit mid-scan, are
// skipped: their environment is unreadable and no classification can be made.
func listMarkedProcesses() (MarkerScan, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return MarkerScan{}, err
	}
	scan := MarkerScan{}
	self := os.Getpid()
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		scan.Total++
		if pid == self {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			scan.Unreadable++
			continue
		}
		markers := keepMarkers(strings.Split(string(raw), "\x00"))
		if !hasOwnershipMarker(markers) {
			continue
		}
		ppid, pgid := processGroupIdentity(entry.Name())
		executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			executable = ""
		}
		scan.Marked = append(scan.Marked, MarkedProcess{
			PID: pid, PPID: ppid, PGID: pgid, ExecutablePath: executable, Markers: markers,
		})
	}
	return scan, nil
}

// processGroupIdentity reads ppid and pgid out of /proc/<pid>/stat. The comm
// field is parenthesised and may contain spaces, so parsing starts after its
// closing parenthesis. An unreadable stat yields zeroes: the report prints the
// process without its parentage rather than dropping the finding.
func processGroupIdentity(pid string) (int, int) {
	raw, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		return 0, 0
	}
	commEnd := strings.LastIndexByte(string(raw), ')')
	if commEnd < 0 {
		return 0, 0
	}
	fields := strings.Fields(string(raw)[commEnd+1:])
	if len(fields) < 3 {
		return 0, 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	pgid, _ := strconv.Atoi(fields[2])
	return ppid, pgid
}
