package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

// LifecyclePartialError reports authoritative success followed by projection
// failure, so callers do not retry the lifecycle mutation blindly.
type LifecyclePartialError struct {
	TaskID string
	State  string
	Cause  error
}

func (e *LifecyclePartialError) Error() string {
	return fmt.Sprintf("task %s is authoritatively %s; projection failed: %v", e.TaskID, e.State, e.Cause)
}

func (e *LifecyclePartialError) Unwrap() error { return e.Cause }

// ProjectionPartialError reports projection reconciliation that could not
// repair one or more .meta/.status projections. Reconciliation never mutates
// the authoritative Task Authority records, so the pass is retryable without
// changing task revision or generation.
type ProjectionPartialError struct {
	Failed []taskauthorityfs.TaskProjection
}

func (e *ProjectionPartialError) Error() string {
	if len(e.Failed) == 0 {
		return "projection reconciliation failed"
	}
	var parts []string
	for _, f := range e.Failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.TaskID, f.Err))
	}
	return "projection reconciliation failed: " + strings.Join(parts, "; ")
}
