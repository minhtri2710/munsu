package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// writeTaskMeta seeds a canonical queued task with the given kind into the
// home (clean break: guards and snapshots read only authoritative Task
// Authority records).
func writeTaskMeta(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	cliSeedCanonicalTask(t, homeDir, id, kind)
}

// cliSeedCanonicalTask creates one queued canonical task of the given kind.
func cliSeedCanonicalTask(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	auth := cliCanonicalForHome(t, homeDir)
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		req.ScoutScope = "investigate scope"
		req.ScoutRuntimeBudgetSecs = 300
	}
	opID, err := domain.NewOperationID("op-create-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

// cliCanonicalForHome opens the canonical Task Authority over homeDir.
func cliCanonicalForHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// cliSeedCanonicalTaskPhase creates one canonical task at the given phase.
func cliSeedCanonicalTaskPhase(t *testing.T, homeDir, id, kind string, phase taskauthority.Phase) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	auth := cliCanonicalForHome(t, homeDir)
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		createReq.ScoutScope = "investigate scope"
		createReq.ScoutRuntimeBudgetSecs = 300
	}
	opID, err := domain.NewOperationID("op-create-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, createReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, createReq); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
	switch phase {
	case taskauthority.PhaseWorking:
		cliStartTask(t, auth, tid, "op-start-"+id)
	case taskauthority.PhaseDone:
		cliCompleteTask(t, auth, tid, "op-done-"+id, taskauthority.PhaseDone)
	}
}

// cliStartTask drives a queued canonical task into working.
func cliStartTask(t *testing.T, auth *taskauthority.Canonical, tid domain.TaskID, opID string) {
	t.Helper()
	cur, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalStartRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(cur.Generation), uint64(cur.Revision)),
		Reason:       "test",
	}
	op, err := domain.NewOperation(mustOpIDFor(t, opID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Start(op, req); err != nil {
		t.Fatalf("Start(%s): %v", tid.Value(), err)
	}
}

// cliCompleteTask drives a non-terminal canonical task into a terminal phase.
func cliCompleteTask(t *testing.T, auth *taskauthority.Canonical, tid domain.TaskID, opID string, to taskauthority.Phase) {
	t.Helper()
	cur, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(cur.Generation), uint64(cur.Revision)),
		To:           to, Reason: "test",
	}
	op, err := domain.NewOperation(mustOpIDFor(t, opID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Complete(op, req); err != nil {
		t.Fatalf("Complete(%s): %v", tid.Value(), err)
	}
}

// mustOpIDFor builds a validated operation identity.
func mustOpIDFor(t *testing.T, value string) domain.OperationID {
	t.Helper()
	id, err := domain.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
