//go:build darwin || linux

package fleet

import (
	"errors"
	"os"
	"syscall"

	"github.com/minhtri2710/munsu/internal/home"
)

type inspectedProcess struct {
	StartToken     StartToken
	ExecutablePath string
}

func isProcessMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
func inspectProcess(pid int) (inspectedProcess, error) {
	executable, start, err := home.ProcessIdentity(pid)
	if err != nil {
		return inspectedProcess{}, err
	}
	return inspectedProcess{StartToken: StartToken(start), ExecutablePath: executable}, nil
}
func (OSProcessVerifier) VerifyDead(artifact WriterArtifact) (bool, error) {
	current, err := inspectProcess(artifact.PID)
	if isProcessMissing(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if current.StartToken != artifact.StartToken {
		return true, nil
	}
	return false, nil
}
