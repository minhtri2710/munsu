//go:build integration

package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- DeliverWake tests ---

// deliverEnv sets up a temp soldier home and optional captain parent home for testing.
// Returns soldierHome, captainHome (empty string if no parent).
func deliverEnv(t *testing.T, withParent bool) (soldierHome, captainHome string) {
	t.Helper()
	soldierHome = t.TempDir()
	if withParent {
		captainHome = t.TempDir()
		if err := os.WriteFile(filepath.Join(captainHome, ProvenanceMarkerName), []byte("captain=test-captain\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return soldierHome, captainHome
}

// TestDeliverWake_SelfAcknowledgesRegardlessOfParent pins ADR-0015: the writer
// closes its own terminal handoff. A captain home used to be excluded here, on
// the premise that a relay sweep would ack it later; #417 deleted that sweep and
// left captain-backed soldiers wedged (BEO-112).
func TestDeliverWake_SelfAcknowledgesRegardlessOfParent(t *testing.T) {
	for _, tt := range []struct {
		name          string
		captainParent bool
	}{
		{"captain parent", true},
		{"local-only parent", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			soldierHome, parentHome := deliverEnv(t, true)
			if !tt.captainParent {
				os.Remove(filepath.Join(parentHome, ProvenanceMarkerName))
			}

			receipt, err := DeliverWake(DeliverRequest{
				HomeDir: soldierHome, ParentHome: parentHome, TaskID: "scout",
				State: "done", Message: "complete", Key: "terminal", Role: "soldier",
			})
			if err != nil {
				t.Fatalf("DeliverWake: %v", err)
			}
			if !receipt.ReceiptWritten || !receipt.ObligationsInit {
				t.Fatalf("receipt = %+v, want receipt and obligation setup", receipt)
			}
			if !IsReceiptAcked(parentHome, "scout", "terminal") {
				t.Fatal("terminal receipt was not self-acknowledged")
			}
			open, err := IsTaskReportRelayOpen(parentHome, "scout")
			if err != nil {
				t.Fatal(err)
			}
			if open {
				t.Fatal("report relay remained open")
			}
		})
	}
}

// TestDeliverWake_SoldierMaterialState verifies that a soldier doing a
// material-state (done) report produces:
//   - local task status
//   - local event log entry
//   - local wake queue entry
//   - captain receipt (if parentHome set)
//   - a closed report-relay obligation in the captain home: the writer closes
//     its own handoff (ADR-0015)
func TestDeliverWake_SoldierMaterialState(t *testing.T) {
	soldierHome, captainHome := deliverEnv(t, true)

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-soldier",
		State:      "done",
		Message:    "task complete",
		Key:        "test-key",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}
	if !receipt.EventAppended || !receipt.WakeEnqueued || receipt.EnqueueUnix == 0 {
		t.Fatalf("receipt = %+v, want event, wake, and enqueue timestamp recorded", receipt)
	}

	// Local task status should exist
	statusPath := filepath.Join(soldierHome, "state", "test-soldier.status")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("status file: %v", err)
	}
	if !strings.Contains(string(data), "done: task complete") {
		t.Errorf("status should contain 'done: task complete', got: %s", string(data))
	}
	if !strings.Contains(string(data), "[key=test-key]") {
		t.Errorf("status should contain key, got: %s", string(data))
	}

	// Local event log should exist
	events := readEventLog(t, soldierHome)
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	if events[0].Type != "task.status" {
		t.Errorf("expected event type 'task.status', got %q", events[0].Type)
	}
	if events[0].Key != "test-key" {
		t.Errorf("expected event key 'test-key', got %q", events[0].Key)
	}

	// Wake queue should have entries
	if !HasQueuedWakes(soldierHome) {
		t.Error("expected wake queue entries")
	}

	// Event ID should be set in receipt
	if receipt.EventID == 0 {
		t.Error("expected non-zero event ID")
	}

	// Captain receipt should exist
	receiptPath := ReceiptPath(captainHome, "test-soldier", "test-key")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("captain receipt should exist: %v", err)
	}

	// The obligation was initialized and then closed by the writer itself.
	if !receipt.ObligationsInit {
		t.Error("expected obligations to be initialized")
	}
	open, err := IsTaskReportRelayOpen(captainHome, "test-soldier")
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen: %v", err)
	}
	if open {
		t.Error("ReportRelay obligation remained open: nothing in the binary can close it later")
	}
}

