// Package fleet owns workforce execution, including Project and Captain
// registries and the Captain-Project Binding (ADR-0008 §3). The Registry is
// the canonical home-backed surface for that identity and lifecycle: it owns
// Project registration, Captain lifecycle, and the Captain-Project Binding
// through typed operations and queries. Config consumes scoped registry facts
// only; it no longer creates, retires, or rebinds them.
//
// The canonical implementation uses three independent aggregates: the Project
// registry, the Captain registry, and the Captain-Project Binding. Each has its
// own scoped fenced lock and revision, so independent Project and Captain
// operations never share a lock (no fleet-wide serialization). The Binding is
// one bounded aggregate because enforcing the one-to-one invariants (one
// Project per Captain, at most one owning Captain per Project) requires atomic
// serialization of the binding state. Retirement rewrites an aggregate without
// the retired entry (home.Commit is write-only and the ADR forbids tombstones,
// so entities are never left as deletion markers). There is no Store
// interface, in-memory fake, projection, legacy adapter, or fleet-wide lock
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

// registryGeneration is the single generation of every registry aggregate.
const registryGeneration uint64 = 1

// registryRoot is the logical home root under which the registry lives, and
// registryDir is the home-relative namespace for its documents.
const (
	registryRoot = home.RootState
	registryDir  = "fleet-registry"
)

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

// Registry durable document keys under the home state root.
const (
	projectsKey = "fleet-registry/projects.json"
	captainsKey = "fleet-registry/captains.json"
	bindingsKey = "fleet-registry/bindings.json"
	receiptsDir = "fleet-registry/receipts"
)

func receiptKey(opID string) string { return receiptsDir + "/" + opID + ".json" }

// Independent fenced lock scopes for the three registry aggregates. There is
// no lock that covers more than one aggregate, so Project and Captain
// operations are truly concurrent.
const (
	projectRegistryScope = "fleet-projects"
	captainRegistryScope = "fleet-captains"
	bindingScope         = "fleet-bindings"
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

// RegisteredCaptain is a registered captain in the Fleet registry.
type RegisteredCaptain struct {
	ID           domain.CaptainID
	Home         string
	Scope        string
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
// binding is owned by the binding aggregate, not the captain record.
type captainRecord struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Home          string `json:"home"`
	Scope         string `json:"scope,omitempty"`
	RegisteredAt  int64  `json:"registered_at_unix"`
}

// projectRegistryDoc is the durable Project registry aggregate.
type projectRegistryDoc struct {
	HomeRevision uint64          `json:"home_revision"`
	Projects     []projectRecord `json:"projects"`
}

// captainRegistryDoc is the durable Captain registry aggregate.
type captainRegistryDoc struct {
	HomeRevision uint64          `json:"home_revision"`
	Captains     []captainRecord `json:"captains"`
}

// bindingRecord is one durable one-to-one claim: a Project is owned by exactly
// one Captain (at most one owning Captain per Project), and a Captain owns at
// most one Project.
type bindingRecord struct {
	ProjectID string `json:"project_id"`
	CaptainID string `json:"captain_id"`
}

// bindingDoc is the durable Binding aggregate.
type bindingDoc struct {
	HomeRevision uint64          `json:"home_revision"`
	Bindings     []bindingRecord `json:"bindings"`
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

// readProjectRegistry reads and validates the Project registry aggregate.
func (r *Registry) readProjectRegistry() (projectRegistryDoc, error) {
	data, ok, err := r.readDoc(projectsKey)
	if err != nil || !ok {
		if err != nil {
			return projectRegistryDoc{}, err
		}
		return projectRegistryDoc{}, nil
	}
	var doc projectRegistryDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return projectRegistryDoc{}, internalError("decode project registry: %v", err)
	}
	if err := validateProjectRegistry(&doc); err != nil {
		return projectRegistryDoc{}, internalError("project registry has malformed current state: %v", err)
	}
	return doc, nil
}

// readCaptainRegistry reads and validates the Captain registry aggregate.
func (r *Registry) readCaptainRegistry() (captainRegistryDoc, error) {
	data, ok, err := r.readDoc(captainsKey)
	if err != nil || !ok {
		if err != nil {
			return captainRegistryDoc{}, err
		}
		return captainRegistryDoc{}, nil
	}
	var doc captainRegistryDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return captainRegistryDoc{}, internalError("decode captain registry: %v", err)
	}
	if err := validateCaptainRegistry(&doc); err != nil {
		return captainRegistryDoc{}, internalError("captain registry has malformed current state: %v", err)
	}
	return doc, nil
}

// readBindingDoc reads and validates the Binding aggregate.
func (r *Registry) readBindingDoc() (bindingDoc, error) {
	data, ok, err := r.readDoc(bindingsKey)
	if err != nil || !ok {
		if err != nil {
			return bindingDoc{}, err
		}
		return bindingDoc{}, nil
	}
	var doc bindingDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return bindingDoc{}, internalError("decode binding document: %v", err)
	}
	if err := validateBindingDoc(&doc); err != nil {
		return bindingDoc{}, internalError("binding aggregate has malformed current state: %v", err)
	}
	return doc, nil
}

