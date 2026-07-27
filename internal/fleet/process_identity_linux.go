//go:build linux

package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func platformProcessIdentity(pid int) (string, string, error) {
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", "", invalidProcessIdentity(pid)
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return "", "", invalidProcessIdentity(pid)
	}
	return executable, fields[19], nil
}
