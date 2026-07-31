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

func StartTask(homeDir, taskID string) (*TaskAggregate, error) {
	return mutateCurrentTaskAggregate(homeDir, taskID, func(agg *TaskAggregate) (*TaskAggregate, error) {
		if err := checkDispatchHoldUnlocked(homeDir, DispatchActionStart, taskID, agg.Project, agg.Generation, ""); err != nil {
			return nil, err
		}
		if agg.State != "queued" && agg.State != "" {
			return nil, lifecyclePrecondition("start requires queued task")
		}
		if agg.Owner == "" {
			return nil, lifecyclePrecondition("start requires authoritative owner")
		}
		updated := *agg
		updated.State = "working"
		updated.StateDetail = ""
		return &updated, nil
	})
}

func UnblockTask(homeDir, taskID string) (*TaskAggregate, error) {
	return mutateCurrentTaskAggregate(homeDir, taskID, func(agg *TaskAggregate) (*TaskAggregate, error) {
		if agg.State != "blocked" {
			return nil, lifecyclePrecondition("unblock requires blocked task")
		}
		updated := *agg
		updated.State = "queued"
		updated.StateDetail = ""
		return &updated, nil
	})
}

func ReopenTask(homeDir, taskID string) (*TaskAggregate, error) {
	lock, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("reopen task: %w", err)
	}
	_ = lock
	defer unlock()

	current, ok, err := readCurrentTaskAggregateUnlocked(homeDir, taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, lifecyclePrecondition("reopen requires existing task")
	}
	if current.State != "done" && current.State != "resolved" && current.State != "retired" {
		return nil, lifecyclePrecondition("reopen requires terminal task")
	}
	generation, err := strconv.ParseUint(current.Generation, 10, 64)
	if err != nil || generation == ^uint64(0) {
		return nil, fmt.Errorf("reopen task: invalid current generation %q", current.Generation)
	}
	updated := *current
	updated.Generation = strconv.FormatUint(generation+1, 10)
	updated.Current = true
	updated.State = "queued"
	updated.StateDetail = ""
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
