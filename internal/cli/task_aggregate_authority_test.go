package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestNoNewTaskAuthorityReachThrough pins the temporary allowlist of CLI
// production files that may still call legacy home task-authority mutations
// during the Task Authority migration (ADR-0007). The allowlist must only
// shrink as migration slices land; new reach-through fails.
func TestNoNewTaskAuthorityReachThrough(t *testing.T) {
	testutil.AssertNoNewTaskAuthorityCallers(t, ".", map[string][]string{
		"backlog_cmd.go": {
			"CreateTaskAggregate",
			"UpdateCurrentTaskAggregateState",
			"StartTask",
			"UnblockTask",
			"ReopenTask",
		},
		"spawn_cmd.go": {"UpdateCurrentTaskAggregateKind"},
		"task_cmd.go":  {"CreateTaskAggregate", "UpdateCurrentTaskAggregateState"},
	})
}
