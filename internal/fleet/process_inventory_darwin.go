//go:build darwin

package fleet

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func listWriterProcesses(canonicalHome string) ([]WriterProcess, error) {
	cmd := exec.Command("ps", "-axo", "pid=,command=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var processes []WriterProcess
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		split := strings.IndexByte(line, ' ')
		if split < 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line[:split]))
		if err != nil || pid == os.Getpid() {
			continue
		}
		args := splitProcessCommand(strings.TrimSpace(line[split+1:]))
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
