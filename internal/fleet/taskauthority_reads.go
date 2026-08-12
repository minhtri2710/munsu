package fleet

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file owns the canonical-read path for snapshot and observation
// (Task 7.8, ADR-0007 §7): authoritative Task Authority records are the only
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
// homes fail closed. `currentCanonical` is used by canonical observation and
// the current-state query; it is never a projection fallback gate.
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

// currentCanonicalAggregate reads one task's current authoritative aggregate,
// reporting whether a canonical record exists. A missing record is not itself
// an error, but every canonical-only caller must treat a missing record as a
// clean-break operation error; non-canonical homes and corrupt state fail
// closed.
func currentCanonicalAggregate(homeDir, id string) (tauth.Aggregate, bool, error) {
	agg, err := currentCanonical(homeDir, id)
	if err != nil {
		if errors.Is(err, tauth.ErrNotFound) {
			return tauth.Aggregate{}, false, nil
		}
		return tauth.Aggregate{}, false, err
	}
	return agg, true, nil
}

// canonicalCurrentStateQ is the canonical current-state query. It is the
// explicit dependency passed to snapshot/guard callers; it is stateless and
// safe to construct at each composition call site.
type canonicalCurrentStateQ struct{}

// NewCanonicalCurrentState returns the canonical current-state query used as
// the explicit CurrentState dependency for snapshot and guard readers.
func NewCanonicalCurrentState() CurrentStateQuery {
	return canonicalCurrentStateQ{}
}

// Read returns the authoritative current state for one task. The lifecycle
// phase/description come from the canonical Task Authority record; open
// activities are evidence derived from the status event log. A missing or
// malformed canonical record fails closed (operation error) — there is no
// legacy projection fallback.
func (canonicalCurrentStateQ) Read(homeDir, taskID string) (*CurrentStateInfo, error) {
	agg, canonical, err := currentCanonicalAggregate(homeDir, taskID)
	if err != nil {
		return nil, err
	}
	if !canonical {
		return nil, fmt.Errorf("task %q in home %q has no canonical Task Authority record", taskID, homeDir)
	}
	info := &CurrentStateInfo{
		State:               string(agg.Phase),
		Description:         agg.PhaseDetail,
		StatusLogSuperseded: true,
	}
	if info.Description == "" {
		info.Description = agg.Definition.Description
	}
	info.OpenActivities = home.OpenActivities(filepath.Join(home.StateDir(homeDir), taskID+".status"))
	return info, nil
}
