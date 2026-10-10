package fleet

import (
	"encoding/json"
	"slices"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// preconditionDigest is the canonical precondition shape inside a request
// digest.
type preconditionDigest struct {
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
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
	Yolo         bool
	Precondition domain.Precondition
	Reason       string
}

func (r RegisterProjectRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string             `json:"home_id"`
		ProjectID    string             `json:"project_id"`
		Name         string             `json:"name"`
		Path         string             `json:"path"`
		Yolo         bool               `json:"yolo"`
		Precondition preconditionDigest `json:"precondition"`
		Reason       string             `json:"reason"`
	}{r.HomeID.Value(), r.ProjectID.Value(), r.Name, r.Path, r.Yolo, preconditionDigest(r.Precondition), r.Reason})
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
	lk, replay, replayed, err := r.begin(op, req, req.HomeID, req.Precondition, projectRegistryScope)
	if err != nil || replayed {
		return replay, err
	}
	defer lk.Release()

	doc, err := r.readProjectRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(req.ProjectID, req.Precondition, doc.HomeRevision); err != nil {
		return Outcome{}, err
	}
	for i := range doc.Projects {
		if doc.Projects[i].ID != req.ProjectID.Value() {
			continue
		}
		if doc.Projects[i].Name == req.Name && doc.Projects[i].Path == req.Path && doc.Projects[i].Yolo == req.Yolo {
			return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, nil
		}
		return Outcome{}, conflictError(ErrConflict, "project %s already exists with a different definition", req.ProjectID.Value())
	}
	next := doc
	next.HomeRevision++
	next.Projects = append(next.Projects, projectRecord{
		SchemaVersion: FleetRegistrySchema,
		ID:            req.ProjectID.Value(),
		Name:          req.Name,
		Path:          req.Path,
		Yolo:          req.Yolo,
		RegisteredAt:  r.now().Unix(),
	})
	rec := receiptFor(op, Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID})
	items, err := registryItems(rec, projectsKey, next)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.ProjectID, req.Precondition, err)
	}
	return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, nil
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
		HomeID       string             `json:"home_id"`
		ProjectID    string             `json:"project_id"`
		Precondition preconditionDigest `json:"precondition"`
		Reason       string             `json:"reason"`
	}{r.HomeID.Value(), r.ProjectID.Value(), preconditionDigest(r.Precondition), r.Reason})
}

// RetireProject is the canonical operation that retires one Project. A
// Project that is still owned by a Captain cannot be retired (fail closed);
// the Captain must be unbound or retired first.
func (r *Registry) RetireProject(op domain.Operation, req RetireProjectRequest) (Outcome, error) {
	lk, replay, replayed, err := r.begin(op, req, req.HomeID, req.Precondition, projectRegistryScope)
	if err != nil || replayed {
		return replay, err
	}
	defer lk.Release()

	doc, err := r.readProjectRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(req.ProjectID, req.Precondition, doc.HomeRevision); err != nil {
		return Outcome{}, err
	}
	idx := -1
	for i := range doc.Projects {
		if doc.Projects[i].ID == req.ProjectID.Value() {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Outcome{}, conflictError(ErrNotFound, "project %s not found", req.ProjectID.Value())
	}
	// Hold the binding lock to prevent a concurrent Bind from claiming the
	// project between the ownership check and the retirement.
	blk, err := r.h.Lock(bindingScope)
	if err != nil {
		return Outcome{}, err
	}
	defer blk.Release()
	bind, err := r.readBindingDoc()
	if err != nil {
		return Outcome{}, err
	}
	for _, b := range bind.Bindings {
		if b.ProjectID == req.ProjectID.Value() {
			return Outcome{}, conflictError(ErrConflict, "project %s is still owned by captain %s", req.ProjectID.Value(), b.CaptainID)
		}
	}
	next := doc
	next.HomeRevision++
	next.Projects = append(next.Projects[:idx], next.Projects[idx+1:]...)
	rec := receiptFor(op, Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID})
	items, err := registryItems(rec, projectsKey, next)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.ProjectID, req.Precondition, err)
	}
	return Outcome{HomeID: req.HomeID, ProjectID: req.ProjectID}, nil
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
		HomeID       string             `json:"home_id"`
		CaptainID    string             `json:"captain_id"`
		Home         string             `json:"home"`
		Scope        string             `json:"scope"`
		Precondition preconditionDigest `json:"precondition"`
		Reason       string             `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), r.Home, r.Scope, preconditionDigest(r.Precondition), r.Reason})
}

// RegisterCaptain is the canonical operation that registers one Captain.
// Registering an existing Captain with an identical definition is a
// successful no-op; a different definition under the same ID conflicts.
func (r *Registry) RegisterCaptain(op domain.Operation, req RegisterCaptainRequest) (Outcome, error) {
	if err := requireNonEmpty("captain home", req.Home); err != nil {
		return Outcome{}, err
	}
	lk, replay, replayed, err := r.begin(op, req, req.HomeID, req.Precondition, captainRegistryScope)
	if err != nil || replayed {
		return replay, err
	}
	defer lk.Release()

	doc, err := r.readCaptainRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(req.CaptainID, req.Precondition, doc.HomeRevision); err != nil {
		return Outcome{}, err
	}
	for i := range doc.Captains {
		if doc.Captains[i].ID != req.CaptainID.Value() {
			continue
		}
		if doc.Captains[i].Home == req.Home && doc.Captains[i].Scope == req.Scope {
			return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, nil
		}
		return Outcome{}, conflictError(ErrConflict, "captain %s already exists with a different definition", req.CaptainID.Value())
	}
	next := doc
	next.HomeRevision++
	next.Captains = append(next.Captains, captainRecord{
		SchemaVersion: FleetRegistrySchema,
		ID:            req.CaptainID.Value(),
		Home:          req.Home,
		Scope:         req.Scope,
		RegisteredAt:  r.now().Unix(),
	})
	rec := receiptFor(op, Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID})
	items, err := registryItems(rec, captainsKey, next)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.CaptainID, req.Precondition, err)
	}
	return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, nil
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
		HomeID       string             `json:"home_id"`
		CaptainID    string             `json:"captain_id"`
		Precondition preconditionDigest `json:"precondition"`
		Reason       string             `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), preconditionDigest(r.Precondition), r.Reason})
}

