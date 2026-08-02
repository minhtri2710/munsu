package taskauthorityfs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ProjectionState is the typed outcome of reconciling one .meta or .status
// projection for a task.
type ProjectionState string

const (
	// ProjectionUnchanged means the projection already matched the canonical
	// records and nothing was written.
	ProjectionUnchanged ProjectionState = "unchanged"
	// ProjectionRepaired means the projection was missing, corrupt, or stale
	// and was rewritten from canonical records.
	ProjectionRepaired ProjectionState = "repaired"
	// ProjectionFailed means the projection could not be repaired. The
	// authoritative records were never touched, so the pass is retryable.
	ProjectionFailed ProjectionState = "failed"
)

// TaskProjection is the typed outcome of reconciling one task's .meta and
// .status projections. Generation and Revision pin the canonical record the
// projections were derived from; reconciliation never changes them.
type TaskProjection struct {
	TaskID     string
	Generation taskauthority.Generation
	Revision   taskauthority.Revision
	Meta       ProjectionState
	Status     ProjectionState
	Err        string
}

// ReconcileTaskProjections reconciles one task's .meta and .status
// projections from one canonical view. .meta authoritative fields are
// derived from the current Task Aggregate (runtime-only projection fields
// are preserved); .status lines are derived from the typed lifecycle audit
// history and appended when missing. The projections are one-directional:
// canonical records are never written, so Revision and Generation cannot
// change through reconciliation. Projections that cannot be repaired are
// reported as a typed failed state with the authority left untouched.
func (s *Store) ReconcileTaskProjections(taskID string) (TaskProjection, error) {
	if err := validateTaskID(taskID); err != nil {
		return TaskProjection{}, err
	}
	v, err := s.View()
	if err != nil {
		return TaskProjection{}, err
	}
	agg, ok := v.Current(taskID)
	if !ok {
		return TaskProjection{}, fmt.Errorf("%w: task %s not found", taskauthority.ErrNotFound, taskID)
	}
	return s.reconcile(agg, auditForTask(v, taskID)), nil
}

// ReconcileProjections reconciles every current task's projections in one
// pass, returning the typed outcomes sorted by task ID. It is idempotent:
// a second pass reports every projection unchanged and rewrites nothing.
func (s *Store) ReconcileProjections() ([]TaskProjection, error) {
	v, err := s.View()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, agg := range v.Aggregates {
		if agg.Current && !seen[agg.TaskID] {
			seen[agg.TaskID] = true
			ids = append(ids, agg.TaskID)
		}
	}
	sort.Strings(ids)
	out := make([]TaskProjection, 0, len(ids))
	for _, id := range ids {
		agg, _ := v.Current(id)
		out = append(out, s.reconcile(agg, auditForTask(v, id)))
	}
	return out, nil
}

// reconcile runs one task's meta and status reconciliation, collecting any
// typed failure detail without ever writing into the authority.
func (s *Store) reconcile(agg taskauthority.Aggregate, audit []taskauthority.AuditEvent) TaskProjection {
	out := TaskProjection{TaskID: agg.TaskID, Generation: agg.Generation, Revision: agg.Revision}
	metaState, metaErr := s.reconcileMeta(agg)
	statusState, statusErr := s.reconcileStatus(agg.TaskID, audit)
	out.Meta = metaState
	out.Status = statusState
	var errs []string
	if metaErr != nil {
		errs = append(errs, fmt.Sprintf(".meta: %v", metaErr))
	}
	if statusErr != nil {
		errs = append(errs, fmt.Sprintf(".status: %v", statusErr))
	}
	out.Err = strings.Join(errs, "; ")
	return out
}

// auditForTask returns the typed audit events for one task in committed
// order (the canonical View loads audit sorted by timestamp, then operation
// ID).
func auditForTask(v taskauthority.View, taskID string) []taskauthority.AuditEvent {
	var out []taskauthority.AuditEvent
	for _, ev := range v.Audit {
		if ev.TaskID == taskID {
			out = append(out, ev)
		}
	}
	return out
}

// authoritativeMetaFields are the .meta projection keys derived from the
// canonical Task Aggregate. They mirror the CLI's authoritative field set
// (Task 3.2): canonical records win and the projection cannot override them.
var authoritativeMetaFields = []string{"owner", "description", "kind", "project", "generation", "state"}

