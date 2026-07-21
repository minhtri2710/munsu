package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReportCmd_FailClosedOnReceiptWrite verifies that when WriteReceipt fails
// (unwritable parent receipt path), the report command returns an error instead
// of logging a warning and continuing. This ensures the fail-closed contract:
// soldier material reports must persist receipt AND obligations BEFORE
// wake/event/injection proceed.
func TestReportCmd_FailClosedOnReceiptWrite(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Set up env for soldier report
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-soldier")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	// Make parentHome read-only so WriteReceipt fails on MkdirAll
	if err := os.Chmod(parentHome, 0444); err != nil {
		t.Fatalf("chmod parentHome: %v", err)
	}
	defer os.Chmod(parentHome, 0755) // restore for test cleanup

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "done", "task complete"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error due to unwritable parent receipt path, got nil")
	}
	if !strings.Contains(err.Error(), "writing captain receipt") {
		t.Errorf("expected error mentioning 'writing captain receipt', got: %v", err)
	}
}

// TestReportCmd_FailClosedOnObligationsInit verifies that when InitTaskObligations
// fails (unwritable parent obligations path), the report command returns an error.
func TestReportCmd_FailClosedOnObligationsInit(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Create the receipt dir so WriteReceipt succeeds
	receiptsDir := filepath.Join(parentHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		t.Fatalf("mkdir receipts: %v", err)
	}

	// Create the obligations dir then make it read-only so WriteFile fails
	obligationsDir := filepath.Join(parentHome, "state", ".obligations")
	if err := os.MkdirAll(obligationsDir, 0755); err != nil {
		t.Fatalf("mkdir obligations: %v", err)
	}
	if err := os.Chmod(obligationsDir, 0444); err != nil {
		t.Fatalf("chmod obligationsDir: %v", err)
	}
	// Ensure state/ is traversable (needs execute for writeObligationsFile path traversal)
	if err := os.Chmod(filepath.Join(parentHome, "state"), 0755); err != nil {
		t.Fatalf("chmod state: %v", err)
	}

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-soldier")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "done", "task complete"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error due to unwritable parent obligations path, got nil")
	}
	if !strings.Contains(err.Error(), "init task obligations") {
		t.Errorf("expected error mentioning 'init task obligations', got: %v", err)
	}
	os.Chmod(obligationsDir, 0755) // restore for cleanup
}

// TestReportCmd_FailClosed_NormalPath verifies that the report command succeeds
// on the happy path (receipt and obligations are writable).
func TestReportCmd_FailClosed_NormalPath(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-soldier")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success on writable paths, got: %v", err)
	}
}