// RetireCaptain is the canonical operation that retires one Captain. Retiring
// a Captain clears its Project binding (leaving the Project unowned) and then
// removes the Captain from the registry. The binding is cleared before the
// Captain is removed so an interrupted retirement leaves a valid unbound
// Captain rather than a dangling binding.
func (r *Registry) RetireCaptain(op domain.Operation, req RetireCaptainRequest) (Outcome, error) {
	lk, replay, replayed, err := r.begin(op, req, req.HomeID, req.Precondition, captainRegistryScope)
	if err != nil || replayed {
		return replay, err
	}
	defer lk.Release()

	doc, err := r.readCaptainRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(req.CaptainID, req.Precondition, doc.HomeRevision); err != nil {
		return Outcome{}, err
	}
	idx := -1
	for i := range doc.Captains {
		if doc.Captains[i].ID == req.CaptainID.Value() {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Outcome{}, conflictError(ErrNotFound, "captain %s not found", req.CaptainID.Value())
	}

	blk, err := r.h.Lock(bindingScope)
	if err != nil {
		return Outcome{}, err
	}
	defer blk.Release()

	// Phase 1: clear the Captain's binding (Binding aggregate).
	bind, err := r.readBindingDoc()
	if err != nil {
		return Outcome{}, err
	}
	cleared, keep := clearBinding(bind, req.CaptainID.Value())
	if cleared {
		nextBind := bind
		nextBind.HomeRevision++
		nextBind.Bindings = keep
		bindItem, err := bindingDocumentItem(nextBind)
		if err != nil {
			return Outcome{}, err
		}
		// Write only the binding change so a crash leaves the Captain unbound;
		// the receipt is written with the final Captain removal.
		if _, err := r.h.Commit(blk, op.ID.Value()+"-bind", bind.HomeRevision, []home.ChangeItem{bindItem}); err != nil {
			return Outcome{}, commitError(req.CaptainID, req.Precondition, err)
		}
	}

	// Phase 2: remove the Captain and write the receipt.
	next := doc
	next.HomeRevision++
	next.Captains = append(next.Captains[:idx], next.Captains[idx+1:]...)
	rec := receiptFor(op, Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID})
	items, err := registryItems(rec, captainsKey, next)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.CaptainID, req.Precondition, err)
	}
	return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID}, nil
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
		HomeID       string             `json:"home_id"`
		CaptainID    string             `json:"captain_id"`
		ProjectID    string             `json:"project_id"`
		Precondition preconditionDigest `json:"precondition"`
		Reason       string             `json:"reason"`
	}{r.HomeID.Value(), r.CaptainID.Value(), r.ProjectID.Value(), preconditionDigest(r.Precondition), r.Reason})
}

