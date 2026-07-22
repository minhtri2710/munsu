package turnend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObligationsByRole(t *testing.T) {
	tests := []struct {
		role      Role
		want      int
		wantKinds []ObligationKind
	}{
		{
			role:      RoleGeneral,
			want:      1,
			wantKinds: []ObligationKind{Cleanup},
		},
		{
			role:      RoleCaptain,
			want:      2,
			wantKinds: []ObligationKind{ReportRelay, Cleanup},
		},
		{
			role:      RoleSoldier,
			want:      2,
			wantKinds: []ObligationKind{ReportRelay, Cleanup},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := Obligations(tt.role)
			if len(got) != tt.want {
				t.Errorf("Obligations(%q) = %d items, want %d", tt.role, len(got), tt.want)
			}

			// Verify all expected kinds are present
			for _, wantKind := range tt.wantKinds {
				found := false
				for _, o := range got {
					if o.Kind == wantKind {
						found = true
						if o.State != StateOpen {
							t.Errorf("Obligations(%q) kind=%q has state=%q, want %q",
								tt.role, o.Kind, o.State, StateOpen)
						}
						break
					}
				}
				if !found {
					t.Errorf("Obligations(%q): missing kind %q", tt.role, wantKind)
				}
			}

			// All must be open
			for _, o := range got {
				if o.State != StateOpen {
					t.Errorf("Obligations(%q) kind=%q state=%q, want %q", tt.role, o.Kind, o.State, StateOpen)
				}
			}
		})
	}
}

func TestObligationsUnknownRole(t *testing.T) {
	got := Obligations("unknown")
	if got != nil {
		t.Errorf("Obligations(unknown) = %v, want nil", got)
	}
}

func TestSaveAndLoadObligations(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Initially no persisted state — should return defaults
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("LoadObligations() = %d items, want 2", len(loaded))
	}

	// Complete one obligation
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation() error = %v", err)
	}
	if !found {
		t.Errorf("CompleteObligation(ReportRelay) = false, want true")
	}

	// Load again — should see one closed, one open
	loaded, err = LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("LoadObligations() = %d items, want 2 after complete", len(loaded))
	}

	var reportRelayFound, cleanupFound bool
	for _, o := range loaded {
		switch o.Kind {
		case ReportRelay:
			reportRelayFound = true
			if o.State != StateClosed {
				t.Errorf("ReportRelay state = %q, want %q", o.State, StateClosed)
			}
			if o.ClosedAt == 0 {
				t.Errorf("ReportRelay ClosedAt = 0, want non-zero")
			}
		case Cleanup:
			cleanupFound = true
			if o.State != StateOpen {
				t.Errorf("Cleanup state = %q, want %q", o.State, StateOpen)
			}
		}
	}
	if !reportRelayFound || !cleanupFound {
		t.Errorf("missing obligations: reportRelay=%v cleanup=%v", reportRelayFound, cleanupFound)
	}
}

func TestCompleteNotFound(t *testing.T) {
	home := t.TempDir()
	role := RoleGeneral

	// General has only Cleanup, completing ReportRelay should return false
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation() error = %v", err)
	}
	if found {
		t.Errorf("CompleteObligation(ReportRelay) for General = true, want false")
	}
}

func TestCompleteIdempotent(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Complete Cleanup twice — second call should find it already closed
	found, err := CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("first CompleteObligation() error = %v", err)
	}
	if !found {
		t.Errorf("first CompleteObligation(Cleanup) = false, want true")
	}

	found, err = CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("second CompleteObligation() error = %v", err)
	}
	if found {
		t.Errorf("second CompleteObligation(Cleanup) = true, want false (already closed)")
	}
}

func TestClearCompleted(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Complete all obligations
	CompleteObligation(home, role, ReportRelay)
	CompleteObligation(home, role, Cleanup)

	// Clear completed
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() error = %v", err)
	}

	// Load — should have no open obligations
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("ClearCompleted: got %d obligations, want 0", len(loaded))
	}
}

