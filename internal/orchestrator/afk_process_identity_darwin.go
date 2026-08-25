//go:build darwin

package orchestrator

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
		return "", "", err
	}
	executable, err := parseDarwinProcessArgs(raw)
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", "", err
	}
	start := info.Proc.P_starttime
	return executable, fmt.Sprintf("%d:%d", start.Sec, start.Usec), nil
}

func parseDarwinProcessArgs(raw []byte) (string, error) {
	if len(raw) <= 4 {
		return "", fmt.Errorf("truncated process args")
	}
	_ = binary.LittleEndian.Uint32(raw[:4])
	end := bytes.IndexByte(raw[4:], 0)
	if end <= 0 {
		return "", fmt.Errorf("missing executable")
	}
	return string(raw[4 : 4+end]), nil
}
