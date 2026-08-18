package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// newTestRegistry builds a canonical Fleet registry over a fresh real
// temporary home. There is no in-memory fake on the canonical path.
func newTestRegistry(t *testing.T) (*Registry, *home.Home, string) {
	t.Helper()
	root := t.TempDir()
	h, err := home.Init(root)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	r, err := NewRegistry(h)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r, h, root
}

func mustOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

func mustProjectID(t *testing.T, value string) domain.ProjectID {
	t.Helper()
	id, err := domain.NewProjectID(value)
	if err != nil {
		t.Fatalf("NewProjectID(%s): %v", value, err)
	}
	return id
}

func mustCaptainID(t *testing.T, value string) domain.CaptainID {
	t.Helper()
	id, err := domain.NewCaptainID(value)
	if err != nil {
		t.Fatalf("NewCaptainID(%s): %v", value, err)
	}
	return id
}

func mustRegisterProject(t *testing.T, r *Registry, name string) {
	t.Helper()
	rev, err := r.ProjectRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, name),
		Name:         name,
		Path:         "/proj/" + name,
		Mode:         "no-mistakes",
		Precondition: preconditionOf(rev),
		Reason:       "register",
	}
	if _, err := r.RegisterProject(mustOp(t, "op-reg-proj-"+name, req), req); err != nil {
		t.Fatalf("RegisterProject(%s): %v", name, err)
	}
}

func mustRegisterCaptain(t *testing.T, r *Registry, id string) {
	t.Helper()
	rev, err := r.CaptainRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := RegisterCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, id),
		Home:         "/captain/" + id,
		Scope:        "domain",
		Precondition: preconditionOf(rev),
		Reason:       "register",
	}
	if _, err := r.RegisterCaptain(mustOp(t, "op-reg-cap-"+id, req), req); err != nil {
		t.Fatalf("RegisterCaptain(%s): %v", id, err)
	}
}

func mustBind(t *testing.T, r *Registry, captainID, projectID string) {
	t.Helper()
	rev, err := r.BindingRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, captainID),
		ProjectID:    mustProjectID(t, projectID),
		Precondition: preconditionOf(rev),
		Reason:       "bind",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-"+captainID+"-"+projectID, req), req); err != nil {
		t.Fatalf("BindCaptain(%s->%s): %v", captainID, projectID, err)
	}
}

