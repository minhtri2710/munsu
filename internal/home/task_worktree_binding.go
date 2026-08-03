package home

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// taskAuthorityDir is the home-relative v1 task-authority namespace root.
// The v1 aggregate store under state/.task-authority/aggregates was deleted
// in Task 8.2; this constant now only locates the v1 worktree-lease markers.
const taskAuthorityDir = "state/.task-authority"

// TaskWorktreeBinding is the v1 worktree binding shape decoded by the
// task-authority migration (internal/taskauthorityfs convertV1Aggregate).
// It is decode-only: the legacy aggregate store that produced it was deleted
// in Task 8.2, and current bindings live on taskauthority.Aggregate.Worktree.
// The lease read below converts a current binding into this shape.
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

type taskWorktreeLeaseMarker struct {
	TaskID         string `json:"task_id"`
	TaskGeneration string `json:"task_generation"`
	LeaseID        string `json:"lease_id"`
	FenceToken     string `json:"fence_token"`
}

func taskWorktreeLeasePath(homeDir, taskID, generation, leaseID string) string {
	return filepath.Join(homeDir, taskAuthorityDir, "v1", "worktree-leases", taskID, generation, leaseID+".json")
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
