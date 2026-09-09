package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
)

// worktreePoolScope is the home-level fenced lock that serializes worktree-pool
// mutation between `worktree reclaim` and the spawn runner's lease. It closes
// the spawn/reclaim reservation race that per-task authority locks cannot:
// reclaim spans every task and the lease (backend.GetWorktreeReserved) holds no
// task lock, so a slot free at reclaim's status snapshot can be leased by a
// concurrent launch and then returned out from under it. Under this fence the
// lease and reclaim's whole snapshot->return pass cannot interleave, so reclaim
// either sees the new lease_holder (and spares it) or runs entirely before the
// lease (which then allocates a fresh slot).
//
// The fence is held across the lease only, never across the later BindWorktree
// (which takes the per-task authority lock), so the pool scope and a task scope
// are never held at once and there is no lock-ordering inversion.
const worktreePoolScope = "worktree-pool"

// LockWorktreePool acquires the home-level worktree-pool fence. The caller must
// call Release on the returned lock exactly once (defer it).
func LockWorktreePool(homeDir string) (*home.Lock, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, fmt.Errorf("locking worktree pool: opening home: %w", err)
	}
	lk, err := h.Lock(worktreePoolScope)
	if err != nil {
		return nil, fmt.Errorf("locking worktree pool: %w", err)
	}
	return lk, nil
}