func TestClearCompletedIdempotent(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Clear completed on fresh home — no-op
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() on fresh home error = %v", err)
	}

	// Load should still return defaults
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("after ClearCompleted on fresh home: got %d, want 2", len(loaded))
	}
}

func TestClearAll(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Save some obligations
	obligations := Obligations(role)
	if err := SaveObligations(home, role, obligations); err != nil {
		t.Fatalf("SaveObligations() error = %v", err)
	}

	// Verify file exists
	p := filepath.Join(home, obligationsDir, string(role)+".obligations")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("obligations file should exist: %v", err)
	}

	// Clear all
	if err := ClearAll(home, role); err != nil {
		t.Fatalf("ClearAll() error = %v", err)
	}

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("obligations file should be removed, but stat err = %v", err)
	}

	// ClearAll on already-cleared home is a no-op
	if err := ClearAll(home, role); err != nil {
		t.Errorf("ClearAll() on cleared home error = %v", err)
	}
}

func TestMaterialStates(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"done", true},
		{"failed", true},
		{"needs-decision", true},
		{"blocked", true},
		{"working", false},
		{"paused", false},
		{"resolved", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		got := MaterialStates(tt.state)
		if got != tt.want {
			t.Errorf("MaterialStates(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestLoadFromEmptyFile(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Create an empty obligations file
	p := filepath.Join(home, obligationsDir, string(role)+".obligations")
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte{}, 0644); err != nil {
		t.Fatalf("writing empty file: %v", err)
	}

	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("empty file: got %d obligations, want 0", len(loaded))
	}
}

func TestSoldierObligations(t *testing.T) {
	home := t.TempDir()
	role := RoleSoldier

	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("soldier: got %d obligations, want 2", len(loaded))
	}

	// Complete both
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation(ReportRelay) error = %v", err)
	}
	if !found {
		t.Errorf("soldier CompleteObligation(ReportRelay) = false, want true")
	}

	found, err = CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("CompleteObligation(Cleanup) error = %v", err)
	}
	if !found {
		t.Errorf("soldier CompleteObligation(Cleanup) = false, want true")
	}

	// Clear completed
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() error = %v", err)
	}

	loaded, err = LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("soldier after clear: got %d obligations, want 0", len(loaded))
	}
}

func TestMaterialReportExists(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	taskID := "test-task"

	// No status file → no material report, no error
	has, err := MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error for missing status: %v", err)
	}
	if has {
		t.Errorf("expected false for missing status file")
	}

	// Write a non-material status line
	statusPath := filepath.Join(stateDir, taskID+".status")
	os.WriteFile(statusPath, []byte("working: in progress\n"), 0644)
	has, err = MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Errorf("expected false for non-material status")
	}

	// Write a material status line (done)
	os.WriteFile(statusPath, []byte("working: in progress\ndone: task complete\n"), 0644)
	has, err = MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Errorf("expected true for material done status")
	}

	// Test with failed
	os.WriteFile(statusPath, []byte("failed: something broke\n"), 0644)
	has, err = MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Errorf("expected true for material failed status")
	}

	// Test with needs-decision (keyed)
	os.WriteFile(statusPath, []byte("needs-decision [key=approach]: pick approach\n"), 0644)
	has, err = MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Errorf("expected true for material needs-decision status")
	}

	// Test with blocked
	os.WriteFile(statusPath, []byte("blocked: waiting for review\n"), 0644)
	has, err = MaterialReportExists(home, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Errorf("expected true for material blocked status")
	}
}

