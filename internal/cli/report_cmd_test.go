package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// TestReportCmd_FailClosedOnReceiptWrite verifies that when WriteReceipt fails
// (parentHome/state is a regular file), the report command returns a typed
// TestReportCmd_FailClosedOnObligationsInit verifies that when
// InitTaskObligations fails (parentHome/state/.obligations is a regular file),
// the report returns errorCode=obligations_init_failed, receipt still writes,
// TestReportCmd_NonPRMessage_SkipsIdentityCapture verifies that a non-PR
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
