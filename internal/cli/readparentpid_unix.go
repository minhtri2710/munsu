//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// readParentPID reads the parent PID of the given PID.
// Tries /proc first (Linux), falls back to `ps` (macOS/BSD).
func readParentPID(pid int) int {
	// Try /proc/[pid]/status (Linux)
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	if data, err := os.ReadFile(statusPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				var ppid int
				if _, err := fmt.Sscanf(line, "PPid:%d", &ppid); err == nil {
					return ppid
				}
			}
		}
	}

	// Fallback to `ps -o ppid= -p <pid>` (macOS/BSD)
	cmd := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	ppidStr := strings.TrimSpace(string(out))
	if ppidStr == "" {
		return -1
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil || ppid <= 0 {
		return -1
	}
	return ppid
}
