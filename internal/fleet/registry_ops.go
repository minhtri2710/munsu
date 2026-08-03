package fleet

import (
	"encoding/json"

	"github.com/minhtri2710/munsu/internal/domain"
)

// preconditionFields returns the canonical precondition fields for a request
// digest. The registry is a single aggregate, so the precondition carries the
// registry generation and the committed revision.
func preconditionFields(p domain.Precondition) struct {
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
} {
	return struct {
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
	}{p.Generation, p.Revision}
}

func preconditionOf(rev uint64) domain.Precondition {
	return domain.Of(registryGeneration, rev)
}

// RegisterProjectRequest registers one Project in the Fleet registry.
type RegisterProjectRequest struct {
	HomeID       domain.HomeID
	ProjectID    domain.ProjectID
	Name         string
	Path         string
	Mode         string
	Yolo         bool
	Precondition domain.Precondition
	Reason       string
}

func (r RegisterProjectRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		ProjectID    string `json:"project_id"`
		Name         string `json:"name"`
		Path         string `json:"path"`
		Mode         string `json:"mode"`
		Yolo         bool   `json:"yolo"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.ProjectID.Value(), r.Name, r.Path, r.Mode, r.Yolo, preconditionFields(r.Precondition), r.Reason})
}

// RegisterProject is the canonical operation that registers one Project.
// Registering an existing Project with an identical definition is a successful
// no-op; a different definition under the same ID conflicts.
func (r *Registry) RegisterProject(op domain.Operation, req RegisterProjectRequest) (Outcome, error) {
	if err := requireNonEmpty("project name", req.Name); err != nil {
		return Outcome{}, err
	}
	if err := requireNonEmpty("project path", req.Path); err != nil {
		return Outcome{}, err
	}
	return r.mutate(op, req, req.HomeID, req.ProjectID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		for i := range doc.Projects {
			if doc.Projects[i].ID != req.ProjectID.Value() {
				continue
			}
			existing := doc.Projects[i]
			if existing.Name == req.Name && existing.Path == req.Path && existing.Mode == req.Mode && existing.Yolo == req.Yolo {
				return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, false, nil
			}
			return Outcome{}, false, conflictError(ErrConflict, "project %s already exists with a different definition", req.ProjectID.Value())
		}
		doc.Projects = append(doc.Projects, projectRecord{
			SchemaVersion: FleetRegistrySchema,
			ID:            req.ProjectID.Value(),
			Name:          req.Name,
			Path:          req.Path,
			Mode:          req.Mode,
			Yolo:          req.Yolo,
			RegisteredAt:  r.now().Unix(),
		})
		return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, true, nil
	})
}

// RetireProjectRequest retires one Project from the Fleet registry.
type RetireProjectRequest struct {
	HomeID       domain.HomeID
	ProjectID    domain.ProjectID
	Precondition domain.Precondition
	Reason       string
}

func (r RetireProjectRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		ProjectID    string `json:"project_id"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.ProjectID.Value(), preconditionFields(r.Precondition), r.Reason})
}

// RetireProject is the canonical operation that retires one Project. A
// Project that is still owned by a Captain cannot be retired (fail closed);
// the Captain must be unbound or retired first.
func (r *Registry) RetireProject(op domain.Operation, req RetireProjectRequest) (Outcome, error) {
	return r.mutate(op, req, req.HomeID, req.ProjectID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		idx := -1
		for i := range doc.Projects {
			if doc.Projects[i].ID == req.ProjectID.Value() {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Outcome{}, false, conflictError(ErrNotFound, "project %s not found", req.ProjectID.Value())
		}
		for _, c := range doc.Captains {
			if c.ProjectID == req.ProjectID.Value() {
				return Outcome{}, false, conflictError(ErrConflict, "project %s is still owned by captain %s", req.ProjectID.Value(), c.ID)
			}
		}
		doc.Projects = append(doc.Projects[:idx], doc.Projects[idx+1:]...)
		return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, true, nil
	})
}

// RegisterCaptainRequest registers one Captain in the Fleet registry.
type RegisterCaptainRequest struct {
	HomeID       domain.HomeID
	CaptainID    domain.CaptainID
	Home         string
	Scope        string
	Precondition domain.Precondition
	Reason       string
}

func (r RegisterCaptainRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		CaptainID    string `json:"captain_id"`
		Home         string `json:"home"`
		Scope        string `json:"scope"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), r.Home, r.Scope, preconditionFields(r.Precondition), r.Reason})
}

// RegisterCaptain is the canonical operation that registers one Captain.
// Registering an existing Captain with an identical definition is a
// successful no-op; a different definition under the same ID conflicts.
func (r *Registry) RegisterCaptain(op domain.Operation, req RegisterCaptainRequest) (Outcome, error) {
	if err := requireNonEmpty("captain home", req.Home); err != nil {
		return Outcome{}, err
	}
	return r.mutate(op, req, req.HomeID, req.CaptainID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		for i := range doc.Captains {
			if doc.Captains[i].ID != req.CaptainID.Value() {
				continue
			}
			existing := doc.Captains[i]
			if existing.Home == req.Home && existing.Scope == req.Scope {
				return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, false, nil
			}
			return Outcome{}, false, conflictError(ErrConflict, "captain %s already exists with a different definition", req.CaptainID.Value())
		}
		doc.Captains = append(doc.Captains, captainRecord{
			SchemaVersion: FleetRegistrySchema,
			ID:            req.CaptainID.Value(),
			Home:          req.Home,
			Scope:         req.Scope,
			RegisteredAt:  r.now().Unix(),
		})
		return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, true, nil
	})
}

// RetireCaptainRequest retires one Captain from the Fleet registry.
type RetireCaptainRequest struct {
	HomeID       domain.HomeID
	CaptainID    domain.CaptainID
	Precondition domain.Precondition
	Reason       string
}

func (r RetireCaptainRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		CaptainID    string `json:"captain_id"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), preconditionFields(r.Precondition), r.Reason})
}

