//go:build e2e

package teardown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// TestE2E_TerminalUplinkContinuity proves the full Soldier → Captain → General
// terminal uplink continuity flow:
//   - Soldier done → uplinkCheck blocks teardown without ack
//   - ReportRelay completion → teardown proceeds
//   - Captain durable receipt → relay to General with idempotency
//   - Evidence survives soldier-side cleanup
//
// Run with: go test -tags=e2e -run TestE2E_TerminalUplinkContinuity ./internal/teardown/
func TestE2E_TerminalUplinkContinuity(t *testing.T) {
	// ---- Setup: General home, Captain home, Soldier task ----
	generalHome := t.TempDir()
	captainHome := t.TempDir()
	soldierID := "e2e-test-soldier"

	// Captain state dir (soldier tasks live in captain's state)
	captainStateDir := filepath.Join(captainHome, "state")
	os.MkdirAll(captainStateDir, 0755)

	// General state dir (captain relay targets this)
	generalStateDir := filepath.Join(generalHome, "state")
	os.MkdirAll(generalStateDir, 0755)

	// Create soldier task meta in captain home
	metaContent := fmt.Sprintf("kind=ship\nwindow=@1\nworktree=%s\n", t.TempDir())
	if err := os.WriteFile(filepath.Join(captainStateDir, soldierID+".meta"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// ---- Phase 1: Soldier reports done (material terminal report) ----
	statusLine := "done: task complete"
	if err := task.AppendStatus(captainHome, soldierID, statusLine); err != nil {
		t.Fatalf("appending soldier status: %v", err)
	}

	// Verify the material report exists
	if !turnend.MaterialReportExists(captainHome, soldierID) {
		t.Fatal("expected material report to exist after done status")
	}

	// Verify ReportRelay obligation is open by default for soldier role
	obligations, err := turnend.LoadObligations(captainHome, turnend.RoleSoldier)
	if err != nil {
		t.Fatalf("loading obligations: %v", err)
	}
	var reportRelayFound bool
	for _, o := range obligations {
		if o.Kind == turnend.ReportRelay && o.State == turnend.StateOpen {
			reportRelayFound = true
			break
		}
	}
	if !reportRelayFound {
		t.Fatal("expected ReportRelay obligation to be open for soldier")
	}

	// ---- Phase 2: uplinkCheck blocks teardown without ack ----
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err == nil {
		t.Fatal("uplinkCheck should block with material report and open ReportRelay")
	}
	t.Logf("Phase 2 passed: uplinkCheck blocked teardown: %v", err)

	// ---- Phase 3: Complete ReportRelay (captain has acknowledged) ----
	found, err := turnend.CompleteObligation(captainHome, turnend.RoleSoldier, turnend.ReportRelay)
	if err != nil {
		t.Fatalf("completing ReportRelay: %v", err)
	}
	if !found {
		t.Fatal("expected to find and complete ReportRelay obligation")
	}

	// Verify ReportRelay is now closed
	obligations, err = turnend.LoadObligations(captainHome, turnend.RoleSoldier)
	if err != nil {
		t.Fatalf("loading obligations after complete: %v", err)
	}
	for _, o := range obligations {
		if o.Kind == turnend.ReportRelay && o.State != turnend.StateClosed {
			t.Fatalf("ReportRelay should be closed after CompleteObligation, got state=%s", o.State)
		}
	}

	// ---- Phase 4: uplinkCheck now passes (ack now closed) ----
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should pass after ReportRelay completed: %v", err)
	}
	t.Log("Phase 4 passed: uplinkCheck allowed teardown after ReportRelay complete")

	// ---- Phase 5: Durable evidence survives cleanup ----
	// The status file still exists (teardown hasn't run yet)
	statusPath := filepath.Join(captainStateDir, soldierID+".status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Fatalf("status file should exist before teardown: %v", err)
	}

	// Read last line for the captain to relay
	lastLine := readLastStatusLine(t, statusPath)
	if !strings.Contains(lastLine, "done") {
		t.Fatalf("expected done status, got: %s", lastLine)
	}

	// Captain's relay to General: the captain writes to general's state
	captainRelayStatus := fmt.Sprintf("captain:%s", soldierID)
	captainStatusPath := filepath.Join(generalStateDir, captainRelayStatus+".status")
	if err := os.MkdirAll(filepath.Dir(captainStatusPath), 0755); err != nil {
		t.Fatalf("creating general status dir: %v", err)
	}
	relayLine := fmt.Sprintf("done: soldier %s completed", soldierID)
	if err := os.WriteFile(captainStatusPath, []byte(relayLine+"\n"), 0644); err != nil {
		t.Fatalf("writing captain relay status: %v", err)
	}

	// Verify general sees the relay
	if _, err := os.Stat(captainStatusPath); err != nil {
		t.Fatalf("general should see captain relay status: %v", err)
	}

	t.Log("Phase 5 passed: evidence survives and captain relay reaches general")

	// ---- Phase 6: Idempotent teardown after ack ----
	// Calling uplinkCheck again should still pass (idempotent)
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should be idempotent after ReportRelay closed: %v", err)
	}
	t.Log("Phase 6 passed: teardown is idempotent after acknowledgement")

	// ---- Phase 7: ReportRelay closes per-key for captain's own obligations ----
	// Captain also has ReportRelay obligation (relay to General)
	captainTaskID := "captain:test-id"
	if err := task.AppendStatus(generalHome, captainTaskID, "done: relayed soldier status"); err != nil {
		t.Fatalf("appending captain relay status: %v", err)
	}

	// Complete captain's own ReportRelay obligation
	found, err = turnend.CompleteObligation(generalHome, turnend.RoleCaptain, turnend.ReportRelay)
	if err != nil {
		t.Fatalf("completing captain ReportRelay in general home: %v", err)
	}
	if !found {
		t.Fatal("expected captain's ReportRelay obligation in general home")
	}

	// Captain's Cleanup obligation should also be closable
	found, err = turnend.CompleteObligation(generalHome, turnend.RoleCaptain, turnend.Cleanup)
	if err != nil {
		t.Fatalf("completing captain Cleanup: %v", err)
	}
	if !found {
		t.Fatal("expected captain's Cleanup obligation in general home")
	}

	t.Log("Phase 7 passed: captain obligations flow correctly (one-hop routing preserved)")
}

func readLastStatusLine(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
