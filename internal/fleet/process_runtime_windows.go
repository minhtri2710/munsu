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
func isProcessMissing(error) bool { return false }
