//go:build linux

package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func processIdentity(pid int) (string, string, error) {
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", "", fmt.Errorf("reading process executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", fmt.Errorf("reading process start: %w", err)
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return "", "", fmt.Errorf("process stat is truncated")
	}
	return executable, fields[19], nil
}