// deriveMetaProjection overlays the canonical aggregate fields on the
// existing projection, preserving runtime-only projection fields. Empty
// canonical values remove stale keys. The projection is one-directional:
// this function never writes into the authority.
func deriveMetaProjection(agg taskauthority.Aggregate, existing map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(authoritativeMetaFields))
	for k, v := range existing {
		out[k] = v
	}
	put := func(k, v string) {
		if v == "" {
			delete(out, k)
			return
		}
		out[k] = v
	}
	put("owner", agg.Definition.Owner)
	put("description", agg.Definition.Description)
	put("kind", agg.Definition.Kind)
	put("project", agg.Definition.Project)
	put("generation", agg.Generation.String())
	put("state", string(agg.Phase))
	return out
}

// reconcileMeta rewrites the .meta projection from the canonical aggregate,
// preserving runtime-only fields. A missing or corrupt projection is rebuilt
// from canonical fields alone.
func (s *Store) reconcileMeta(agg taskauthority.Aggregate) (ProjectionState, error) {
	existing, existingErr := home.ReadMeta(s.homeDir, agg.TaskID)
	corrupt := projectionFileCorrupt(s.homeDir, agg.TaskID, ".meta")
	if existingErr != nil {
		if errors.Is(existingErr, os.ErrNotExist) {
			existing = map[string]string{}
		} else {
			// A real read failure (corruption, non-regular file): rebuild
			// from canonical fields alone.
			corrupt = true
			existing = map[string]string{}
		}
	}
	derived := deriveMetaProjection(agg, existing)
	if !mapsEqual(derived, existing) || corrupt {
		if err := home.WriteMeta(s.homeDir, agg.TaskID, derived); err != nil {
			return ProjectionFailed, err
		}
		return ProjectionRepaired, nil
	}
	return ProjectionUnchanged, nil
}

// deriveStatusLines renders the typed lifecycle audit history of one task as
// append-only .status lines ("<after>: <reason>"). Dispatch audit events are
// not task-bound and never become status lines. Lines are deterministic in
// committed audit order, so reconciliation is idempotent.
func deriveStatusLines(audit []taskauthority.AuditEvent) []string {
	var out []string
	for _, ev := range audit {
		if ev.Kind != taskauthority.AuditLifecycle {
			continue
		}
		reason := ev.Reason
		if reason == "" {
			reason = string(ev.After)
		}
		out = append(out, string(ev.After)+": "+reason)
	}
	return out
}

// reconcileStatus derives the .status projection from the typed lifecycle
// audit history. A missing or corrupt file is rebuilt; an existing file is
// append-only: legacy runtime lines are preserved and missing derived lines
// are appended in audit order.
func (s *Store) reconcileStatus(taskID string, audit []taskauthority.AuditEvent) (ProjectionState, error) {
	derived := deriveStatusLines(audit)
	existing, existingErr := home.ReadStatus(s.homeDir, taskID)
	corrupt := projectionFileCorrupt(s.homeDir, taskID, ".status")
	if existingErr != nil && !errors.Is(existingErr, os.ErrNotExist) {
		corrupt = true
		existing = nil
	}
	if corrupt {
		if err := home.WriteStatus(s.homeDir, taskID, derived); err != nil {
			return ProjectionFailed, err
		}
		return ProjectionRepaired, nil
	}
	present := map[string]bool{}
	for _, line := range existing {
		present[line] = true
	}
	appended := 0
	for _, line := range derived {
		if present[line] {
			continue
		}
		if err := home.AppendStatus(s.homeDir, taskID, line); err != nil {
			return ProjectionFailed, err
		}
		present[line] = true
		appended++
	}
	if appended > 0 {
		return ProjectionRepaired, nil
	}
	return ProjectionUnchanged, nil
}

// projectionFileCorrupt reports whether a projection file contains NUL
// bytes, the corruption signal that would otherwise parse as empty or
// garbage content. A missing file is not corrupt.
func projectionFileCorrupt(homeDir, id, ext string) bool {
	p := filepath.Join(home.StateDir(homeDir), id+ext)
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0
}

// mapsEqual reports whether two string maps carry identical key/value pairs.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
