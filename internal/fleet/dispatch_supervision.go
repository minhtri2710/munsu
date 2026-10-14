package fleet

import (
	"github.com/minhtri2710/munsu/internal/home"
)

// CheckSupervisionForDispatch is the orchestration supervision gate for
// dispatch. It fails closed with home.ErrUnhealthyWatcher when the home's
// watcher lease is degraded. It is a read-only check:
// it never evaluates or mutates durable Dispatch Holds and never touches a
// task phase — Task Authority owns those.
func CheckSupervisionForDispatch(homeDir string) error {
	return home.CheckWatcherHealthForDispatch(homeDir)
}
