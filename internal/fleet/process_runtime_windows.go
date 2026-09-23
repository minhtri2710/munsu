//go:build windows

package fleet

import "fmt"

type inspectedProcess struct {
	StartToken     StartToken
	ExecutablePath string
}

func listWriterProcesses(string) ([]WriterProcess, error) { return nil, ErrProcessInventoryUnsupported }
func inspectProcess(pid int) (inspectedProcess, error) {
	return inspectedProcess{}, fmt.Errorf("%w: PID %d", ErrProcessInventoryUnsupported, pid)
}

// VerifyDead cannot prove a writer dead without a process inventory, so it
// reports the unsupported-inventory error and never a death.
func (OSProcessVerifier) VerifyDead(artifact WriterArtifact) (bool, error) {
	_, err := inspectProcess(artifact.PID)
	return false, err
}
