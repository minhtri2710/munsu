//go:build linux

package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func listWriterProcesses(canonicalHome string) ([]WriterProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []WriterProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		raw := strings.Split(string(data), "\x00")
		args := raw[:0]
		for _, arg := range raw {
			if arg != "" {
				args = append(args, arg)
			}
		}
		kind := writerKindForArgs(args, canonicalHome)
		if kind == "" {
			continue
		}
		identity, err := inspectProcess(pid)
		if err != nil {
			if isProcessMissing(err) {
				continue
			}
			return nil, err
		}
		processes = append(processes, WriterProcess{PID: pid, StartToken: identity.StartToken, ExecutablePath: identity.ExecutablePath, CanonicalHome: canonicalHome, Kind: kind})
	}
	return processes, nil
}
