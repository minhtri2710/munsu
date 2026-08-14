//go:build darwin

package fleet

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// listMarkedProcesses walks the whole process table (not a process group), so
// a process that called setsid() out of its run's session is still seen.
// Processes owned by another user, and processes that exit mid-scan, are
// skipped: their environment is unreadable and no classification can be made.
func listMarkedProcesses() (MarkerScan, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return MarkerScan{}, err
	}
	scan := MarkerScan{Total: len(all)}
	self := os.Getpid()
	for i := range all {
		pid := int(all[i].Proc.P_pid)
		if pid <= 0 || pid == self {
			continue
		}
		executable, environment, err := processEnvironment(pid)
		if err != nil || len(environment) == 0 {
			scan.Unreadable++
			continue
		}
		markers := keepMarkers(environment)
		if !hasOwnershipMarker(markers) {
			continue
		}
		scan.Marked = append(scan.Marked, MarkedProcess{
			PID: pid, PPID: int(all[i].Eproc.Ppid), PGID: int(all[i].Eproc.Pgid),
			ExecutablePath: executable, Markers: markers,
		})
	}
	return scan, nil
}

// processEnvironment reads one process's executable path and environment out
// of kern.procargs2, whose layout is: argc, the executable path, NUL padding,
// argc argument strings, then the environment strings. Reading the block
// directly keeps `ps -E` (which would print every secret in the environment to
// the terminal) out of the scan entirely.
func processEnvironment(pid int) (string, []string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", nil, err
	}
	if len(raw) <= 4 {
		return "", nil, invalidProcessIdentity(pid)
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end <= 0 {
		return "", nil, invalidProcessIdentity(pid)
	}
	executable := string(rest[:end])
	rest = rest[end:]
	tokens := bytes.Split(rest, []byte{0})
	var environment []string
	skipped := 0
	for _, token := range tokens {
		if len(token) == 0 {
			continue
		}
		if skipped < argc {
			skipped++
			continue
		}
		environment = append(environment, string(token))
	}
	return executable, environment, nil
}
