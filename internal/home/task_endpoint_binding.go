package home

import (
	"fmt"
	"strings"
)

type TaskEndpointBinding struct {
	TaskGeneration string `json:"task_generation"`
	Backend        string `json:"backend"`
	Handle         string `json:"handle"`
	LeaseID        string `json:"lease_id"`
	FenceToken     string `json:"fence_token"`
	SessionOwner   string `json:"session_owner,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TabID          string `json:"tab_id,omitempty"`
	BoundAtUnix    int64  `json:"bound_at_unix"`
}

func validateTaskEndpointBinding(binding TaskEndpointBinding) error {
	if err := validateTaskGeneration(binding.TaskGeneration); err != nil {
		return err
	}
	if strings.TrimSpace(binding.Backend) == "" {
		return fmt.Errorf("endpoint binding missing backend")
	}
	if strings.TrimSpace(binding.Handle) == "" {
		return fmt.Errorf("endpoint binding missing handle")
	}
	if strings.TrimSpace(binding.LeaseID) == "" {
		return fmt.Errorf("endpoint binding missing lease id")
	}
	if strings.TrimSpace(binding.FenceToken) == "" {
		return fmt.Errorf("endpoint binding missing fence token")
	}
	if binding.BoundAtUnix <= 0 {
		return fmt.Errorf("endpoint binding missing bound timestamp")
	}
	return nil
}

func BindTaskEndpoint(homeDir, taskID, generation string, binding TaskEndpointBinding) error {
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
	if agg.Endpoint != nil {
		return fmt.Errorf("task aggregate %s/%s already has endpoint binding", taskID, generation)
	}
	binding.TaskGeneration = generation
	if err := validateTaskEndpointBinding(binding); err != nil {
		return err
	}
	updated := *agg
	updated.Endpoint = &binding
	return WriteTaskAggregate(homeDir, updated)
}
