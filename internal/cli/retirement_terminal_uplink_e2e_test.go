//go:build e2e

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mustOpenHome opens an already-initialized home for e2e fixtures.
func mustOpenHome(t *testing.T, homeDir string) *home.Home {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type e2eTeardown struct{}

func (e2eTeardown) RefuseGate() error { return nil }
func (e2eTeardown) Probe(string, map[string]string) (fleet.RetirementEndpointStatus, error) {
	return fleet.RetirementEndpointStatus{}, nil
}
func (e2eTeardown) Dispose(string, map[string]string, fleet.DisposeRequest) error { return nil }
func (e2eTeardown) ReturnWorktree(string, string) error                           { return nil }
func (e2eTeardown) QueryMergeStatus(*domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return nil, nil
}

func TestLocalOnlyScoutReportAllowsNormalTeardown(t *testing.T) {
	homeDir, taskID := t.TempDir(), "e2e-scout"
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "data", taskID), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fleet.ReportPath(homeDir, taskID, 1)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.ReportPath(homeDir, taskID, 1), []byte("# report\n"), 0644); err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(mustOpenHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:                 auth.HomeID(),
		TaskID:                 tid,
		Owner:                  "owner",
		Kind:                   "scout",
		ScoutScope:             "investigate the requested question",
		ScoutRuntimeBudgetSecs: 300,
		Reason:                 "create",
	}
	opID, err := domain.NewOperationID("op-create-" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}

	if _, err := orchestrator.DeliverWake(orchestrator.DeliverRequest{
		HomeDir: homeDir, ParentHome: homeDir, TaskID: taskID,
		State: "done", Message: "findings recorded", Key: "terminal", Role: "soldier",
	}); err != nil {
		t.Fatalf("local-only terminal report: %v", err)
	}
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: homeDir, ID: taskID}, e2eTeardown{}, orchestratorRetirementJournals{}, auth); err != nil {
		t.Fatalf("normal teardown after local-only scout report: %v", err)
	}
}

func TestRetirementTerminalUplinkContinuity(t *testing.T) {
	homeDir, taskID, key := t.TempDir(), "task-e2e", "terminal"
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, taskID, "done: complete"); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.InitTaskObligations(homeDir, taskID, key); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteReceipt(homeDir, taskID, key, "done", "complete"); err != nil {
		t.Fatal(err)
	}
	// The composed canonical Task Authority (Task 7.7) records the task and
	// carries the durable retirement receipt; the teardown flow commits the
	// Retire op before saga-side cleanup.
	auth, err := taskauthority.NewCanonical(mustOpenHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:                 auth.HomeID(),
		TaskID:                 tid,
		Owner:                  "owner",
		Kind:                   "scout",
		ScoutScope:             "investigate the requested question",
		ScoutRuntimeBudgetSecs: 300,
		Reason:                 "create",
	}
	opID, err := domain.NewOperationID("op-create-" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: homeDir, ID: taskID}, e2eTeardown{}, orchestratorRetirementJournals{}, auth); err == nil {
		t.Fatal("open report obligation must block retirement")
	}
	if err := orchestrator.WriteAck(homeDir, taskID, key); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.CompleteTaskObligation(homeDir, taskID, orchestrator.ReportRelay); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: homeDir, ID: taskID, Force: true}, e2eTeardown{}, orchestratorRetirementJournals{}, auth); err != nil {
		t.Fatal(err)
	}
	stem, err := home.DurableKey(taskID)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(homeDir, "state", ".backup", stem, stem+".status")
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("forced evidence missing: %v", err)
	}
	if _, err := orchestrator.PrepareForcedRetirementEvidence(homeDir, taskID); err != nil {
		t.Fatalf("evidence retry must be idempotent: %v", err)
	}
}
