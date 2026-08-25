//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownPrevalidateDeliveryTaskRefusesTaskShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, homeDir, taskID string)
		want  string
	}{
		{
			name: "non-current generation",
			setup: func(t *testing.T, homeDir, taskID string) {
				if err := rewriteDeliveryAggregate(t, homeDir, taskID, func(cur taskauthority.Aggregate) taskauthority.Aggregate {
					cur.Current = false
					return cur
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "is not the current generation",
		},
		{
			name: "non-working phase",
			setup: func(t *testing.T, homeDir, taskID string) {
				if err := rewriteDeliveryAggregate(t, homeDir, taskID, func(cur taskauthority.Aggregate) taskauthority.Aggregate {
					cur.Phase = taskauthority.PhaseBlocked
					return cur
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "requires a working task",
		},
		{
			name: "missing worktree and endpoint",
			setup: func(t *testing.T, homeDir, taskID string) {
				if err := rewriteDeliveryAggregate(t, homeDir, taskID, func(cur taskauthority.Aggregate) taskauthority.Aggregate {
					cur.Worktree = nil
					cur.Endpoint = nil
					return cur
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "requires the bound worktree and endpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			tc.setup(t, homeDir, taskID)
			agg, err := readDeliveryAggregate(t, homeDir, taskID)
			if err != nil {
				t.Fatal(err)
			}
			err = prevalidateDeliveryTask(c, agg, deliverRequest())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("prevalidateDeliveryTask error = %v, want %q", err, tc.want)
			}
		})
	}
}

func rewriteDeliveryAggregate(t *testing.T, homeDir, taskID string, mutate func(taskauthority.Aggregate) taskauthority.Aggregate) error {
	t.Helper()
	path := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc struct {
		HomeRevision uint64                  `json:"home_revision"`
		Aggregate    taskauthority.Aggregate `json:"aggregate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc.Aggregate = mutate(doc.Aggregate)
	doc.HomeRevision++
	next, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, next, 0600)
}

func readDeliveryAggregate(t *testing.T, homeDir, taskID string) (taskauthority.Aggregate, error) {
	t.Helper()
	path := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return taskauthority.Aggregate{}, err
	}
	var doc struct {
		Aggregate taskauthority.Aggregate `json:"aggregate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return taskauthority.Aggregate{}, err
	}
	return doc.Aggregate, nil
}
