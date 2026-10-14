package orchestrator

import (
	"errors"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// BoundEndpoint is the canonical endpoint binding of one live soldier task, in
// the key shape the session backends resolve a bound endpoint from.
type BoundEndpoint struct {
	TaskID string
	Meta   map[string]string
}

// LiveBoundEndpoints lists the home's live, bound soldier tasks
// (taskauthority.LiveBound) from the canonical store, sorted by task ID. A
// home with no canonical store has none.
func LiveBoundEndpoints(homeDir string) ([]BoundEndpoint, error) {
	h, err := home.Open(homeDir)
	if errors.Is(err, home.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	auth, err := taskauthority.NewCanonical(h)
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
		ep := agg.Endpoint
		meta := map[string]string{"backend": ep.Backend, "window": ep.Handle}
		for k, v := range map[string]string{backend.MetaSessionOwner: ep.SessionOwner, backend.MetaWorkspaceID: ep.WorkspaceID, backend.MetaTabID: ep.TabID} {
			if v != "" {
				meta[k] = v
			}
		}
		if agg.Launch != nil && agg.Launch.Harness != "" {
			meta["harness"] = agg.Launch.Harness
		}
		out = append(out, BoundEndpoint{TaskID: agg.TaskID, Meta: meta})
	}
	return out, nil
}
