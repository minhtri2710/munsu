package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file owns the canonical-read path for snapshot and observation:
// authoritative Task Authority records are the only
// source for kind, project, and lifecycle phase. A home that is not a
// canonical v1 home fails closed (migration is explicit and never automatic);
// a canonical home with no authority state at all serves the empty view
// without creating anything.

// canonicalAuthority opens the canonical home and constructs the canonical
// Task Authority over it. A directory that is not an initialized canonical
// v1 home (no verified identity/layout) fails closed; a canonical home with
// no task-authority state serves an empty view.
func canonicalAuthority(homeDir string) (*tauth.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return tauth.NewCanonical(h)
}

// canonicalAggregates loads the current authoritative aggregates for one
// home, keyed by task ID. A canonical home with no authority state returns an
// empty map; a non-canonical home or corrupt/recovery-required state fails
// closed.
func canonicalAggregates(homeDir string) (map[string]tauth.Aggregate, error) {
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		return nil, err
	}
	aggs, err := c.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]tauth.Aggregate, len(aggs))
	for _, agg := range aggs {
		out[agg.TaskID] = agg
	}
	return out, nil
}

// currentCanonical reads one task's current authoritative aggregate. A
// missing record is ErrNotFound (the caller reports a canonical-missing
// clean-break error); a non-canonical home and corrupt/recovery-required
// homes fail closed. It is never a projection fallback gate.
func currentCanonical(homeDir, id string) (tauth.Aggregate, error) {
	taskID, err := domain.NewTaskID(id)
	if err != nil {
		// A non-task identity (for example a captain:<id> metadata key) can
		// never hold a canonical task record; report it as missing so callers
		// surface a clean-break canonical-missing result rather than treating
		// it as Task observation authority.
		return tauth.Aggregate{}, tauth.ErrNotFound
	}
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		return tauth.Aggregate{}, err
	}
	return c.Get(taskID)
}

// heldEndpoint is the endpoint a task's current generation holds.
type heldEndpoint struct {
	TaskID   string
	Kind     string
	Endpoint tauth.EndpointBinding
}

// heldEndpoints lists the endpoint each task's current generation in homeDir
// holds, bound or acquired before its bind; a retired generation holds none.
// open is home.Open, or home.OpenCommitted for a fenced seat's reads. A home
// with no canonical store holds none; an unreadable task document fails
// closed.
func heldEndpoints(homeDir string, open func(string) (*home.Home, error)) ([]heldEndpoint, error) {
	h, err := open(homeDir)
	if errors.Is(err, home.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := tauth.NewCanonical(h)
	if err != nil {
		return nil, err
	}
	aggs, err := c.List()
	if err != nil {
		return nil, err
	}
	var held []heldEndpoint
	for _, agg := range aggs {
		if ep := endpointOrAcquired(agg.Endpoint, agg.AcquiredEndpoint); ep != nil {
			held = append(held, heldEndpoint{TaskID: agg.TaskID, Kind: agg.Definition.Kind, Endpoint: *ep})
		}
	}
	return held, nil
}

// captainWorkspaces is the herdr workspace each captain .meta in homeDir
// records, keyed by its captain:<id> task ID. A .meta that vanishes after
// enumeration records nothing; any other read error fails closed.
func captainWorkspaces(homeDir string) (map[string]string, error) {
	ids, err := home.ListMetaIDs(homeDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, id := range ids {
		if !strings.HasPrefix(id, "captain:") {
			continue
		}
		path, err := home.MetaFilePath(homeDir, id)
		if err != nil {
			return nil, err
		}
		meta, err := home.ReadMetaFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ws := meta[backend.MetaWorkspaceID]; ws != "" {
			out[id] = ws
		}
	}
	return out, nil
}

// LiveWorkspaceIDs is the set of herdr workspaces homeDir's tasks and
// captains hold, which herdr prune never closes.
// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council: re-home onto canonical bindings, fail closed)
func LiveWorkspaceIDs(homeDir string) (map[string]bool, error) {
	held, err := heldEndpoints(homeDir, home.Open)
	if err != nil {
		return nil, fmt.Errorf("reading task endpoint bindings: %w", err)
	}
	captains, err := captainWorkspaces(homeDir)
	if err != nil {
		return nil, fmt.Errorf("reading captain meta: %w", err)
	}
	ids := make(map[string]bool)
	for _, h := range held {
		if h.Endpoint.WorkspaceID != "" {
			ids[h.Endpoint.WorkspaceID] = true
		}
	}
	for _, ws := range captains {
		ids[ws] = true
	}
	return ids, nil
}

// SoldierSeat returns the check that homeDir hosts a live soldier seat for a
// task: its current generation holds an endpoint and is not retired. It reads
// committed state only, so a fenced soldier seat can run it.
func SoldierSeat(homeDir string) home.SoldierSeatCheck {
	return func(id string) error {
		taskID, err := domain.NewTaskID(id)
		if err != nil {
			return fmt.Errorf("home %s hosts no soldier task %q: %w", homeDir, id, err)
		}
		h, err := home.OpenCommitted(homeDir)
		if err != nil {
			return fmt.Errorf("home %s hosts no soldier task %q: %w", homeDir, id, err)
		}
		c, err := tauth.NewCanonical(h)
		if err != nil {
			return fmt.Errorf("home %s hosts no soldier task %q: %w", homeDir, id, err)
		}
		agg, err := c.Get(taskID)
		if err != nil {
			return fmt.Errorf("home %s hosts no soldier task %q: %w", homeDir, id, err)
		}
		if endpointOrAcquired(agg.Endpoint, agg.AcquiredEndpoint) == nil {
			return fmt.Errorf("home %s hosts no live soldier seat for task %q", homeDir, id)
		}
		return nil
	}
}