// TestDeliverWake_SoldierNonMaterialState verifies that a non-material state
// (e.g., "working") produces no receipt, no obligations, and no wake but
// still writes status and event.
func TestDeliverWake_SoldierNonMaterialState(t *testing.T) {
	soldierHome, captainHome := deliverEnv(t, true)

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-worker",
		State:      "working",
		Message:    "in progress",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}

	// Local status should exist
	statusPath := filepath.Join(soldierHome, "state", "test-worker.status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("status file should exist: %v", err)
	}

	// Event should exist
	events := readEventLog(t, soldierHome)
	if len(events) == 0 {
		t.Fatal("expected event for non-material state")
	}

	// Wake queue should NOT exist for non-material state
	if HasQueuedWakes(soldierHome) {
		t.Error("expected NO wake queue for non-material state")
	}

	// Captain receipt should NOT exist
	receiptPath := ReceiptPath(captainHome, "test-worker", "default")
	if _, err := os.Stat(receiptPath); err == nil {
		t.Error("receipt should NOT exist for non-material state")
	}
}

// TestDeliverWake_SoldierNoParent skips parent setup.
func TestDeliverWake_SoldierNoParent(t *testing.T) {
	soldierHome, _ := deliverEnv(t, false)
	// No parentHome needed

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: "",
		TaskID:     "test-no-parent",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake without parent: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}

	// Local status + event + wake should exist
	if !HasQueuedWakes(soldierHome) {
		t.Error("expected wake queue even without parent")
	}
}

// TestDeliverWake_ReceiptWriteFails verifies fail-closed when receipt write
// fails (parentHome/state is a regular file blocking MkdirAll).
func TestDeliverWake_ReceiptWriteFails(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()

	// Make parentHome/state a regular file so MkdirAll fails
	stateFile := filepath.Join(captainHome, "state")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0644); err != nil {
		t.Fatalf("writing state file: %v", err)
	}

	_, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-fail-closed",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err == nil {
		t.Fatal("expected error when receipt write fails")
	}

	// No event log should exist
	eventPath := LogPath(soldierHome)
	if _, err := os.Stat(eventPath); err == nil {
		t.Error("event log should not exist after receipt failure")
	}

	// No wake queue
	if HasQueuedWakes(soldierHome) {
		t.Error("wake queue should not exist after receipt failure")
	}

	// Status should exist (it comes before receipt in pipeline)
	statusPath := filepath.Join(soldierHome, "state", "test-fail-closed.status")
	if _, err := os.Stat(statusPath); os.IsNotExist(err) {
		t.Error("status should exist even after receipt failure (written before)")
	}
}

// TestDeliverWake_ObligationsInitFails verifies fail-closed when
// InitTaskObligations fails (state/.obligations is a regular file).
func TestDeliverWake_ObligationsInitFails(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()

	// Create state/.terminal-receipts so WriteReceipt succeeds
	receiptsDir := filepath.Join(captainHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		t.Fatalf("mkdir receipts: %v", err)
	}

	// Make state/.obligations a regular file so InitTaskObligations fails
	obligFile := filepath.Join(captainHome, "state", ".obligations")
	if err := os.WriteFile(obligFile, []byte("not-a-dir"), 0644); err != nil {
		t.Fatalf("writing obligations file: %v", err)
	}

	_, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-obl-fail",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err == nil {
		t.Fatal("expected error when obligation init fails")
	}

	// Receipt should exist (WriteReceipt succeeded before obligation failure)
	receiptPath := ReceiptPath(captainHome, "test-obl-fail", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist even when obligations fail: %v", err)
	}

	// No ack should exist
	if IsReceiptAcked(captainHome, "test-obl-fail", "default") {
		t.Error("no ack should exist after obligations failure")
	}

	// No event log
	eventPath := LogPath(soldierHome)
	if _, err := os.Stat(eventPath); err == nil {
		t.Error("event log should not exist after obligations failure")
	}

	// No wake queue
	if HasQueuedWakes(soldierHome) {
		t.Error("wake queue should not exist after obligations failure")
	}
}

