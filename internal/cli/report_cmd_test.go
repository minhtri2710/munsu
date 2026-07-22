package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// TestReportCmd_FailClosedOnReceiptWrite verifies that when WriteReceipt fails
// (parentHome/state is a regular file), the report command returns a typed
// error with errorCode=receipt_write_failed and no event/wake/ack is produced.
func TestReportCmd_FailClosedOnReceiptWrite(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Make parentHome/state a regular file so os.MkdirAll fails
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

	var cerr *contractError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected contractError, got %T", err)
	}
	if cerr.value.Error.ErrorCode != "receipt_write_failed" {
		t.Errorf("expected errorCode receipt_write_failed, got %q", cerr.value.Error.ErrorCode)
	}

	// Assert no event log exists
	eventPath := event.LogPath(homeDir)
	if _, err := os.Stat(eventPath); err == nil {
		t.Error("event log should not exist after receipt failure")
	}

	// Assert no wake queue
	wakePath := lifecycle.QueuePath(homeDir)
	if _, err := os.Stat(wakePath); err == nil {
		t.Error("wake queue should not exist after receipt failure")
	}

	// Assert no ack
	if turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		t.Error("no ack should exist after receipt failure")
	}
}

// TestReportCmd_FailClosedOnObligationsInit verifies that when
// InitTaskObligations fails (parentHome/state/.obligations is a regular file),
// the report returns errorCode=obligations_init_failed, receipt still writes,
// and no event/wake/ack is produced.
func TestReportCmd_FailClosedOnObligationsInit(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Create state/.terminal-receipts so WriteReceipt succeeds
	receiptsDir := filepath.Join(parentHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		t.Fatalf("mkdir receipts: %v", err)
	}

	// Make state/.obligations a regular file so InitTaskObligations fails
	obligFile := filepath.Join(parentHome, "state", ".obligations")
	if err := os.WriteFile(obligFile, []byte("not-a-dir"), 0644); err != nil {
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
		t.Fatal("expected error due to file collision at .obligations, got nil")
	}

	var cerr *contractError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected contractError, got %T", err)
	}
	if cerr.value.Error.ErrorCode != "obligations_init_failed" {
		t.Errorf("expected errorCode obligations_init_failed, got %q", cerr.value.Error.ErrorCode)
	}

	// Receipt should exist (WriteReceipt succeeded before obligation failure)
	receiptPath := turnend.ReceiptPath(parentHome, "test-soldier", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist even when obligations fail: %v", err)
	}

	// But no ack
	if turnend.IsReceiptAcked(parentHome, "test-soldier", "default") {
		t.Error("no ack should exist after obligations failure")
	}

	// Assert no event log
	eventPath := event.LogPath(homeDir)
	if _, err := os.Stat(eventPath); err == nil {
		t.Error("event log should not exist after obligations failure")
	}

	// Assert no wake queue
	wakePath := lifecycle.QueuePath(homeDir)
	if _, err := os.Stat(wakePath); err == nil {
		t.Error("wake queue should not exist after obligations failure")
	}
}

// TestReportCmd_FailClosed_NormalPath verifies success on the happy path.
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

	receiptPath := turnend.ReceiptPath(parentHome, "test-soldier", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist after successful report: %v", err)
	}
}

// TestReportCmd_NonPRMessage_SkipsIdentityCapture verifies that a non-PR
// soldier report does not attempt identity capture (no error, flows through).
func TestReportCmd_NonPRMessage_SkipsIdentityCapture(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-no-pr")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "task complete no PR"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success for non-PR report: %v", err)
	}

	// Meta should NOT have been created (no identity capture attempted)
	_, metaErr := task.ReadMeta(homeDir, "test-no-pr")
	if metaErr == nil {
		t.Error("meta should not exist — no identity capture for non-PR message")
	}

	// Receipt should exist (normal path)
	receiptPath := turnend.ReceiptPath(parentHome, "test-no-pr", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist: %v", err)
	}
}

// TestReportCmd_MalformedPRURL_FailsClosed verifies that a PR-prefixed
// message with an invalid URL fails closed before any terminal state is written.
func TestReportCmd_MalformedPRURL_FailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-bad-url")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "PR not-a-valid-url"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for malformed PR URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PR URL") {
		t.Errorf("expected 'invalid PR URL' error, got: %v", err)
	}
}

// TestReportCmd_NonGithubPRURL_FailsClosed verifies that a PR-prefixed
// message with a non-GitHub URL fails closed.
func TestReportCmd_NonGithubPRURL_FailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-non-gh")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "PR https://gitlab.com/owner/repo/pull/1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for non-GitHub URL, got nil")
	}
	if !strings.Contains(err.Error(), "not a github.com URL") {
		t.Errorf("expected 'not a github.com URL' error, got: %v", err)
	}
}

// TestReportCmd_PRURL_NoStatusOnMalformedURL verifies that a malformed PR URL
// message fails before any status or receipt is written.
func TestReportCmd_PRURL_NoStatusOnMalformedURL(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-no-output")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "PR not-a-valid-url"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Assert no status file was created
	statusPath := filepath.Join(homeDir, "state", "test-no-output.status")
	if _, err := os.Stat(statusPath); err == nil {
		t.Error("status file should not exist after malformed PR URL")
	}

	// Assert no receipt was written
	receiptPath := turnend.ReceiptPath(parentHome, "test-no-output", "default")
	if _, err := os.Stat(receiptPath); err == nil {
		t.Error("receipt should not exist after malformed PR URL")
	}

	// Assert no meta was written
	_, metaErr := task.ReadMeta(homeDir, "test-no-output")
	if metaErr == nil {
		t.Error("meta should not exist after malformed PR URL")
	}
}

// TestReportCmd_PRURL_EmptyAfterPrefix_FailsClosed verifies that "PR "
// followed by nothing fails closed.
func TestReportCmd_PRURL_EmptyAfterPrefix_FailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-empty-pr")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "PR "})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for empty URL after PR prefix, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' error, got: %v", err)
	}
}
