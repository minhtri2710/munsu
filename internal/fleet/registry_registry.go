// Package fleet owns workforce execution, including Project and Captain
// registries and the Captain-Project Binding (ADR-0008 §3). The Registry is
// the canonical home-backed surface for that identity and lifecycle: it owns
// Project registration, Captain lifecycle, and the Captain-Project Binding
// through typed operations and queries. Config consumes scoped registry facts
// only; it no longer creates, retires, or rebinds them.
//
// The canonical implementation is one deep aggregate under the home state
// root. Every mutation is an atomic journaled change-set under the smallest
// scoped fenced lock (the registry aggregate), is idempotent by Operation
// identity, and is durable in the current v1 state through home's mechanics.
// There is no Store interface, in-memory fake, projection, or legacy adapter
// behind this surface.
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// FleetRegistrySchema is the current v1 schema identity of the Fleet
// Project/Captain registry. Only documents carrying exactly this identity are
// accepted; anything else fails closed.
const FleetRegistrySchema = "munsu.fleet.registry/v1"

// registryGeneration is the single generation of the registry aggregate. The
// registry is one aggregate (projects, captains, and bindings are coupled by
// the binding invariants), so it has one optimistic generation and one scope
// revision.
const registryGeneration uint64 = 1

// registryRoot is the logical home root under which the registry lives, and
// registryDir is the home-relative namespace for its documents.
const (
	registryRoot = home.RootState
	registryDir  = "fleet-registry"
)

// registryScope is the smallest fenced lock scope for the registry aggregate.
// Project and Captain mutations share this scope because the binding
// invariants couple them into one aggregate; there is no global lock.
const registryScope = "fleet-registry"

// registryDocumentKey is the durable registry aggregate document.
func registryDocumentKey() string { return registryDir + "/registry.json" }

// registryReceiptKey is the durable operation receipt for one committed
// registry mutation.
func registryReceiptKey(opID string) string { return registryDir + "/receipts/" + opID + ".json" }

// Sentinel errors reachable through errors.Is on every typed error returned
// by the Registry.
var (
	// ErrNotFound reports a Project or Captain that is not present in the
	// registry.
	ErrNotFound = errors.New("fleet: registry entry not found")
	// ErrConflict reports a mutation that violates a registry identity or
	// binding rule.
	ErrConflict = errors.New("fleet: registry conflict")
	// ErrPrecondition reports a lifecycle precondition that failed.
	ErrPrecondition = errors.New("fleet: registry lifecycle precondition failed")
	// ErrOperationConflict reports an Operation ID reused with a different
	// request digest (non-retryable identity conflict).
	ErrOperationConflict = errors.New("fleet: operation identity reused with different intent")
	// ErrInvalidInput reports a request field or record that violates
	// validation.
	ErrInvalidInput = errors.New("fleet: invalid input")
)

// RegisteredProject is a registered project in the Fleet registry.
type RegisteredProject struct {
	ID           domain.ProjectID
	Name         string
	Path         string
	Mode         string
	Yolo         bool
	RegisteredAt int64
}

// RegisteredCaptain is a registered captain in the Fleet registry. A captain owns
// at most one Project (ProjectID is zero when unbound).
type RegisteredCaptain struct {
	ID           domain.CaptainID
	Home         string
	Scope        string
	ProjectID    domain.ProjectID
	RegisteredAt int64
}

// Outcome is the committed outcome of one registry mutation.
type Outcome struct {
	HomeID    domain.HomeID
	ProjectID domain.ProjectID
	CaptainID domain.CaptainID
	Replayed  bool
}

// Registry is the canonical home-backed Fleet Project/Captain registry. It
// owns registration, lifecycle, and binding authority (ADR-0008 §3) over the
// canonical home's durable mechanics.
type Registry struct {
	h      *home.Home
	homeID domain.HomeID
	now    func() time.Time
}

