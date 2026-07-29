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
)

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

func TestRetirementTerminalUplinkContinuity(t *testing.T) {
	homeDir, taskID, key := t.TempDir(), "task-e2e", "terminal"
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
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: homeDir, ID: taskID}, e2eTeardown{}, orchestratorRetirementJournals{}); err == nil {
		t.Fatal("open report obligation must block retirement")
	}
	if err := orchestrator.WriteAck(homeDir, taskID, key); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.CompleteTaskObligation(homeDir, taskID, orchestrator.ReportRelay); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: homeDir, ID: taskID, Force: true}, e2eTeardown{}, orchestratorRetirementJournals{}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(homeDir, "state", ".backup", taskID, taskID+".status")
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("forced evidence missing: %v", err)
	}
	if _, err := orchestrator.PrepareForcedRetirementEvidence(homeDir, taskID); err != nil {
		t.Fatalf("evidence retry must be idempotent: %v", err)
	}
}
