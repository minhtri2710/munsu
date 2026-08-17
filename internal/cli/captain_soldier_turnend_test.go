package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// captainBackedSoldierReport runs the real terminal-report delivery a scout
// soldier performs on `done`, with a captain home as its parent, and returns
// the two homes. This is the exact shape produced by report_cmd.go's scout
// branch (DeliverWake with Role=soldier, ParentHome set).
func captainBackedSoldierReport(t *testing.T, taskID string) (soldierHome, captainHome string) {
	t.Helper()
	soldierHome = t.TempDir()
	captainHome = t.TempDir()

	// The provenance marker is what makes this a captain home.
	if err := os.WriteFile(filepath.Join(captainHome, orchestrator.ProvenanceMarkerName),
		[]byte("captain=test-captain\n"), 0644); err != nil {
		t.Fatal(err)
	}

	receipt, err := orchestrator.DeliverWake(orchestrator.DeliverRequest{
		HomeDir: soldierHome, ParentHome: captainHome, TaskID: taskID,
		State: "done", Message: "scout complete", Key: "terminal", Role: "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	if !receipt.ReceiptWritten || !receipt.ObligationsInit {
		t.Fatalf("receipt = %+v, want receipt written and obligations initialized", receipt)
	}

	// The captain owns the task record and marks it done in its own home once
	// the soldier reports — the line `munsu task done` writes (task_cmd.go:506).
	// Both gates key off this material line plus the receipt beside it.
	if err := home.AppendStatus(captainHome, taskID, "done: cli task done"); err != nil {
		t.Fatal(err)
	}
	return soldierHome, captainHome
}

// TestCaptainBackedSoldierCanEndItsTurn is the BEO-112 regression guard.
//
// A soldier whose parent is a captain home reported a material terminal state.
// Nothing in the binary can write the ack for the receipt that report left in
// the captain home (#417 deleted the relay's only call site), so the turn-end
// guard answered "continue" on every subsequent turn and the session could
// never end. The soldier must be able to end its turn.
func TestCaptainBackedSoldierCanEndItsTurn(t *testing.T) {
	soldierHome, captainHome := captainBackedSoldierReport(t, "scout-task")

	t.Setenv("MUNSU_HOME", soldierHome)
	t.Setenv("MUNSU_PARENT_STATUS", captainHome)

	oldExit := exitWithCode
	var exitCode int
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	stdout, _ := captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{"fullyIdle":true}`))
		w.Close()
		os.Stdin = r
		runGuardAgy(soldierHome)
		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdout)
	}
	if payload["decision"] != "allow" {
		t.Fatalf("decision = %v (reason=%v), want \"allow\": a captain-backed soldier's "+
			"own terminal receipt must not block its turn end",
			payload["decision"], payload["reason"])
	}
}

// TestPendingRelayGateRemedyIsActionable pins the guard's refusal text to a
// command that can actually clear the condition. `munsu turnend obligations`
// closes obligations by role and writes no receipt ack, so advertising it
// leaves an operator who follows the instruction exactly still stuck.
func TestPendingRelayGateRemedyIsActionable(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// A receipt with no ack: the crash window between WriteReceipt and WriteAck.
	receiptsDir := filepath.Join(homeDir, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptsDir, "crashed-task.terminal.receipt"),
		[]byte("state=done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(homeDir, "state")
	if err := os.WriteFile(filepath.Join(stateDir, "crashed-task.status"),
		[]byte("done: task complete\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := checkPendingRelayObligations(homeDir)
	if err == nil {
		t.Fatal("expected an un-acked receipt to block, got nil")
	}
	if strings.Contains(err.Error(), "turnend obligations") {
		t.Fatalf("refusal advertises 'munsu turnend obligations', which closes "+
			"obligations by role and writes no receipt ack: %v", err)
	}
	if !strings.Contains(err.Error(), "munsu report") {
		t.Fatalf("refusal must name a command that clears the condition, got: %v", err)
	}
}
