package fleet

import (
	"os"
	"strconv"
	"strings"
)

// readSyscalls reports how many read syscalls this process has issued, from
// /proc/self/io. The second result is false when the kernel does not expose the
// counter, which is what callers gate the cost-shape guard on.
func readSyscalls() (int64, bool) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "syscr:")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
