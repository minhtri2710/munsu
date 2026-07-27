//go:build darwin

package afk

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
	if len(raw) <= 4 {
		return "", "", fmt.Errorf("truncated process args")
	}
	_ = binary.LittleEndian.Uint32(raw[:4])
	end := bytes.IndexByte(raw[4:], 0)
	if end <= 0 {
		return "", "", fmt.Errorf("missing executable")
	}
	executable := string(raw[4 : 4+end])
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
