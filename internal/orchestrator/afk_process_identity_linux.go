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
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", err
	}
	startToken, err := parseLinuxProcessStat(data)
	if err != nil {
		return "", "", err
	}
	return executable, startToken, nil
}

func parseLinuxProcessStat(data []byte) (string, error) {
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) <= 19 {
		return "", fmt.Errorf("truncated process stat")
	}
	return fields[19], nil
}