func TestLineVerb(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"done: task complete", "done"},
		{"failed: something broke", "failed"},
		{"working [key=phase1]: Phase 1", "working"},
		{"needs-decision [key=approach]: Pick approach", "needs-decision"},
		{"blocked: waiting", "blocked"},
		{"resolved [key=approach]: Chose React", "resolved"},
		{"paused: waiting", "paused"},
	}
	for _, tt := range tests {
		got := lineVerb(tt.line)
		if got != tt.want {
			t.Errorf("lineVerb(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// TestWriteReceipt_StaleAckInvalidation verifies that WriteReceipt removes
// any prior ack for the same taskID+termKey, making the new receipt pending.
func TestWriteReceipt_StaleAckInvalidation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-task"
	termKey := "uplink"

	// Write initial receipt and ack (simulating completed relay)
	if err := WriteReceipt(homeDir, taskID, termKey, "done", "first"); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	if err := WriteAck(homeDir, taskID, termKey); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	if !IsReceiptAcked(homeDir, taskID, termKey) {
		t.Fatal("receipt should be acked after WriteAck")
	}

	// Rewrite receipt via WriteReceipt — should invalidate the stale ack
	if err := WriteReceipt(homeDir, taskID, termKey, "done", "second"); err != nil {
		t.Fatalf("second receipt: %v", err)
	}

	// Ack should no longer exist (removed by WriteReceipt)
	if IsReceiptAcked(homeDir, taskID, termKey) {
		t.Error("receipt should NOT be acked after WriteReceipt invalidated stale ack")
	}

	// But old receipt should be overwritten with new content
	data, err := os.ReadFile(ReceiptPath(homeDir, taskID, termKey))
	if err != nil {
		t.Fatalf("reading receipt: %v", err)
	}
	if !strings.Contains(string(data), "second") {
		t.Errorf("expected new receipt content, got: %s", string(data))
	}
	if strings.Contains(string(data), "first") {
		t.Errorf("receipt should not contain old content, got: %s", string(data))
	}

	// No temp file should remain
	tmpPath := ReceiptPath(homeDir, taskID, termKey) + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temporary file should be cleaned up after successful WriteReceipt")
	}
}

// TestReadCaptainID_FromMarker verifies that readCaptainID parses the
// .munsu-captain-home provenance marker correctly.
func TestReadCaptainID_FromMarker(t *testing.T) {
	tmp := t.TempDir()
	t.Run("reads captain ID from marker", func(t *testing.T) {
		markerPath := filepath.Join(tmp, ProvenanceMarkerName)
		os.MkdirAll(filepath.Dir(markerPath), 0755)
		os.WriteFile(markerPath, []byte("munsu-v2\nmy-captain\n/home/user/.munsu/captains/my-captain\n"), 0644)

		id, err := readCaptainID(tmp)
		if err != nil {
			t.Fatalf("readCaptainID: %v", err)
		}
		if id != "my-captain" {
			t.Errorf("captain id = %q, want %q", id, "my-captain")
		}
	})

	t.Run("falls back to basename when no marker", func(t *testing.T) {
		empty := t.TempDir()
		id, err := readCaptainID(empty)
		if err != nil {
			t.Fatalf("readCaptainID without marker: %v", err)
		}
		basename := filepath.Base(empty)
		if id != basename {
			t.Errorf("captain id = %q, want basename %q", id, basename)
		}
	})

	t.Run("falls back to basename when marker is malformed", func(t *testing.T) {
		bad := t.TempDir()
		markerPath := filepath.Join(bad, ProvenanceMarkerName)
		os.WriteFile(markerPath, []byte("munsu-v2\n\n"), 0644)

		id, err := readCaptainID(bad)
		if err != nil {
			t.Fatalf("readCaptainID with empty id: %v", err)
		}
		basename := filepath.Base(bad)
		if id != basename {
			t.Errorf("captain id = %q, want basename %q", id, basename)
		}
	})
}

// TestRelayPendingReceipts_NoPending verifies idempotent no-op on empty receipts.
func TestRelayPendingReceipts_NoPending(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()

	relayed, err := RelayPendingReceipts(captainHome, parentHome)
	if err != nil {
		t.Fatalf("RelayPendingReceipts with no receipts: %v", err)
	}
	if relayed != 0 {
		t.Errorf("expected 0 relayed, got %d", relayed)
	}
}

// TestRelayPendingReceipts_RelaysToParent verifies that pending receipts
// are relayed to the parent home, ack is written, and obligation is closed.
func TestRelayPendingReceipts_RelaysToParent(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	taskID := "test-soldier"
	termKey := "uplink"

	// Create captain provenance marker
	captainID := "test-captain"
	markerPath := filepath.Join(captainHome, ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(markerPath), 0755)
	os.WriteFile(markerPath, []byte("munsu-v2\n"+captainID+"\n"+captainHome+"\n"), 0644)

	// Create parent state dir and captain home state dir
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)

	// Write receipt and init obligation (simulating soldier done)
	if err := WriteReceipt(captainHome, taskID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	if err := InitTaskObligations(captainHome, taskID, termKey); err != nil {
		t.Fatalf("InitTaskObligations: %v", err)
	}

	// Verify receipt is NOT acked before relay
	if IsReceiptAcked(captainHome, taskID, termKey) {
		t.Fatal("receipt should NOT be acked before relay")
	}

	// Relay
	relayed, err := RelayPendingReceipts(captainHome, parentHome)
	if err != nil {
		t.Fatalf("RelayPendingReceipts: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 relayed receipt, got %d", relayed)
	}

	// Verify ack exists
	if !IsReceiptAcked(captainHome, taskID, termKey) {
		t.Fatal("receipt should be acked after relay")
	}

	// Verify parent received the relay status
	relayStatusPath := filepath.Join(parentHome, "state", "captain:"+captainID+".relay-"+taskID+".status")
	data, err := os.ReadFile(relayStatusPath)
	if err != nil {
		t.Fatalf("parent relay status should exist: %v", err)
	}
	if !strings.Contains(string(data), "done") {
		t.Errorf("relay status should contain 'done', got: %s", string(data))
	}

	// Verify obligation is closed
	open, err := IsTaskReportRelayOpen(captainHome, taskID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen: %v", err)
	}
	if open {
		t.Fatal("ReportRelay should be closed after relay")
	}
}

// TestRelayPendingReceipts_Idempotent verifies relay is idempotent.
func TestRelayPendingReceipts_Idempotent(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	taskID := "test-soldier"
	termKey := "uplink"

	captainID := "test-captain"
	markerPath := filepath.Join(captainHome, ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(markerPath), 0755)
	os.WriteFile(markerPath, []byte("munsu-v2\n"+captainID+"\n"+captainHome+"\n"), 0644)
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)

	// Write receipt, init obligations, relay
	WriteReceipt(captainHome, taskID, termKey, "done", "task complete")
	InitTaskObligations(captainHome, taskID, termKey)

	relayed, err := RelayPendingReceipts(captainHome, parentHome)
	if err != nil {
		t.Fatalf("first relay: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 first relay, got %d", relayed)
	}

	// Second relay should be idempotent (receipt no longer pending)
	relayed, err = RelayPendingReceipts(captainHome, parentHome)
	if err != nil {
		t.Fatalf("second relay: %v", err)
	}
	if relayed != 0 {
		t.Errorf("second relay should relay 0, got %d", relayed)
	}
}

