package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TaskWorktreeBinding struct {
	TaskGeneration     string `json:"task_generation"`
	RepositoryIdentity string `json:"repository_identity"`
	Path               string `json:"path"`
	GitDir             string `json:"git_dir"`
	CommonDir          string `json:"common_dir"`
	Head               string `json:"head"`
	LeaseID            string `json:"lease_id"`
	FenceToken         string `json:"fence_token"`
	BoundAtUnix        int64  `json:"bound_at_unix"`
}

func validateTaskWorktreeBinding(binding TaskWorktreeBinding) error {
	if err := validateTaskGeneration(binding.TaskGeneration); err != nil {
		return err
	}
	if strings.TrimSpace(binding.RepositoryIdentity) == "" {
		return fmt.Errorf("worktree binding missing repository identity")
	}
	if strings.TrimSpace(binding.Path) == "" {
		return fmt.Errorf("worktree binding missing path")
	}
	if strings.TrimSpace(binding.GitDir) == "" {
		return fmt.Errorf("worktree binding missing git dir")
	}
	if strings.TrimSpace(binding.CommonDir) == "" {
		return fmt.Errorf("worktree binding missing common dir")
	}
	if strings.TrimSpace(binding.Head) == "" {
		return fmt.Errorf("worktree binding missing head")
	}
	if strings.TrimSpace(binding.LeaseID) == "" {
		return fmt.Errorf("worktree binding missing lease id")
	}
	if strings.TrimSpace(binding.FenceToken) == "" {
		return fmt.Errorf("worktree binding missing fence token")
	}
	if binding.BoundAtUnix <= 0 {
		return fmt.Errorf("worktree binding missing bound timestamp")
	}
	return nil
}

type taskWorktreeLeaseMarker struct {
	TaskID         string `json:"task_id"`
	TaskGeneration string `json:"task_generation"`
	LeaseID        string `json:"lease_id"`
	FenceToken     string `json:"fence_token"`
}

func taskWorktreeLeasePath(homeDir, taskID, generation, leaseID string) string {
	return filepath.Join(homeDir, taskAuthorityDir, "worktree-leases", taskID, generation, leaseID+".json")
}

func BindTaskWorktree(homeDir, taskID, generation string, binding TaskWorktreeBinding) error {
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("task aggregate %s has no current generation", taskID)
	}
	if agg.Generation != generation {
		return fmt.Errorf("task aggregate %s current generation is %s, not %s", taskID, agg.Generation, generation)
	}
	if agg.Worktree != nil {
		return fmt.Errorf("task aggregate %s/%s already has worktree binding", taskID, generation)
	}
	binding.TaskGeneration = generation
	if err := validateTaskWorktreeBinding(binding); err != nil {
		return err
	}
	marker := taskWorktreeLeaseMarker{TaskID: taskID, TaskGeneration: generation, LeaseID: binding.LeaseID, FenceToken: binding.FenceToken}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	path := taskWorktreeLeasePath(homeDir, taskID, generation, binding.LeaseID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return err
	}
	updated := *agg
	updated.Worktree = &binding
	if err := WriteTaskAggregate(homeDir, updated); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func TaskWorktreeLeaseActive(homeDir, taskID string, binding TaskWorktreeBinding) bool {
	path := taskWorktreeLeasePath(homeDir, taskID, binding.TaskGeneration, binding.LeaseID)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var marker taskWorktreeLeaseMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.TaskID == taskID && marker.TaskGeneration == binding.TaskGeneration && marker.LeaseID == binding.LeaseID && marker.FenceToken == binding.FenceToken
}