// TestDeliverWake_CaptainMaterialState verifies captain-level reports
// (which have parentHome set but use a different route — same as soldier
// but goes to parent instead of local for events).
func TestDeliverWake_CaptainMaterialState(t *testing.T) {
	captainHome := t.TempDir()
	generalHome := t.TempDir()

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    captainHome,
		ParentHome: generalHome,
		TaskID:     "captain-task",
		State:      "done",
		Message:    "captain report complete",
		Key:        "captain-key",
		Role:       "captain",
	})
	if err != nil {
		t.Fatalf("DeliverWake for captain: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}

	// Captain gets no receipt/obligation (that's soldier→captain only)
	// But should get local status, event log, wake queue
	statusPath := filepath.Join(captainHome, "state", "captain-task.status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("captain status should exist: %v", err)
	}

	if !HasQueuedWakes(captainHome) {
		t.Error("expected wake queue for captain material state")
	}
}

// TestDeliverWake_GeneralMaterialState verifies general-level reports.
func TestDeliverWake_GeneralMaterialState(t *testing.T) {
	generalHome := t.TempDir()

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir: generalHome,
		TaskID:  "general-task",
		State:   "done",
		Message: "general report",
		Role:    "general",
	})
	if err != nil {
		t.Fatalf("DeliverWake for general: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}

	if !HasQueuedWakes(generalHome) {
		t.Error("expected wake queue for general material state")
	}
}

// TestDeliverWake_EventWrittenWithWarningLevel verifies that event append
// errors are non-fatal (best-effort warning only).
func TestDeliverWake_EventAppendIsBestEffort(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()

	// Force the event append to fail deterministically: a directory where
	// the append-only event log file must be opened makes OpenFile fail,
	// while the status append, receipt, and wake queue remain writable.
	if err := os.MkdirAll(filepath.Join(soldierHome, "state", ".event-log"), 0755); err != nil {
		t.Fatalf("mkdir event-log-as-dir: %v", err)
	}

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-event-best-effort",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	if receipt.EventAppended {
		t.Fatalf("receipt = %+v, want EventAppended=false when event append fails", receipt)
	}
	// The report must not fail and the wake must still be enqueued so the
	// parent supervisor is woken; the explicit EventAppended=false flag is
	// what makes the gap recoverable.
	if !receipt.WakeEnqueued || receipt.EnqueueUnix == 0 {
		t.Fatalf("receipt = %+v, want wake still enqueued after event append failure", receipt)
	}
}

func TestDeliverWake_EnqueueFailureRecorded(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()

	// Force the wake enqueue to fail deterministically: a directory where
	// the append-only wake queue file must be opened makes OpenFile fail.
	if err := os.MkdirAll(filepath.Join(soldierHome, "state", ".wake-queue"), 0755); err != nil {
		t.Fatalf("mkdir wake-queue-as-dir: %v", err)
	}

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-enqueue-failure",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	// The failed enqueue must be explicit on the receipt (no timestamp, no
	// wake flag) rather than silently indistinguishable from a delivered wake.
	if receipt.WakeEnqueued || receipt.EnqueueUnix != 0 {
		t.Fatalf("receipt = %+v, want WakeEnqueued=false and EnqueueUnix=0 when enqueue fails", receipt)
	}
	if !receipt.EventAppended {
		t.Fatalf("receipt = %+v, want event still appended when enqueue fails", receipt)
	}
}

// TestDeliverWake_KeyDefault verifies that an empty key defaults to "default".
func TestDeliverWake_KeyDefault(t *testing.T) {
	soldierHome, captainHome := deliverEnv(t, true)

	receipt, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "test-default-key",
		State:      "done",
		Message:    "task complete",
		Role:       "soldier",
	})
	if err != nil {
		t.Fatalf("DeliverWake: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected non-nil receipt")
	}

	// Receipt should use "default" key
	receiptPath := ReceiptPath(captainHome, "test-default-key", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt with default key should exist: %v", err)
	}
}

// TestDeliverWake_InvalidState verifies error for invalid state.
func TestDeliverWake_InvalidState(t *testing.T) {
	_, err := DeliverWake(DeliverRequest{
		HomeDir: t.TempDir(),
		TaskID:  "test-invalid",
		State:   "invalid-state",
		Message: "bad",
		Role:    "soldier",
	})
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
}

// TestDeliverWake_EmptyParams verifies error for missing required params.
func TestDeliverWake_EmptyParams(t *testing.T) {
	tests := []struct {
		name string
		req  DeliverRequest
	}{
		{"no home", DeliverRequest{TaskID: "t", State: "done", Message: "m", Role: "soldier"}},
		{"no task", DeliverRequest{HomeDir: t.TempDir(), State: "done", Message: "m", Role: "soldier"}},
		{"no message", DeliverRequest{HomeDir: t.TempDir(), TaskID: "t", State: "done", Role: "soldier"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeliverWake(tt.req)
			if err == nil {
				t.Error("expected error for missing param")
			}
		})
	}
}

func initProvenance(t *testing.T, home, captainID string) {
	t.Helper()
	os.WriteFile(filepath.Join(home, ProvenanceMarkerName),
		[]byte("munsu-v2\n"+captainID+"\n"), 0644)
}

// --- ActivateOnReceipt tests ---