func TestRegistryRegisterQuery(t *testing.T) {
	r, _, _ := newTestRegistry(t)

	mustRegisterProject(t, r, "alpha")
	mustRegisterProject(t, r, "beta")
	mustRegisterCaptain(t, r, "c1")
	mustRegisterCaptain(t, r, "c2")

	proj, err := r.GetProject(mustProjectID(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if proj.ID.Value() != "alpha" || proj.Name != "alpha" || proj.Path != "/proj/alpha" || proj.Mode != "no-mistakes" {
		t.Fatalf("GetProject = %+v", proj)
	}

	projects, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].ID.Value() != "alpha" || projects[1].ID.Value() != "beta" {
		t.Fatalf("ListProjects = %+v", projects)
	}

	captains, err := r.ListCaptains()
	if err != nil {
		t.Fatal(err)
	}
	if len(captains) != 2 || captains[0].ID.Value() != "c1" || captains[1].ID.Value() != "c2" {
		t.Fatalf("ListCaptains = %+v", captains)
	}

	if _, err := r.GetProject(mustProjectID(t, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject(missing) = %v, want ErrNotFound", err)
	}
	if _, err := r.GetCaptain(mustCaptainID(t, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCaptain(missing) = %v, want ErrNotFound", err)
	}
}

func TestRegistryBindingInvariants(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterProject(t, r, "beta")
	mustRegisterProject(t, r, "gamma")
	mustRegisterCaptain(t, r, "c1")
	mustRegisterCaptain(t, r, "c2")

	// c1 owns alpha; c2 owns beta.
	mustBind(t, r, "c1", "alpha")
	mustBind(t, r, "c2", "beta")

	owner, err := bindingOwnerOf(r, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "c1" {
		t.Fatalf("owner of alpha = %s, want c1", owner)
	}
	projOf, err := r.ProjectOf(mustCaptainID(t, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if projOf != mustProjectID(t, "alpha") {
		t.Fatalf("ProjectOf(c1) = %s, want alpha", projOf.Value())
	}

	// A second captain cannot own the same project (at most one owning captain).
	rev, err := r.BindingRevision()
	if err != nil {
		t.Fatal(err)
	}
	conflict := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c2"),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "steal",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-conflict", conflict), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("second owner bind = %v, want ErrConflict", err)
	}

	// Rebinding c1 from alpha to gamma leaves alpha unowned and c1 on gamma.
	rev, _ = r.BindingRevision()
	rebind := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c1"),
		ProjectID:    mustProjectID(t, "gamma"),
		Precondition: preconditionOf(rev),
		Reason:       "rebind",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-rebind", rebind), rebind); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	// ProjectOf(c1) alone cannot prove this: it returns the FIRST binding that
	// matches c1, so a stale (c1, alpha) row left behind after (c1, gamma)
	// would go unnoticed. The reverse lookup is what checks alpha is unowned.
	owner, err = bindingOwnerOf(r, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		t.Fatalf("alpha should be unowned after rebind, got %s", owner)
	}
	projOf, err = r.ProjectOf(mustCaptainID(t, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if projOf != mustProjectID(t, "gamma") {
		t.Fatalf("ProjectOf(c1) after rebind = %s, want gamma", projOf.Value())
	}
}

func TestRegistryBindUnknownFailsClosed(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")

	// Unknown captain.
	rev, _ := r.BindingRevision()
	req := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "nope"),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "bind",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-unknown-cap", req), req); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind unknown captain = %v, want ErrNotFound", err)
	}

	// Unknown project.
	rev, _ = r.BindingRevision()
	req = BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c1"),
		ProjectID:    mustProjectID(t, "nope"),
		Precondition: preconditionOf(rev),
		Reason:       "bind",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-unknown-proj", req), req); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind unknown project = %v, want ErrNotFound", err)
	}
}

func TestRegistryRetire(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")
	mustBind(t, r, "c1", "alpha")

	// A bound project cannot be retired.
	rev, _ := r.ProjectRevision()
	reject := RetireProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "retire",
	}
	if _, err := r.RetireProject(mustOp(t, "op-retire-bound", reject), reject); !errors.Is(err, ErrConflict) {
		t.Fatalf("retire bound project = %v, want ErrConflict", err)
	}

	// Retiring the captain clears the binding; the project becomes unowned and
	// can then be retired.
	rev, _ = r.CaptainRevision()
	capRetire := RetireCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c1"),
		Precondition: preconditionOf(rev),
		Reason:       "retire",
	}
	if _, err := r.RetireCaptain(mustOp(t, "op-retire-cap", capRetire), capRetire); err != nil {
		t.Fatalf("RetireCaptain: %v", err)
	}
	owner, err := bindingOwnerOf(r, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		t.Fatalf("alpha should be unowned after captain retire, got %s", owner)
	}

	rev, _ = r.ProjectRevision()
	projRetire := RetireProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "retire",
	}
	if _, err := r.RetireProject(mustOp(t, "op-retire-proj", projRetire), projRetire); err != nil {
		t.Fatalf("RetireProject: %v", err)
	}
	if _, err := r.GetProject(mustProjectID(t, "alpha")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject(retired) = %v, want ErrNotFound", err)
	}

	// Retiring a missing entity fails closed.
	rev, _ = r.ProjectRevision()
	missing := RetireProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "ghost"),
		Precondition: preconditionOf(rev),
		Reason:       "retire",
	}
	if _, err := r.RetireProject(mustOp(t, "op-retire-missing", missing), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retire missing project = %v, want ErrNotFound", err)
	}
}

