package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// BoundEndpoint is the canonical endpoint binding of one live soldier task and
// the harness its launch runs.
type BoundEndpoint struct {
	TaskID   string
	Endpoint taskauthority.EndpointBinding
	Harness  string
}

// LiveBoundEndpoints lists the home's live, bound soldier tasks
// (taskauthority.LiveBound) from the canonical store, sorted by task ID. A
// home with no canonical store has none.
func LiveBoundEndpoints(homeDir string) ([]BoundEndpoint, error) {
	auth, err := canonicalStore(homeDir)
	if errors.Is(err, home.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	aggs, err := auth.List()
	if err != nil {
		return nil, err
	}
	var out []BoundEndpoint
	for _, agg := range aggs {
		if !taskauthority.LiveBound(agg) {
			continue
		}
		out = append(out, boundEndpoint(agg))
	}
	return out, nil
}

// taskEndpoint is the endpoint the task's current generation is bound to.
func taskEndpoint(homeDir, taskID string) (BoundEndpoint, error) {
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return BoundEndpoint{}, err
	}
	auth, err := canonicalStore(homeDir)
	if err != nil {
		return BoundEndpoint{}, err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return BoundEndpoint{}, err
	}
	if agg.Endpoint == nil {
		return BoundEndpoint{}, fmt.Errorf("task %s has no bound endpoint", taskID)
	}
	return boundEndpoint(agg), nil
}

// paneEndpoint is the pane a task's messages reach: a captain's as its .meta
// records it, a soldier's as its canonical binding names it.
func paneEndpoint(homeDir, taskID string) (BoundEndpoint, error) {
	if !strings.HasPrefix(taskID, "captain:") {
		return taskEndpoint(homeDir, taskID)
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return BoundEndpoint{}, err
	}
	ref := backend.MetaEndpointRef(meta)
	return BoundEndpoint{TaskID: taskID, Harness: meta["harness"], Endpoint: taskauthority.EndpointBinding{
		Backend: ref.Backend, Handle: ref.Handle, SessionOwner: ref.SessionOwner, WorkspaceID: ref.WorkspaceID, TabID: ref.TabID,
	}}, nil
}

func canonicalStore(homeDir string) (*taskauthority.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return taskauthority.NewCanonical(h)
}

func boundEndpoint(agg taskauthority.Aggregate) BoundEndpoint {
	ep := BoundEndpoint{TaskID: agg.TaskID, Endpoint: *agg.Endpoint}
	if agg.Launch != nil {
		ep.Harness = agg.Launch.Harness
	}
	return ep
}
