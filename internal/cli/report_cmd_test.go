package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// TestReportCmd_FailClosedOnReceiptWrite verifies that when WriteReceipt fails
// (parentHome/state is a regular file, not a directory), the report command
// returns a typed error and no event/wake/ack is produced.
// Deterministic: regular file collision instead of permission trickery.
func TestReportCmd_FailClosedOnReceiptWrite(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Create parentHome/state as a regular file so os.MkdirAll for
	// parentHome/state/.terminal-receipts fails on NotADirectory.
	stateFile := filepath.Join(parentHome, "state")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0644); err != nil {
		t.Fatalf("writing state file: %v", err)
	}

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-soldier")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "task complete"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error due to file collision at parentHome/state, got nil")
	}
	if !strings.Contains(err.Error(), "writing captain receipt") {
		t.Errorf("expected error mentioning 'writing captain receipt', got: %v", err)
	}

	// Assert no event was appended
	eventDir := filepath.Join(homeDir, "events")
	if entries, _ := os.ReadDir(eventDir); len(entries) > 0 {
		t.Errorf("no events should exist after receipt failure, found %d", len(entries))
	}

	// Assert no wake was enqueued
	wakePath := lifecycle.QueuePath(homeDir)
	if _, err := os.Stat(wakePath); err == nil {
		t.Error("wake queue should not exist after receipt failure")
	}

	// Assert no ack exists for the task/key
	if turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		t.Error("no ack should exist after receipt failure")
	}
}

// TestReportCmd_FailClosedOnObligationsInit verifies that when
// InitTaskObligations fails (parentHome/state/.obligations is a regular file),
// the report command returns a typed error and no event/wake/ack is produced.
// Deterministic: regular file collision instead of permission trickery.
func TestReportCmd_FailClosedOnObligationsInit(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Create state/.terminal-receipts so WriteReceipt succeeds
	receiptsDir := filepath.Join(parentHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		t.Fatalf("mkdir receipts: %v", err)
	}

	// Create state/.obligations as a regular file so InitTaskObligations fails
	// on SaveTaskObligations → writeObligationsFile → os.MkdirAll.
	obligationsFile := filepath.Join(parentHome, "state", ".obligations")
	if err := os.WriteFile(obligationsFile, []byte("not-a-dir"), 0644); err != nil {
		t.Fatalf("writing obligations file: %v", err)
	}

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-soldier")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "task complete"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error due to file collision at parentHome/state/.obligations, got nil")
	}
	if !strings.Contains(err.Error(), "init task obligations") {
		t.Errorf("expected error mentioning 'init task obligations', got: %v", err)
	}

	// Assert receipt WAS written (WriteReceipt succeeds before obligations)
	if !turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		receiptPath := turnend.ReceiptPath(parentHome, "test-soldier", "default")
		if _, err := os.Stat(receiptPath); err != nil {
			t.Errorf("receipt should exist even when obligations fail, got: %v", err)
		}
	}

	// Assert no event was appended
	eventDir := filepath.Join(homeDir, "events")
	if entries, _ := os.ReadDir(eventDir); len(entries) > 0 {
		t.Errorf("no events should exist after obligations failure, found %d", len(entries))
	}

	// Assert no wake was enqueued
	wakePath := lifecycle.QueuePath(homeDir)
	if _, err := os.Stat(wakePath); err == nil {
		t.Error("wake queue should not exist after obligations failure")
	}

	// Assert no ack exists
	if turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		t.Error("no ack should exist after obligations failure")
	}
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

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success on writable paths, got: %v", err)
	}

	// Verify receipt and ack state: receipt exists, no ack yet
	if !turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		receiptPath := turnend.ReceiptPath(parentHome, "test-soldier", "default")
		if _, err := os.Stat(receiptPath); err != nil {
			t.Errorf("receipt should exist after successful report, got: %v", err)
		}
	}
}