func TestRegistryOperationReplay(t *testing.T) {
	r, _, _ := newTestRegistry(t)

	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Name:         "alpha",
		Path:         "/proj/alpha",
		Mode:         "no-mistakes",
		Precondition: preconditionOf(0),
		Reason:       "register",
	}
	op := mustOp(t, "op-register-replay", req)
	first, err := r.RegisterProject(op, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatalf("first register marked replayed: %+v", first)
	}
	second, err := r.RegisterProject(op, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("replay not marked replayed: %+v", second)
	}
	if second.ProjectID != first.ProjectID {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestRegistryOperationIDReusedWithDifferentIntent(t *testing.T) {
	r, _, _ := newTestRegistry(t)

	createReq := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Name:         "alpha",
		Path:         "/proj/alpha",
		Precondition: preconditionOf(0),
		Reason:       "register",
	}
	op := mustOp(t, "op-shared", createReq)
	if _, err := r.RegisterProject(op, createReq); err != nil {
		t.Fatal(err)
	}

	// Reuse the same Operation ID with a different intent (a different project).
	other := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "beta"),
		Name:         "beta",
		Path:         "/proj/beta",
		Precondition: preconditionOf(0),
		Reason:       "register",
	}
	reused, err := domain.NewOperation(op.ID, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterProject(reused, other); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func TestRegistryStalePreconditionConflict(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")

	// Expect revision 9, but the current project revision is 1.
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "beta"),
		Name:         "beta",
		Path:         "/proj/beta",
		Precondition: preconditionOf(9),
		Reason:       "register",
	}
	_, err := r.RegisterProject(mustOp(t, "op-stale", req), req)
	if !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale register = %v, want domain.ErrStalePrecondition", err)
	}
	var conflict *domain.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error is not a typed domain.Conflict: %v", err)
	}
	if conflict.ExpectedRevision != 9 {
		t.Fatalf("conflict expected revision = %d, want 9", conflict.ExpectedRevision)
	}
}

func TestRegistryWrongHomeFailsClosed(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	otherHome, err := domain.NewHomeID("other-home")
	if err != nil {
		t.Fatal(err)
	}
	req := RegisterProjectRequest{
		HomeID:       otherHome,
		ProjectID:    mustProjectID(t, "alpha"),
		Name:         "alpha",
		Path:         "/proj/alpha",
		Precondition: preconditionOf(0),
		Reason:       "register",
	}
	if _, err := r.RegisterProject(mustOp(t, "op-wrong-home", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong home register = %v, want ErrConflict", err)
	}
}

func TestRegistryNaturalIdempotency(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")
	mustBind(t, r, "c1", "alpha")

	// Re-registering an identical project is a no-op (new operation ID).
	rev, _ := r.ProjectRevision()
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Name:         "alpha",
		Path:         "/proj/alpha",
		Mode:         "no-mistakes",
		Precondition: preconditionOf(rev),
		Reason:       "re-register",
	}
	if _, err := r.RegisterProject(mustOp(t, "op-reg-again", req), req); err != nil {
		t.Fatalf("re-register identical project: %v", err)
	}

	// A different definition under the same ID conflicts.
	rev, _ = r.ProjectRevision()
	diff := req
	diff.Path = "/proj/other"
	diff.Precondition = preconditionOf(rev)
	if _, err := r.RegisterProject(mustOp(t, "op-reg-diff", diff), diff); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-register different definition = %v, want ErrConflict", err)
	}

	// Re-binding an already-bound relationship is a no-op.
	rev, _ = r.BindingRevision()
	rebind := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c1"),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "re-bind",
	}
	if _, err := r.BindCaptain(mustOp(t, "op-bind-again", rebind), rebind); err != nil {
		t.Fatalf("re-bind same relationship: %v", err)
	}

}

