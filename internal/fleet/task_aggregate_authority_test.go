package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestNoNewTaskAuthorityReachThrough pins the temporary allowlist of fleet
// production files that may still call legacy home task-authority mutations
// during the Task Authority migration (ADR-0007). The allowlist must only
// shrink as migration slices land; new reach-through fails.
func TestNoNewTaskAuthorityReachThrough(t *testing.T) {
	testutil.AssertNoNewTaskAuthorityCallers(t, ".", map[string][]string{
		"spawn_runner.go": {
			"CreateTaskAggregate",
			"UpdateCurrentTaskAggregateState",
			"BindTaskWorktree",
			"BindTaskEndpoint",
			"CheckDispatchHold",
		},
		"task_handoff_transaction.go": {"CheckDispatchHold"},
	})
}
