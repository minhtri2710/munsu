package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestNoNewTaskAuthorityReachThrough pins the empty allowlist of fleet
// production files that may call legacy home task-authority mutations during
// the Task Authority migration (ADR-0007). Task 7.8 removed the final
// allowlisted reach-through (`spawn_runner.go` → CreateTaskAggregate via the
// spawn backlog shim, now an Authority query); new reach-through fails.
func TestNoNewTaskAuthorityReachThrough(t *testing.T) {
	testutil.AssertNoNewTaskAuthorityCallers(t, ".", map[string][]string{})
}