func TestRegistryRereadAfterHomeReopen(t *testing.T) {
	r, _, root := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")
	mustBind(t, r, "c1", "alpha")

	// Reopen the home (mechanical recovery runs) and re-read canonical state
	// through a fresh Registry.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	r2, err := NewRegistry(h2)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := r2.GetProject(mustProjectID(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if proj.Name != "alpha" {
		t.Fatalf("reread project = %+v", proj)
	}
	owner, err := bindingOwnerOf(r2, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "c1" {
		t.Fatalf("reread owner = %s, want c1", owner)
	}
}

func TestRegistryReceiptSurvivesReopen(t *testing.T) {
	r, _, root := newTestRegistry(t)
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectID(t, "alpha"),
		Name:         "alpha",
		Path:         "/proj/alpha",
		Precondition: preconditionOf(0),
		Reason:       "register",
	}
	op := mustOp(t, "op-register-persist", req)
	if _, err := r.RegisterProject(op, req); err != nil {
		t.Fatal(err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := NewRegistry(h2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r2.RegisterProject(op, req)
	if err != nil {
		t.Fatalf("replay after reopen: %v", err)
	}
	if !out.Replayed {
		t.Fatalf("replay after reopen not marked replayed: %+v", out)
	}
}

func TestRegistryDocumentsCarryCurrentV1Identity(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")

	data, ok, err := r.readDoc(projectsKey)
	if err != nil || !ok {
		t.Fatalf("read project doc: ok=%v err=%v", ok, err)
	}
	var doc projectRegistryDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if FleetRegistrySchema != "munsu.fleet.registry/v1" {
		t.Fatalf("FleetRegistrySchema = %q, want the current v1 identity", FleetRegistrySchema)
	}
	if len(doc.Projects) != 1 || doc.Projects[0].SchemaVersion != FleetRegistrySchema {
		t.Fatalf("project schema = %+v", doc.Projects)
	}

	captainData, ok, err := r.readDoc(captainsKey)
	if err != nil || !ok {
		t.Fatalf("read captain doc: ok=%v err=%v", ok, err)
	}
	var cdoc captainRegistryDoc
	if err := json.Unmarshal(captainData, &cdoc); err != nil {
		t.Fatal(err)
	}
	if len(cdoc.Captains) != 1 || cdoc.Captains[0].SchemaVersion != FleetRegistrySchema {
		t.Fatalf("captain schema = %+v", cdoc.Captains)
	}

	// A current-v1 reread through the canonical surface succeeds.
	if _, err := r.GetProject(mustProjectID(t, "alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetCaptain(mustCaptainID(t, "c1")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListProjects(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryMalformedCurrentStateFailsClosed(t *testing.T) {
	r, h, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")

	path, err := h.Path(home.RootState, projectsKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	r2, err := NewRegistry(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.GetProject(mustProjectID(t, "alpha")); err == nil {
		t.Fatalf("GetProject on malformed state = nil error, want failure")
	}
	if _, err := r2.ListProjects(); err == nil {
		t.Fatalf("ListProjects on malformed state = nil error, want failure")
	}
	if _, err := r2.ProjectRevision(); err == nil {
		t.Fatalf("ProjectRevision on malformed state = nil error, want failure")
	}
}

func TestRegistryRejectsHistoricalV2Input(t *testing.T) {
	r, h, _ := newTestRegistry(t)

	// Plant a legacy v2-identity project document directly on disk. The
	// canonical surface must fail closed: it never accepts, migrates, or
	// upgrades the historical v2 identity to the current v1.
	path, err := h.Path(home.RootState, projectsKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"home_revision":1,"projects":[{"schema_version":"munsu.fleet.registry/v2","id":"legacy","name":"legacy","path":"/x"}]}`), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetProject(mustProjectID(t, "legacy")); err == nil {
		t.Fatalf("GetProject on legacy v2 document = nil error, want fail closed")
	}
	if _, err := r.ListProjects(); err == nil {
		t.Fatalf("ListProjects with legacy v2 document = nil error, want fail closed")
	}
}