// RetireCaptain is the canonical operation that retires one Captain. Retiring
// a Captain clears its Project binding, leaving the Project unowned.
func (r *Registry) RetireCaptain(op domain.Operation, req RetireCaptainRequest) (Outcome, error) {
	return r.mutate(op, req, req.HomeID, req.CaptainID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		idx := -1
		for i := range doc.Captains {
			if doc.Captains[i].ID == req.CaptainID.Value() {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Outcome{}, false, conflictError(ErrNotFound, "captain %s not found", req.CaptainID.Value())
		}
		doc.Captains = append(doc.Captains[:idx], doc.Captains[idx+1:]...)
		return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, true, nil
	})
}

// BindCaptainRequest binds one Captain to one ownable Project.
type BindCaptainRequest struct {
	HomeID       domain.HomeID
	CaptainID    domain.CaptainID
	ProjectID    domain.ProjectID
	Precondition domain.Precondition
	Reason       string
}

func (r BindCaptainRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		CaptainID    string `json:"captain_id"`
		ProjectID    string `json:"project_id"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), r.ProjectID.Value(), preconditionFields(r.Precondition), r.Reason})
}

// BindCaptain is the canonical operation that binds one Captain to one
// Project under the registry invariants: one Project per Captain (a Captain
// holds a single Project slot) and at most one owning Captain per Project.
// Binding a Captain that is already bound to this Project is a successful
// no-op; binding to a Project owned by another Captain conflicts.
func (r *Registry) BindCaptain(op domain.Operation, req BindCaptainRequest) (Outcome, error) {
	return r.mutate(op, req, req.HomeID, req.CaptainID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		ci := -1
		for i := range doc.Captains {
			if doc.Captains[i].ID == req.CaptainID.Value() {
				ci = i
				break
			}
		}
		if ci < 0 {
			return Outcome{}, false, conflictError(ErrNotFound, "captain %s not found", req.CaptainID.Value())
		}
		pi := -1
		for i := range doc.Projects {
			if doc.Projects[i].ID == req.ProjectID.Value() {
				pi = i
				break
			}
		}
		if pi < 0 {
			return Outcome{}, false, conflictError(ErrNotFound, "project %s not found", req.ProjectID.Value())
		}
		if doc.Captains[ci].ProjectID == req.ProjectID.Value() {
			return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID, ProjectID: req.ProjectID}, false, nil
		}
		for _, c := range doc.Captains {
			if c.ID != req.CaptainID.Value() && c.ProjectID == req.ProjectID.Value() {
				return Outcome{}, false, conflictError(ErrConflict, "project %s is already owned by captain %s", req.ProjectID.Value(), c.ID)
			}
		}
		doc.Captains[ci].ProjectID = req.ProjectID.Value()
		return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID, ProjectID: req.ProjectID}, true, nil
	})
}

// UnbindCaptainRequest unbinds one Captain from its Project.
type UnbindCaptainRequest struct {
	HomeID       domain.HomeID
	CaptainID    domain.CaptainID
	Precondition domain.Precondition
	Reason       string
}

func (r UnbindCaptainRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		CaptainID    string `json:"captain_id"`
		Precondition struct {
			Generation uint64 `json:"generation"`
			Revision   uint64 `json:"revision"`
		} `json:"precondition"`
		Reason string `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), preconditionFields(r.Precondition), r.Reason})
}

// UnbindCaptain is the canonical operation that unbinds one Captain from its
// Project, leaving the Project unowned. Unbinding an already-unbound Captain
// is a successful no-op.
func (r *Registry) UnbindCaptain(op domain.Operation, req UnbindCaptainRequest) (Outcome, error) {
	return r.mutate(op, req, req.HomeID, req.CaptainID, req.Precondition, func(doc *registryDoc) (Outcome, bool, error) {
		ci := -1
		for i := range doc.Captains {
			if doc.Captains[i].ID == req.CaptainID.Value() {
				ci = i
				break
			}
		}
		if ci < 0 {
			return Outcome{}, false, conflictError(ErrNotFound, "captain %s not found", req.CaptainID.Value())
		}
		if doc.Captains[ci].ProjectID == "" {
			return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, false, nil
		}
		doc.Captains[ci].ProjectID = ""
		return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, true, nil
	})
}