// BindCaptain is the canonical operation that binds one Captain to one
// Project under the registry invariants: one Project per Captain and at most
// one owning Captain per Project. Binding a Captain that is already bound to
// this Project is a successful no-op; binding to a Project owned by another
// Captain conflicts. Rebinding a Captain from one Project to another is atomic
// within the Binding aggregate.
func (r *Registry) BindCaptain(op domain.Operation, req BindCaptainRequest) (Outcome, error) {
	// Serialize on the binding aggregate only. The project and captain
	// existence checks are reads; the only operations that remove those
	// entities (RetireProject, RetireCaptain) both take the binding lock in
	// their critical section, so holding it excludes a concurrent retirement
	// of the bound entities. RegisterProject/RegisterCaptain only add
	// entities and never invalidate a bind. Taking all three aggregate locks
	// would serialize unrelated Project/Captain registration while a bind
	// executes; the binding scope alone is the smallest truthful scope.
	blk, replay, replayed, err := r.begin(op, req, req.HomeID, req.Precondition, bindingScope)
	if err != nil || replayed {
		return replay, err
	}
	defer blk.Release()

	pdoc, err := r.readProjectRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if !slices.ContainsFunc(pdoc.Projects, func(p projectRecord) bool { return p.ID == req.ProjectID.Value() }) {
		return Outcome{}, conflictError(ErrNotFound, "project %s not found", req.ProjectID.Value())
	}
	cdoc, err := r.readCaptainRegistry()
	if err != nil {
		return Outcome{}, err
	}
	if !slices.ContainsFunc(cdoc.Captains, func(c captainRecord) bool { return c.ID == req.CaptainID.Value() }) {
		return Outcome{}, conflictError(ErrNotFound, "captain %s not found", req.CaptainID.Value())
	}

	bind, err := r.readBindingDoc()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(req.CaptainID, req.Precondition, bind.HomeRevision); err != nil {
		return Outcome{}, err
	}
	// At most one owning Captain per Project.
	for _, b := range bind.Bindings {
		if b.ProjectID == req.ProjectID.Value() && b.CaptainID != req.CaptainID.Value() {
			return Outcome{}, conflictError(ErrConflict, "project %s is already owned by captain %s", req.ProjectID.Value(), b.CaptainID)
		}
	}
	// Already bound to this Project -> no-op.
	for _, b := range bind.Bindings {
		if b.ProjectID == req.ProjectID.Value() && b.CaptainID == req.CaptainID.Value() {
			return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID, ProjectID: req.ProjectID}, nil
		}
	}
	next := bind
	next.HomeRevision++
	next.Bindings = upsertBinding(bind.Bindings, req.ProjectID.Value(), req.CaptainID.Value())
	rec := receiptFor(op, Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID, ProjectID: req.ProjectID})
	items, err := registryItems(rec, bindingsKey, next)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(blk, op.ID.Value(), bind.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.CaptainID, req.Precondition, err)
	}
	return Outcome{HomeID: req.HomeID, CaptainID: req.CaptainID, ProjectID: req.ProjectID}, nil
}

// begin runs the preamble every registry operation shares: it validates op
// against its typed intent and home, refuses any precondition generation but
// the registry's, takes the scope lock and replays a recorded receipt. When
// replayed is true the caller returns out; otherwise the caller owns lk and
// releases it.
func (r *Registry) begin(op domain.Operation, intent domain.Intent, homeID domain.HomeID, prec domain.Precondition, scope string) (lk *home.Lock, out Outcome, replayed bool, err error) {
	if err := r.prepare(op, intent, homeID); err != nil {
		return nil, Outcome{}, false, err
	}
	if prec.Generation != registryGeneration {
		return nil, Outcome{}, false, validationError("registry precondition generation must be %d", registryGeneration)
	}
	lk, err = r.h.Lock(scope)
	if err != nil {
		return nil, Outcome{}, false, err
	}
	rec, ok, err := r.checkedReceipt(op)
	if err != nil {
		lk.Release()
		return nil, Outcome{}, false, err
	}
	if ok {
		lk.Release()
		return nil, rec.outcome(), true, nil
	}
	return lk, Outcome{}, false, nil
}

// --- Change items ----------------------------------------------------------

// registryItems encodes one registry document under key and the operation
// receipt as the change-set of one Home.Commit.
func registryItems(rec receipt, key string, doc any) ([]home.ChangeItem, error) {
	docData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: registryRoot, Key: key, Data: docData},
		{Root: registryRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}, nil
}

func bindingDocumentItem(doc bindingDoc) (home.ChangeItem, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return home.ChangeItem{}, err
	}
	return home.ChangeItem{Root: registryRoot, Key: bindingsKey, Data: data}, nil
}

// clearBinding removes the binding for captainID. It returns whether a
// binding was removed and the resulting binding list.
func clearBinding(bind bindingDoc, captainID string) (bool, []bindingRecord) {
	keep := make([]bindingRecord, 0, len(bind.Bindings))
	cleared := false
	for _, b := range bind.Bindings {
		if b.CaptainID == captainID {
			cleared = true
			continue
		}
		keep = append(keep, b)
	}
	return cleared, keep
}

// upsertBinding sets or replaces the binding for projectID to captainID,
// releasing any prior Project owned by captainID.
func upsertBinding(bindings []bindingRecord, projectID, captainID string) []bindingRecord {
	out := make([]bindingRecord, 0, len(bindings)+1)
	for _, b := range bindings {
		if b.CaptainID == captainID || b.ProjectID == projectID {
			continue
		}
		out = append(out, b)
	}
	return append(out, bindingRecord{ProjectID: projectID, CaptainID: captainID})
}
