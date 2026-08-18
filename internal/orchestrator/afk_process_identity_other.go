//go:build !darwin && !linux && !windows

package orchestrator

import "fmt"

// processIdentity is deliberately unimplemented outside darwin, linux and
// windows, and the error is the correct behaviour rather than a gap to fill.
//
// The three implemented halves each read a kernel interface this repository can
// name (/proc, kern.procargs2 + kern.proc.pid, QueryFullProcessImageName +
// GetProcessTimes). For any other GOOS there is no measured equivalent here, and
// the callers read a successful return as permission: publishDaemonIdentity
// writes an identity artifact other processes trust, and Return uses the token to
// decide whether it may terminate a PID. Returning a placeholder or a constant
// would make both of those answer yes without evidence, so this half fails
// closed -- the AFK daemon refuses to start rather than run unidentifiable.
//
// This half must survive any future windows work: the windows split lives in
// afk_process_identity_windows.go, and narrowing this build tag to cover windows
// again would silently re-open the same hole.
func processIdentity(pid int) (string, string, error) {
	return "", "", fmt.Errorf("AFK process identity unsupported for PID %d", pid)
}