// receiptEnv creates a captain home with receipt infrastructure.
func receiptEnv(t *testing.T) string {
	t.Helper()
	captainHome := t.TempDir()
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	return captainHome
}

// TestActivationSeen_MarkerDirect tests the IsActivationSeen / MarkActivationSeen
// marker lifecycle directly.
func TestActivationSeen_MarkerDirect(t *testing.T) {
	captainHome := receiptEnv(t)
	taskID, termKey := "test-task", "test-key"

	// Initially not activation-seen.
	if IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("receipt should NOT be activation-seen before marking")
	}

	// Mark as seen.
	if err := MarkActivationSeen(captainHome, taskID, termKey); err != nil {
		t.Fatalf("MarkActivationSeen: %v", err)
	}

	// Now it should be seen.
	if !IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("receipt SHOULD be activation-seen after marking")
	}

	// Marker should be in the receipts directory.
	markerPath := ActivationSeenPath(captainHome, taskID, termKey)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if !strings.Contains(string(data), "task_id="+taskID) {
		t.Errorf("marker should contain task_id=%s, got: %s", taskID, string(data))
	}
	if !strings.Contains(string(data), "key="+termKey) {
		t.Errorf("marker should contain key=%s, got: %s", termKey, string(data))
	}
	if !strings.Contains(string(data), "activated_at=") {
		t.Error("marker should contain activated_at=")
	}
}

// parentHomeWithMeta creates a parent home with captain meta containing
// herdr_pane_id and writes a provenance marker to captainHome.
func parentHomeWithMeta(t *testing.T, captainHome, captainID, paneID, session string) string {
	t.Helper()
	parentHome := t.TempDir()
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)

	// Write provenance marker to captain home.
	initProvenance(t, captainHome, captainID)

	// Write captain task meta to parent home (mimics Launch).
	taskID := "captain:" + captainID
	metaContent := fmt.Sprintf("kind=captain\nherdr_pane_id=%s\nherdr_session=%s\nbackend=herdr\n", paneID, session)
	metaPath := filepath.Join(parentHome, "state", taskID+".meta")
	if err := os.WriteFile(metaPath, []byte(metaContent), 0644); err != nil {
		t.Fatalf("writing captain meta: %v", err)
	}

	return parentHome
}

// TestActivateOnReceipt_NoReceipts verifies that ActivateOnReceipt returns 0
// when there are no pending receipts.
func TestActivateOnReceipt_NoReceipts(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p1", "w1")

	// No receipt files at all.
	count := ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	if count != 0 {
		t.Errorf("expected 0 activations with no receipts, got %d", count)
	}
}

// TestActivateOnReceipt_AllAlreadySeen verifies that receipts already
// activation-seen do not trigger duplicate nudges.
func TestActivateOnReceipt_AllAlreadySeen(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p1", "w1")
	taskID, termKey := "already-seen", "seen-key"

	// Write receipt and mark it as activation-seen.
	if err := WriteReceipt(captainHome, taskID, termKey, "done", "complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	if err := MarkActivationSeen(captainHome, taskID, termKey); err != nil {
		t.Fatalf("MarkActivationSeen: %v", err)
	}

	count := ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	if count != 0 {
		t.Errorf("expected 0 activations for already-seen receipt, got %d", count)
	}

	// Marker should still exist.
	if !IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("activation-seen marker should persist after ActivateOnReceipt")
	}
}

// TestActivateOnReceipt_NoBackend verifies that when no session backend is
// available, ActivateOnReceipt gracefully returns 0 and does NOT mark the
// receipt as activation-seen (preserves retry).
func TestActivateOnReceipt_NoBackend(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p1", "w1")
	taskID, termKey := "no-backend", "no-bk-key"

	if err := WriteReceipt(captainHome, taskID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	count := ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	if count != 0 {
		t.Errorf("expected 0 activations with no backend, got %d", count)
	}

	// Receipt must NOT be activation-seen — retries must remain possible.
	if IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("receipt should NOT be activation-seen after failed activation attempt")
	}
}

