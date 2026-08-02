package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

var ErrTaskLifecyclePrecondition = errors.New("task lifecycle precondition failed")

type ReadinessReason string

const (
	ReadinessMissingOwner ReadinessReason = "missing-owner"
	ReadinessQueued       ReadinessReason = "queued"
	ReadinessBlocked      ReadinessReason = "blocked"
	ReadinessInFlight     ReadinessReason = "in-flight"
	ReadinessTerminal     ReadinessReason = "terminal"
	ReadinessDispatchHold ReadinessReason = "dispatch-hold"
)

type TaskReadiness struct {
	TaskID          string
	Generation      string
	Ready           bool
	BlockingReasons []ReadinessReason
}

func QueryTaskReadiness(homeDir, taskID string) (TaskReadiness, error) {
	if err := validateTaskID(taskID); err != nil {
		return TaskReadiness{}, err
	}
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil {
		return TaskReadiness{}, err
	}
	if !ok {
		return TaskReadiness{TaskID: taskID, BlockingReasons: []ReadinessReason{"not-found"}}, nil
	}
	result := TaskReadiness{TaskID: taskID, Generation: agg.Generation}
	if err := checkDispatchHoldUnlocked(homeDir, DispatchActionStart, taskID, agg.Project, agg.Generation, ""); err != nil {
		if errors.Is(err, ErrDispatchHeld) {
			result.BlockingReasons = append(result.BlockingReasons, ReadinessDispatchHold)
		}
	}
	if agg.Owner == "" {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessMissingOwner)
	}
	switch agg.State {
	case "queued", "":
		if len(result.BlockingReasons) == 0 {
			result.Ready = true
		}
	case "blocked":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessBlocked)
	case "working", "in-flight":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessInFlight)
	case "done", "resolved", "retired":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessTerminal)
	default:
		result.BlockingReasons = append(result.BlockingReasons, ReadinessTerminal)
	}
	return result, nil
}

func ListReadyTaskAggregates(homeDir string) ([]TaskAggregate, error) {
	aggs, err := ListCurrentTaskAggregates(homeDir)
	if err != nil {
		return nil, err
	}
	ready := make([]TaskAggregate, 0, len(aggs))
	for _, agg := range aggs {
		readiness, err := QueryTaskReadiness(homeDir, agg.TaskID)
		if err != nil {
			return nil, err
		}
		if readiness.Ready {
			ready = append(ready, agg)
		}
	}
	return ready, nil
}

// supersedeGenerations is the shared safe generation transition used by
// SupersedeTask (failed or terminal). It bumps the current generation to the
// next queued generation WITHOUT carrying stale endpoint (pane) or worktree
// bindings, and preserves the prior generation as historical. The caller
// decides which source states are eligible.
func supersedeGenerations(homeDir, taskID, verb string, eligible func(string) bool) (*TaskAggregate, error) {
	lock, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("%s task: %w", verb, err)
	}
	_ = lock
	defer unlock()

	current, ok, err := readCurrentTaskAggregateUnlocked(homeDir, taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, lifecyclePrecondition(verb + " requires existing task")
	}
	if !eligible(current.State) {
		return nil, lifecyclePrecondition(fmt.Sprintf("%s requires eligible generation, not %q", verb, current.State))
	}
	generation, err := strconv.ParseUint(current.Generation, 10, 64)
	if err != nil || generation == ^uint64(0) {
		return nil, fmt.Errorf("%s task: invalid current generation %q", verb, current.Generation)
	}
	updated := *current
	updated.Generation = strconv.FormatUint(generation+1, 10)
	updated.Current = true
	updated.State = "queued"
	updated.StateDetail = ""
	// Prevent stale pane/worktree ownership: the new generation starts with no
	// endpoint or worktree binding. The prior generation's bindings stay on
	// the historical record only.
	updated.Endpoint = nil
	updated.Worktree = nil
	// Reset status ownership: the old generation's status log must not be
	// attributed to the new generation.
	if p, statusErr := statusPath(homeDir, taskID); statusErr == nil {
		_ = os.Remove(p)
	}
	oldPath := filepath.Join(homeDir, taskAggregateRelPath(taskID, current.Generation))
	currentPath := filepath.Join(homeDir, taskAggregateDir, taskID, taskCurrentFile)
	oldBytes, err := os.ReadFile(oldPath)
	if err != nil {
		return nil, err
	}
	pointerBytes, err := os.ReadFile(currentPath)
	if err != nil {
		return nil, err
	}
	newPath := filepath.Join(homeDir, taskAggregateRelPath(taskID, updated.Generation))
	rollback := func() {
		_ = atomicWrite(oldPath, oldBytes)
		_ = os.Remove(newPath)
		_ = atomicWrite(currentPath, pointerBytes)
	}
	if err := writeTaskAggregateUnlocked(homeDir, updated, false); err != nil {
		rollback()
		return nil, err
	}
	if err := writeTaskAggregateUnlocked(homeDir, *current, false); err != nil {
		rollback()
		return nil, err
	}
	if err := writeTextFile(currentPath, updated.Generation+"\n"); err != nil {
		rollback()
		return nil, err
	}
	return &updated, nil
}

// SupersedeTask is the explicit safe retry/supersede transition for failed or
// terminal worktree generations. It refuses live generations (queued, working,
// blocked) so a retry never claims work that is still executing, creates the
// next queued generation without stale pane/worktree bindings, resets status
// ownership, and preserves the prior generation as historical.
func SupersedeTask(homeDir, taskID string) (*TaskAggregate, error) {
	return supersedeGenerations(homeDir, taskID, "supersede", func(state string) bool {
		switch state {
		case "failed", "done", "resolved", "retired":
			return true
		default:
			return false
		}
	})
}

func mutateCurrentTaskAggregate(homeDir, taskID string, mutate func(*TaskAggregate) (*TaskAggregate, error)) (*TaskAggregate, error) {
	var result *TaskAggregate
	var mutateErr error
	if err := withDispatchControlLock(homeDir, func() error {
		var err error
		result, mutateErr = mutateCurrentTaskAggregateLocked(homeDir, taskID, mutate)
		return err
	}); err != nil {
		return nil, err
	}
	return result, mutateErr
}

func mutateCurrentTaskAggregateLocked(homeDir, taskID string, mutate func(*TaskAggregate) (*TaskAggregate, error)) (*TaskAggregate, error) {
	lock, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("mutate task: %w", err)
	}
	_ = lock
	defer unlock()

	current, ok, err := readCurrentTaskAggregateUnlocked(homeDir, taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, lifecyclePrecondition("task does not exist")
	}
	updated, err := mutate(current)
	if err != nil {
		return nil, err
	}
	if err := writeTaskAggregateUnlocked(homeDir, *updated, true); err != nil {
		return nil, err
	}
	return updated, nil
}

func lifecyclePrecondition(message string) error {
	return fmt.Errorf("%w: %s", ErrTaskLifecyclePrecondition, message)
}

func readCurrentTaskAggregateUnlocked(homeDir, taskID string) (*TaskAggregate, bool, error) {
	return ReadCurrentTaskAggregate(homeDir, taskID)
}

func writeTaskAggregateUnlocked(homeDir string, agg TaskAggregate, current bool) error {
	agg.Current = current
	if err := writeTaskAggregateFilesUnlocked(homeDir, agg); err != nil {
		return err
	}
	if current {
		return writeTextFile(filepath.Join(homeDir, taskAggregateDir, agg.TaskID, taskCurrentFile), agg.Generation+"\n")
	}
	return nil
}
