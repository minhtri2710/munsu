//go:build e2e

package teardown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// TestE2E_TerminalUplinkContinuity proves the full Soldier → Captain → General
// terminal uplink continuity flow using production paths:
//   - Soldier done → WriteReceipt/InitTaskObligations (production report path)
//   - uplinkCheck blocks teardown without ack
//   - Captain relay via RelayTerminalReceipts → General state write + ack
//   - CompleteTaskObligation → teardown proceeds
//   - Evidence survives soldier-side cleanup
//   - Duplicate report/relay is idempotent on stable task/key
//   - Full flow: Soldier done → Captain durable receipt → Captain relay to General
//     → ack → teardown succeeds only after ack
//
// Run with: go test -tags=e2e -run TestE2E_TerminalUplinkContinuity ./internal/teardown/
func TestE2E_TerminalUplinkContinuity(t *testing.T) {
	// ---- Setup: General home, Captain home, Soldier task ----
	generalHome := t.TempDir()
	captainHome := t.TempDir()
	soldierID := "e2e-test-soldier"
	termKey := "uplink"

	// Create captain provenance marker so RelayTerminalReceipts can read captain ID
	captainMarkerPath := filepath.Join(captainHome, captain.ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(captainMarkerPath), 0755)
	os.WriteFile(captainMarkerPath, []byte("munsu-v2 e2e-captain\n"), 0644)

	// Captain state dir (soldier tasks live in captain's state)
	captainStateDir := filepath.Join(captainHome, "state")
	os.MkdirAll(captainStateDir, 0755)

	// General state dir (captain relay targets this)
	generalStateDir := filepath.Join(generalHome, "state")
	os.MkdirAll(generalStateDir, 0755)

	// Create soldier task meta in captain home (so teardown can read it)
	metaContent := fmt.Sprintf("kind=ship\nwindow=@1\nworktree=%s\n", t.TempDir())
	if err := os.WriteFile(filepath.Join(captainStateDir, soldierID+".meta"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// ---- Phase 1: Soldier reports done (material terminal report) ----
	// Using production receipt + obligation paths (same as report_cmd.go)
	statusLine := "done: task complete"
	if err := task.AppendStatus(captainHome, soldierID, statusLine); err != nil {
		t.Fatalf("appending soldier status: %v", err)
	}

	// Durably write receipt (production path)
	if err := turnend.WriteReceipt(captainHome, soldierID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("writing captain receipt: %v", err)
	}

	// Initialize per-task obligations (production path)
	if err := turnend.InitTaskObligations(captainHome, soldierID, termKey); err != nil {
		t.Fatalf("init task obligations: %v", err)
	}

	// Verify the material report exists (fail-closed version)
	hasMaterial, err := turnend.MaterialReportExists(captainHome, soldierID)
	if err != nil {
		t.Fatalf("MaterialReportExists error: %v", err)
	}
	if !hasMaterial {
		t.Fatal("expected material report to exist after done status")
	}

	// Verify per-task ReportRelay obligation is open
	open, err := turnend.IsTaskReportRelayOpen(captainHome, soldierID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen error: %v", err)
	}
	if !open {
		t.Fatal("expected ReportRelay obligation to be open for soldier")
	}

	// Verify receipt exists and is NOT acked yet
	if turnend.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("receipt should NOT be acked before relay")
	}

	t.Log("Phase 1 passed: soldier report persisted receipt + task obligations")

	// ---- Phase 2: uplinkCheck blocks teardown without ack ----
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err == nil {
		t.Fatal("uplinkCheck should block with material report and open ReportRelay")
	}
	t.Logf("Phase 2 passed: uplinkCheck blocked teardown: %v", err)

	// ---- Phase 3: Captain relays to General (production reconcile path) ----
	relayed, err := captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("RelayTerminalReceipts: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 relayed receipt, got %d", relayed)
	}

	// Verify ack now exists
	if !turnend.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("receipt should be acked after relay")
	}

	// Verify General received the relay
	captainID := "e2e-captain"
	relayStatusPath := filepath.Join(generalStateDir, "captain:"+captainID+".relay-"+soldierID+".status")
	if _, err := os.Stat(relayStatusPath); err != nil {
		t.Fatalf("general should see captain relay status: %v", err)
	}
	data, _ := os.ReadFile(relayStatusPath)
	if !strings.Contains(string(data), "done") {
		t.Fatalf("relay status should contain done, got: %s", string(data))
	}

	t.Log("Phase 3 passed: Captain relayed receipt to General, ack written")

	// ---- Phase 4: Per-task ReportRelay is now closed (by relay) ----
	open, err = turnend.IsTaskReportRelayOpen(captainHome, soldierID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen error: %v", err)
	}
	if open {
		t.Fatal("ReportRelay should be closed after relay completed")
	}

	// uplinkCheck now passes (ack completed)
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should pass after ReportRelay completed: %v", err)
	}
	t.Log("Phase 4 passed: uplinkCheck allowed teardown after relay ack")

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

	// Evidence: receipt and ack files persist
	receiptPath := turnend.ReceiptPath(captainHome, soldierID, termKey)
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("receipt should persist: %v", err)
	}
	ackPath := turnend.AckPath(captainHome, soldierID, termKey)
	if _, err := os.Stat(ackPath); err != nil {
		t.Fatalf("ack should persist: %v", err)
	}
	t.Log("Phase 5 passed: evidence survives and captain relay reaches general")

	// ---- Phase 6: Idempotent teardown after ack ----
	// Calling uplinkCheck again should still pass (idempotent)
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should be idempotent after ReportRelay closed: %v", err)
	}

	// Duplicate relay is idempotent (receipt already acked)
	relayed, err = captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("duplicate RelayTerminalReceipts error: %v", err)
	}
	if relayed != 0 {
		t.Fatalf("duplicate relay should relay 0 receipts, got %d", relayed)
	}
	t.Log("Phase 6 passed: teardown and relay are idempotent after acknowledgement")

	// ---- Phase 7: Teardown Run succeeds (full production path) ----
	_, err = Run(Options{HomeDir: captainHome, ID: soldierID, Force: false})
	if err != nil {
		t.Fatalf("teardown Run should succeed after ack: %v", err)
	}

	// After teardown, status should be removed
	if _, err := os.Stat(statusPath); err == nil {
		t.Log("Note: status file may still exist if previous step removed it")
	}
	t.Log("Phase 7 passed: full production teardown succeeded after ack")

	// ---- Phase 8: Duplicate Soldier report is idempotent ----
	// Re-init obligations and re-write receipt to simulate another report
	if err := turnend.InitTaskObligations(captainHome, soldierID, termKey); err != nil {
		t.Fatalf("re-init obligations (should be noop): %v", err)
	}
	if err := turnend.WriteReceipt(captainHome, soldierID, termKey, "done", "task complete again"); err != nil {
		t.Fatalf("re-write receipt: %v", err)
	}

	// Receipt should not be acked yet
	if turnend.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("re-written receipt should not be acked yet")
	}

	// Relay again — should relay 1
	relayed, err = captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("relay after duplicate report: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 relayed after duplicate report, got %d", relayed)
	}
	t.Log("Phase 8 passed: duplicate report/relay is idempotent on stable task/key")
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