// TestActivateOnReceipt_OnlyNewAreActivated verifies that when there are
// multiple receipts (some seen, some new), only new receipts are candidates
// for activation. Without a backend, no new marker is written.
func TestActivateOnReceipt_OnlyNewAreActivated(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p1", "w1")

	// Receipt 1: already seen.
	if err := WriteReceipt(captainHome, "seen-task", "seen-key", "done", ""); err != nil {
		t.Fatalf("WriteReceipt seen: %v", err)
	}
	if err := MarkActivationSeen(captainHome, "seen-task", "seen-key"); err != nil {
		t.Fatalf("MarkActivationSeen: %v", err)
	}

	// Receipt 2: new (not seen).
	if err := WriteReceipt(captainHome, "new-task", "new-key", "failed", "error"); err != nil {
		t.Fatalf("WriteReceipt new: %v", err)
	}

	count := ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	if count != 0 {
		t.Errorf("expected 0 activations (no backend), got %d", count)
	}

	// New receipt must NOT be activation-seen — no backend means no submission.
	if IsActivationSeen(captainHome, "new-task", "new-key") {
		t.Error("new receipt should NOT be activation-seen when no backend")
	}

	// Seen receipt must remain activation-seen.
	if !IsActivationSeen(captainHome, "seen-task", "seen-key") {
		t.Error("already-seen receipt must remain activation-seen")
	}
}

// TestActivateOnReceipt_NoMeta verifies that when no captain meta exists
// (no parent home or no herdr_pane_id), ActivateOnReceipt returns 0 and
// does NOT write activation-seen (retries remain possible).
func TestActivateOnReceipt_NoMeta(t *testing.T) {
	captainHome := receiptEnv(t)
	taskID, termKey := "no-meta-task", "no-meta-key"

	if err := WriteReceipt(captainHome, taskID, termKey, "done", "complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	// No parent home set, no captain meta exists.
	count := ActivateOnReceiptWithTransport(captainHome, "", nil)
	if count != 0 {
		t.Errorf("expected 0 activations with no meta, got %d", count)
	}

	// Receipt must NOT be marked activation-seen.
	if IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("receipt should NOT be activation-seen when no meta available")
	}
}

// TestActivateOnReceipt_EnvMismatchRegression verifies that when the
// process environment has a wrong HERDR_PANE_ID (inherited from watcher),
// but captain meta has the correct herdr_pane_id, the meta value is used.
func TestActivateOnReceipt_EnvMismatchRegression(t *testing.T) {
	captainHome := receiptEnv(t)
	// Captain meta has pane "p2" in session "w76".
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p2", "w76")
	taskID, termKey := "env-mismatch", "mm-key"

	// Set the inherited env to a DIFFERENT pane (simulating watcher inheritance).
	t.Setenv("HERDR_PANE_ID", "w1K:p1")
	t.Setenv("HERDR_ENV", "1")

	if err := WriteReceipt(captainHome, taskID, termKey, "done", ""); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	// Verify that resolveCaptainActivationTarget returns the META pane, not the env pane.
	target, err := resolveCaptainActivationTarget(captainHome, parentHome)
	if err != nil {
		t.Fatalf("resolveCaptainActivationTarget: %v", err)
	}
	if target.Handle != "w76:p2" {
		t.Errorf("expected target handle 'w76:p2' from meta, got %q", target.Handle)
	}
}

// TestActivateOnReceipt_MetaWithMissingSession verifies that when meta has
// herdr_pane_id but no herdr_session, the session defaults to "default".
func TestActivateOnReceipt_MetaWithMissingSession(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := t.TempDir()
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)
	initProvenance(t, captainHome, "test-captain")

	taskID := "captain:test-captain"
	metaContent := "kind=captain\nherdr_pane_id=p1\nbackend=herdr\n"
	metaPath := filepath.Join(parentHome, "state", taskID+".meta")
	os.WriteFile(metaPath, []byte(metaContent), 0644)

	target, err := resolveCaptainActivationTarget(captainHome, parentHome)
	if err != nil {
		t.Fatalf("resolveCaptainActivationTarget: %v", err)
	}
	if target.Handle != "default:p1" {
		t.Errorf("expected target handle 'default:p1', got %q", target.Handle)
	}
	if target.Session != "default" {
		t.Errorf("expected session 'default', got %q", target.Session)
	}
}

// TestActivateOnReceipt_EmptyParentHome verifies that an empty parentHome
// causes resolveCaptainActivationTarget to fail gracefully.
func TestActivateOnReceipt_EmptyParentHome(t *testing.T) {
	captainHome := receiptEnv(t)

	_, err := resolveCaptainActivationTarget(captainHome, "")
	if err == nil {
		t.Error("expected error with empty parentHome")
	}
}

// TestActivateOnReceipt_MissingCaptainMeta verifies behavior when the captain
// meta file does not exist in the parent home.
func TestActivateOnReceipt_MissingCaptainMeta(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := t.TempDir()
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)
	initProvenance(t, captainHome, "test-captain")

	_, err := resolveCaptainActivationTarget(captainHome, parentHome)
	if err == nil {
		t.Error("expected error when meta file does not exist")
	}
}
