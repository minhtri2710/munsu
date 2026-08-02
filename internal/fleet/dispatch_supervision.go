package fleet

import (
	"github.com/minhtri2710/munsu/internal/home"
)

// CheckSupervisionForDispatch is the orchestration supervision gate for a
// dispatch action. It fails closed with home.ErrUnhealthyWatcher when the
// home's watcher lease is degraded for the action. It is a read-only check:
// it never evaluates or mutates durable Dispatch Holds and never touches a
// task phase — Task Authority owns those (ADR-0007 §8, Task 4.3).
func CheckSupervisionForDispatch(homeDir string, action home.DispatchAction) error {
	return home.CheckWatcherHealthForDispatch(homeDir, action)
}