// checkedReceipt returns the committed receipt for an operation. A matching
// digest means the operation already committed (replay); a different digest
// is a non-retryable operation identity conflict.
func (r *Registry) checkedReceipt(op domain.Operation) (receipt, bool, error) {
	data, ok, err := r.readDoc(receiptKey(op.ID.Value()))
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
// committed revision does not match the precondition.
func verifyPrecondition(target domain.Scoped, prec domain.Precondition, actualRev uint64) error {
	if prec.Generation == registryGeneration && prec.Revision == actualRev {
		return nil
	}
	conflict, _ := domain.ConflictFrom(target, prec, home.ErrConflict, func(e error) bool { return errors.Is(e, home.ErrConflict) })
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

// --- Aggregate validation -------------------------------------------------

func validateProjectRegistry(doc *projectRegistryDoc) error {
	seen := make(map[string]struct{}, len(doc.Projects))
	for _, p := range doc.Projects {
		if p.SchemaVersion != FleetRegistrySchema {
			return fmt.Errorf("project %q has schema %q, want %q", p.ID, p.SchemaVersion, FleetRegistrySchema)
		}
		if p.ID == "" || p.Name == "" {
			return fmt.Errorf("project record requires id and name")
		}
		if _, exists := seen[p.ID]; exists {
			return fmt.Errorf("duplicate project %q", p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return nil
}

func validateCaptainRegistry(doc *captainRegistryDoc) error {
	seen := make(map[string]struct{}, len(doc.Captains))
	for _, c := range doc.Captains {
		if c.SchemaVersion != FleetRegistrySchema {
			return fmt.Errorf("captain %q has schema %q, want %q", c.ID, c.SchemaVersion, FleetRegistrySchema)
		}
		if c.ID == "" || c.Home == "" {
			return fmt.Errorf("captain record requires id and home")
		}
		if _, exists := seen[c.ID]; exists {
			return fmt.Errorf("duplicate captain %q", c.ID)
		}
		seen[c.ID] = struct{}{}
	}
	return nil
}

func validateBindingDoc(doc *bindingDoc) error {
	seenProject := make(map[string]struct{}, len(doc.Bindings))
	seenCaptain := make(map[string]struct{}, len(doc.Bindings))
	for _, b := range doc.Bindings {
		if b.ProjectID == "" || b.CaptainID == "" {
			return fmt.Errorf("binding record requires project and captain")
		}
		if _, exists := seenProject[b.ProjectID]; exists {
			return fmt.Errorf("project %q is owned by more than one captain", b.ProjectID)
		}
		if _, exists := seenCaptain[b.CaptainID]; exists {
			return fmt.Errorf("captain %q owns more than one project", b.CaptainID)
		}
		seenProject[b.ProjectID] = struct{}{}
		seenCaptain[b.CaptainID] = struct{}{}
	}
	return nil
}

// --- Queries --------------------------------------------------------------

// ProjectRevision returns the current revision of the Project registry aggregate.
func (r *Registry) ProjectRevision() (uint64, error) {
	doc, err := r.readProjectRegistry()
	if err != nil {
		return 0, err
	}
	return doc.HomeRevision, nil
}

// CaptainRevision returns the current revision of the Captain registry aggregate.
func (r *Registry) CaptainRevision() (uint64, error) {
	doc, err := r.readCaptainRegistry()
	if err != nil {
		return 0, err
	}
	return doc.HomeRevision, nil
}

// BindingRevision returns the current revision of the Binding aggregate.
func (r *Registry) BindingRevision() (uint64, error) {
	doc, err := r.readBindingDoc()
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
	doc, err := r.readProjectRegistry()
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
	doc, err := r.readProjectRegistry()
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
	doc, err := r.readCaptainRegistry()
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
	doc, err := r.readCaptainRegistry()
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

// ProjectOf returns the Project bound to the given Captain, or the zero
// ProjectID when the Captain is unbound. A missing Captain fails closed.
func (r *Registry) ProjectOf(captainID domain.CaptainID) (domain.ProjectID, error) {
	if err := captainID.Validate(); err != nil {
		return domain.ProjectID{}, err
	}
	if _, err := r.GetCaptain(captainID); err != nil {
		return domain.ProjectID{}, err
	}
	doc, err := r.readBindingDoc()
	if err != nil {
		return domain.ProjectID{}, err
	}
	for _, b := range doc.Bindings {
		if b.CaptainID == captainID.Value() {
			id, err := domain.NewProjectID(b.ProjectID)
			if err != nil {
				return domain.ProjectID{}, err
			}
			return id, nil
		}
	}
	return domain.ProjectID{}, nil
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
	return RegisteredCaptain{
		ID:           id,
		Home:         rec.Home,
		Scope:        rec.Scope,
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