// TestRelayPendingReceipts_InvalidParent fails gracefully.
func TestRelayPendingReceipts_InvalidParent(t *testing.T) {
	captainHome := t.TempDir()
	taskID := "test-soldier"
	termKey := "uplink"

	captainID := "test-captain"
	markerPath := filepath.Join(captainHome, ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(markerPath), 0755)
	os.WriteFile(markerPath, []byte("munsu-v2\n"+captainID+"\n"+captainHome+"\n"), 0644)
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)

	// Write receipt and init obligations
	WriteReceipt(captainHome, taskID, termKey, "done", "task complete")
	InitTaskObligations(captainHome, taskID, termKey)

	// Relay with non-existent parent home (no state dir).
	// RelayPendingReceipts should NOT fail fatally — AppendStatus creates the path.
	nonexistent := filepath.Join(captainHome, "nonexistent")
	relayed, err := RelayPendingReceipts(captainHome, nonexistent)
	if err != nil {
		t.Fatalf("RelayPendingReceipts to nonexistent parent: %v", err)
	}
	if relayed != 1 {
		t.Errorf("expected 1 relay even to invalid parent (AppendStatus creates path), got %d", relayed)
	}

	// Receipt should still be acked after relay attempt
	if !IsReceiptAcked(captainHome, taskID, termKey) {
		t.Error("receipt should be acked after relay (relay succeeds regardless of parent validity)")
	}
}
