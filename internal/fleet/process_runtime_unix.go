//go:build darwin || linux

package fleet

import (
	"errors"
	"os"
	"syscall"
)

type inspectedProcess struct {
	StartToken     StartToken
	ExecutablePath string
}

func isProcessMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
func inspectProcess(pid int) (inspectedProcess, error) {
	executable, start, err := platformProcessIdentity(pid)
	if err != nil {
		return inspectedProcess{}, err
	}
	return inspectedProcess{StartToken: StartToken(start), ExecutablePath: executable}, nil
}
func invalidProcessIdentity(pid int) error { return errors.New("invalid process identity") }
