//go:build darwin

package supervision

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (string, string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", "", fmt.Errorf("reading process args: %w", err)
	}
	if len(raw) <= 4 {
		return "", "", fmt.Errorf("process args are truncated")
	}
	_ = binary.LittleEndian.Uint32(raw[:4])
	end := bytes.IndexByte(raw[4:], 0)
	if end <= 0 {
		return "", "", fmt.Errorf("process executable is missing")
	}
	executable := string(raw[4 : 4+end])
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", "", fmt.Errorf("reading process start: %w", err)
	}
	start := info.Proc.P_starttime
	return executable, fmt.Sprintf("%d:%d", start.Sec, start.Usec), nil
}