// NewRegistry constructs the canonical Fleet registry over an opened
// canonical home. It derives the typed Home identity from the home's durable
// identity; every operation is bound to exactly this home.
func NewRegistry(h *home.Home) (*Registry, error) {
	if h == nil {
		return nil, errors.New("fleet: nil home")
	}
	homeID, err := domain.NewHomeID(h.Identity().ID)
	if err != nil {
		return nil, fmt.Errorf("fleet: invalid home identity: %w", err)
	}
	return &Registry{h: h, homeID: homeID, now: time.Now}, nil
}

// HomeID returns the canonical home identity this Registry is bound to.
func (r *Registry) HomeID() domain.HomeID { return r.homeID }

// --- Durable records -------------------------------------------------------

// projectRecord is the durable v1 encoding of one registered Project.
type projectRecord struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Mode          string `json:"mode,omitempty"`
	Yolo          bool   `json:"yolo,omitempty"`
	RegisteredAt  int64  `json:"registered_at_unix"`
}

// captainRecord is the durable v1 encoding of one registered Captain. The
// binding is expressed as the owning Project's ID value (empty when unbound).
type captainRecord struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Home          string `json:"home"`
	Scope         string `json:"scope,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	RegisteredAt  int64  `json:"registered_at_unix"`
}

// registryDoc is the durable registry aggregate. It carries the home scope
// revision so the next mutation can pass the correct optimistic
// expectedRevision to home.Commit.
type registryDoc struct {
	HomeRevision uint64          `json:"home_revision"`
	Projects     []projectRecord `json:"projects"`
	Captains     []captainRecord `json:"captains"`
}

// receipt pins one committed Operation identity and its outcome so replay
// returns the original result and a changed digest fails closed.
type receipt struct {
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	ProjectID   string `json:"project_id,omitempty"`
	CaptainID   string `json:"captain_id,omitempty"`
}

func (rec receipt) outcome() Outcome {
	out := Outcome{Replayed: true}
	if rec.ProjectID != "" {
		if id, err := domain.NewProjectID(rec.ProjectID); err == nil {
			out.ProjectID = id
		}
	}
	if rec.CaptainID != "" {
		if id, err := domain.NewCaptainID(rec.CaptainID); err == nil {
			out.CaptainID = id
		}
	}
	return out
}

func receiptFor(op domain.Operation, out Outcome) receipt {
	return receipt{
		OperationID: op.ID.Value(),
		Digest:      op.Digest,
		ProjectID:   out.ProjectID.Value(),
		CaptainID:   out.CaptainID.Value(),
	}
}

// clone returns a shallow copy of the registry aggregate for mutation.
func (d *registryDoc) clone() *registryDoc {
	cp := *d
	cp.Projects = append([]projectRecord(nil), d.Projects...)
	cp.Captains = append([]captainRecord(nil), d.Captains...)
	return &cp
}

// --- Reads ---------------------------------------------------------------

// readDoc reads one canonical document under the home state root, reporting
// absence via the boolean. All document reads go through home.Read.
func (r *Registry) readDoc(key string) ([]byte, bool, error) {
	data, err := r.h.Read(registryRoot, key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// readRegistryDoc reads and validates the current registry aggregate. A fresh
// registry (no document yet) is reported as an empty aggregate at revision 0.
// Malformed current state fails closed instead of being served.
func (r *Registry) readRegistryDoc() (*registryDoc, error) {
	data, ok, err := r.readDoc(registryDocumentKey())
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return &registryDoc{}, nil
	}
	var doc registryDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, internalError("decode fleet registry document: %v", err)
	}
	if err := validateRegistryDoc(&doc); err != nil {
		return nil, internalError("fleet registry has malformed current state: %v", err)
	}
	return &doc, nil
}

// checkedReceipt returns the committed receipt for an operation. A matching
// digest means the operation already committed (replay); a different digest
// is a non-retryable operation identity conflict.
func (r *Registry) checkedReceipt(op domain.Operation) (receipt, bool, error) {
	data, ok, err := r.readDoc(registryReceiptKey(op.ID.Value()))
	if err != nil || !ok {
		return receipt{}, false, err
	}
	var rec receipt
	if err := json.Unmarshal(data, &rec); err != nil {
		return receipt{}, true, internalError("decode operation receipt %s: %v", op.ID.Value(), err)
	}
	if rec.Digest != op.Digest {
		return receipt{}, false, conflictError(ErrOperationConflict, "operation %s reused with different intent", op.ID.Value())
	}
	return rec, true, nil
}

// --- Preparation ----------------------------------------------------------

// prepare validates the operation identity, verifies the digest derives from
// the typed intent, and binds the operation to this canonical home.
func (r *Registry) prepare(op domain.Operation, intent domain.Intent, homeID domain.HomeID) error {
	if err := op.Validate(); err != nil {
		return err
	}
	want, err := domain.Digest(intent)
	if err != nil {
		return err
	}
	if op.Digest != want {
		return validationError("operation %s digest does not match its typed intent", op.ID.Value())
	}
	return r.verifyHome(homeID)
}

func (r *Registry) verifyHome(homeID domain.HomeID) error {
	if homeID != r.homeID {
		return conflictError(ErrConflict, "operation targets home %s but registry is bound to %s", homeID.Canonical(), r.homeID.Canonical())
	}
	return nil
}

// verifyPrecondition fails closed with a typed domain.Conflict when the
// committed registry revision does not match the precondition. Conflicts
// originate only here and from a home.ErrConflict commit error.
func verifyPrecondition(target domain.Scoped, prec domain.Precondition, actualRev uint64) error {
	if prec.Generation == registryGeneration && prec.Revision == actualRev {
		return nil
	}
	conflict, ok := domain.ConflictFrom(target, prec, home.ErrConflict, func(e error) bool { return errors.Is(e, home.ErrConflict) })
	if !ok {
		return conflictError(ErrConflict, "registry stale precondition for %s", target.Canonical())
	}
	return conflict.WithActual(registryGeneration, actualRev)
}

// commitError maps a home.Commit conflict (optimistic revision mismatch) to a
// typed domain.Conflict; every other storage error keeps its category.
func commitError(target domain.Scoped, prec domain.Precondition, err error) error {
	if errors.Is(err, home.ErrConflict) {
		conflict, ok := domain.ConflictFrom(target, prec, err, func(e error) bool { return errors.Is(e, home.ErrConflict) })
		if ok {
			return conflict
		}
	}
	return err
}

// mutate runs one registry-scoped mutation under the registry aggregate's
// fenced lock: receipt idempotency first, then precondition verification, then
// one atomic home.Commit that writes the new registry aggregate and the
// operation receipt together. Natural idempotency (a no-op definition) is
// detected by apply and returns without committing.
func (r *Registry) mutate(op domain.Operation, req domain.Intent, homeID domain.HomeID, target domain.Scoped, prec domain.Precondition, apply func(*registryDoc) (Outcome, bool, error)) (Outcome, error) {
	if err := r.prepare(op, req, homeID); err != nil {
		return Outcome{}, err
	}
	if err := prec.Validate(); err != nil {
		return Outcome{}, err
	}
	if prec.Generation != registryGeneration {
		return Outcome{}, validationError("registry precondition generation must be %d", registryGeneration)
	}
	lk, err := r.h.Lock(registryScope)
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := r.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	doc, err := r.readRegistryDoc()
	if err != nil {
		return Outcome{}, err
	}
	if err := verifyPrecondition(target, prec, doc.HomeRevision); err != nil {
		return Outcome{}, err
	}
	next := doc.clone()
	next.HomeRevision = doc.HomeRevision + 1
	outcome, changed, err := apply(next)
	if err != nil {
		return Outcome{}, err
	}
	if !changed {
		return outcome, nil
	}
	rec := receiptFor(op, outcome)
	items, err := registryItems(next, rec)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(target, prec, err)
	}
	return outcome, nil
}

func registryItems(doc *registryDoc, rec receipt) ([]home.ChangeItem, error) {
	docData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: registryRoot, Key: registryDocumentKey(), Data: docData},
		{Root: registryRoot, Key: registryReceiptKey(rec.OperationID), Data: recData},
	}, nil
}

// --- Registry document validation -----------------------------------------

// validateRegistryDoc rejects malformed or mutually inconsistent current
// state. A registry aggregate must carry the current v1 identity, unique
// Project and Captain IDs, and consistent bindings (one Project per Captain
// and at most one owning Captain per Project).
func validateRegistryDoc(doc *registryDoc) error {
	projects := make(map[string]struct{}, len(doc.Projects))
	for _, p := range doc.Projects {
		if p.SchemaVersion != FleetRegistrySchema {
			return fmt.Errorf("project %q has schema %q, want %q", p.ID, p.SchemaVersion, FleetRegistrySchema)
		}
		if p.ID == "" || p.Name == "" {
			return fmt.Errorf("project record requires id and name")
		}
		if _, exists := projects[p.ID]; exists {
			return fmt.Errorf("duplicate project %q", p.ID)
		}
		projects[p.ID] = struct{}{}
	}
	captains := make(map[string]struct{}, len(doc.Captains))
	owners := make(map[string]string, len(doc.Captains))
	for _, c := range doc.Captains {
		if c.SchemaVersion != FleetRegistrySchema {
			return fmt.Errorf("captain %q has schema %q, want %q", c.ID, c.SchemaVersion, FleetRegistrySchema)
		}
		if c.ID == "" || c.Home == "" {
			return fmt.Errorf("captain record requires id and home")
		}
		if _, exists := captains[c.ID]; exists {
			return fmt.Errorf("duplicate captain %q", c.ID)
		}
		captains[c.ID] = struct{}{}
		if c.ProjectID == "" {
			continue
		}
		if _, exists := projects[c.ProjectID]; !exists {
			return fmt.Errorf("captain %q references unknown project %q", c.ID, c.ProjectID)
		}
		if owner, exists := owners[c.ProjectID]; exists {
			return fmt.Errorf("project %q is already owned by captain %q", c.ProjectID, owner)
		}
		owners[c.ProjectID] = c.ID
	}
	return nil
}

// --- Queries --------------------------------------------------------------

// Revision returns the current optimistic revision of the registry aggregate,
// for building a Precondition on the next mutation.
func (r *Registry) Revision() (uint64, error) {
	doc, err := r.readRegistryDoc()
	if err != nil {
		return 0, err
	}
	return doc.HomeRevision, nil
}

// GetProject returns the current authoritative Project record.
func (r *Registry) GetProject(projectID domain.ProjectID) (RegisteredProject, error) {
	if err := projectID.Validate(); err != nil {
		return RegisteredProject{}, err
	}
	doc, err := r.readRegistryDoc()
	if err != nil {
		return RegisteredProject{}, err
	}
	for _, p := range doc.Projects {
		if p.ID == projectID.Value() {
			return projectFromRecord(p), nil
		}
	}
	return RegisteredProject{}, conflictError(ErrNotFound, "project %s not found", projectID.Value())
}

// ListProjects returns the registered Projects sorted by ID.
func (r *Registry) ListProjects() ([]RegisteredProject, error) {
	doc, err := r.readRegistryDoc()
	if err != nil {
		return nil, err
	}
	out := make([]RegisteredProject, 0, len(doc.Projects))
	for _, p := range doc.Projects {
		out = append(out, projectFromRecord(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Value() < out[j].ID.Value() })
	return out, nil
}

// GetCaptain returns the current authoritative Captain record.
func (r *Registry) GetCaptain(captainID domain.CaptainID) (RegisteredCaptain, error) {
	if err := captainID.Validate(); err != nil {
		return RegisteredCaptain{}, err
	}
	doc, err := r.readRegistryDoc()
	if err != nil {
		return RegisteredCaptain{}, err
	}
	for _, c := range doc.Captains {
		if c.ID == captainID.Value() {
			return captainFromRecord(c), nil
		}
	}
	return RegisteredCaptain{}, conflictError(ErrNotFound, "captain %s not found", captainID.Value())
}

// ListCaptains returns the registered Captains sorted by ID.
func (r *Registry) ListCaptains() ([]RegisteredCaptain, error) {
	doc, err := r.readRegistryDoc()
	if err != nil {
		return nil, err
	}
	out := make([]RegisteredCaptain, 0, len(doc.Captains))
	for _, c := range doc.Captains {
		out = append(out, captainFromRecord(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Value() < out[j].ID.Value() })
	return out, nil
}

// OwnerOf returns the Captain that owns the given Project, or the zero
// CaptainID when the Project is unowned. A missing Project fails closed.
func (r *Registry) OwnerOf(projectID domain.ProjectID) (domain.CaptainID, error) {
	if err := projectID.Validate(); err != nil {
		return domain.CaptainID{}, err
	}
	doc, err := r.readRegistryDoc()
	if err != nil {
		return domain.CaptainID{}, err
	}
	found := false
	for _, p := range doc.Projects {
		if p.ID == projectID.Value() {
			found = true
			break
		}
	}
	if !found {
		return domain.CaptainID{}, conflictError(ErrNotFound, "project %s not found", projectID.Value())
	}
	for _, c := range doc.Captains {
		if c.ProjectID == projectID.Value() {
			id, err := domain.NewCaptainID(c.ID)
			if err != nil {
				return domain.CaptainID{}, err
			}
			return id, nil
		}
	}
	return domain.CaptainID{}, nil
}

// ProjectOf returns the Project bound to the given Captain, or the zero
// ProjectID when the Captain is unbound. A missing Captain fails closed.
func (r *Registry) ProjectOf(captainID domain.CaptainID) (domain.ProjectID, error) {
	if err := captainID.Validate(); err != nil {
		return domain.ProjectID{}, err
	}
	doc, err := r.readRegistryDoc()
	if err != nil {
		return domain.ProjectID{}, err
	}
	for _, c := range doc.Captains {
		if c.ID == captainID.Value() {
			if c.ProjectID == "" {
				return domain.ProjectID{}, nil
			}
			id, err := domain.NewProjectID(c.ProjectID)
			if err != nil {
				return domain.ProjectID{}, err
			}
			return id, nil
		}
	}
	return domain.ProjectID{}, conflictError(ErrNotFound, "captain %s not found", captainID.Value())
}

func projectFromRecord(rec projectRecord) RegisteredProject {
	id, _ := domain.NewProjectID(rec.ID)
	return RegisteredProject{
		ID:           id,
		Name:         rec.Name,
		Path:         rec.Path,
		Mode:         rec.Mode,
		Yolo:         rec.Yolo,
		RegisteredAt: rec.RegisteredAt,
	}
}

func captainFromRecord(rec captainRecord) RegisteredCaptain {
	id, _ := domain.NewCaptainID(rec.ID)
	projectID, _ := domain.NewProjectID(rec.ProjectID)
	return RegisteredCaptain{
		ID:           id,
		Home:         rec.Home,
		Scope:        rec.Scope,
		ProjectID:    projectID,
		RegisteredAt: rec.RegisteredAt,
	}
}

// --- Errors ---------------------------------------------------------------

func conflictError(sentinel error, format string, args ...any) error {
	return domain.NewError(domain.ErrorConflict, fmt.Sprintf(format, args...), domain.RetryNever, sentinel)
}

func validationError(format string, args ...any) error {
	return domain.NewError(domain.ErrorValidation, fmt.Sprintf(format, args...), domain.RetryNever, ErrInvalidInput)
}

func internalError(format string, args ...any) error {
	return domain.NewError(domain.ErrorInternal, fmt.Sprintf(format, args...), domain.RetryNever, nil)
}

// requireNonEmpty fails closed when a request-required field is blank.
func requireNonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return validationError("%s is required", field)
	}
	return nil
}
