package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestNoNewTaskAuthorityReachThrough pins the empty allowlist of CLI
// production files that may call legacy home task-authority mutations during
// the Task Authority migration (ADR-0007). Task 7.8 removed the final
// allowlisted reach-through (`spawn_cmd.go` → UpdateCurrentTaskAggregateKind
// via `promote`, now a named Authority operation); new reach-through fails.
func TestNoNewTaskAuthorityReachThrough(t *testing.T) {
	testutil.AssertNoNewTaskAuthorityCallers(t, ".", map[string][]string{})
}
