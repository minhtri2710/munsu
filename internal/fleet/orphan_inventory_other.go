//go:build !darwin && !linux

package fleet

func listMarkedProcesses() (MarkerScan, error) { return MarkerScan{}, ErrProcessInventoryUnsupported }
