package cli

import "fmt"

// LifecyclePartialError reports authoritative success followed by projection
// failure, so callers do not retry the lifecycle mutation blindly.
type LifecyclePartialError struct {
	TaskID string
	State  string
	Cause  error
}

func (e *LifecyclePartialError) Error() string {
	return fmt.Sprintf("task %s is authoritatively %s; backlog projection failed: %v", e.TaskID, e.State, e.Cause)
}

func (e *LifecyclePartialError) Unwrap() error { return e.Cause }
