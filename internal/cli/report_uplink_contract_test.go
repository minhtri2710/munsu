package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

func runUplinkReport(t *testing.T, notify func(string, string, orchestrator.NotificationRef) orchestrator.UplinkNotifyResult, args ...string) (string, string, Response[MessageResult]) {
	t.Helper()
	senderHome, receiverHome := t.TempDir(), t.TempDir()
	t.Setenv("MUNSU_HOME", senderHome)
	t.Setenv("MUNSU_TASK_ID", "task:with/slash")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", receiverHome)
	cmd := newReportCmdWithNotifier(notify)
	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"report", "--output", "json"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var resp Response[MessageResult]
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return senderHome, receiverHome, resp
}

func TestReportCmdNoRingCreatesDurableMailboxOnly(t *testing.T) {
	senderHome, receiverHome, resp := runUplinkReport(t, func(string, string, orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		t.Fatal("no-ring must not notify")
		return orchestrator.UplinkNotifyResult{}
	}, "--ring", "no-ring", "done", "complete")
	pending, err := orchestrator.NewStore(receiverHome).ListPending("task_with_slash")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if !orchestrator.HasQueuedWakes(receiverHome) {
		t.Fatal("receiver wake missing")
	}
	if !orchestrator.HasAnyOpenReport(receiverHome, "task:with/slash") {
		t.Fatal("open evidence missing")
	}
	if resp.Data.Injection == nil || resp.Data.Injection.Outcome != "queued" {
		t.Fatalf("response=%+v", resp.Data.Injection)
	}
	if _, err := os.Stat(filepath.Join(receiverHome, "state", ".terminal-receipts")); !os.IsNotExist(err) {
		t.Fatal("new material report must not create terminal receipts")
	}
	_ = senderHome
}

func TestReportCmdNotificationFailureReturnsQueued(t *testing.T) {
	_, _, resp := runUplinkReport(t, func(string, string, orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		return orchestrator.UplinkNotifyResult{Queued: true}
	}, "--ring", "ring", "failed", "failed")
	if resp.Data.Injection == nil || resp.Data.Injection.Outcome != "queued" {
		t.Fatalf("response=%+v", resp.Data.Injection)
	}
}

func TestReportCmdImmediateNotificationUsesRefAndReturnsNotified(t *testing.T) {
	var got orchestrator.NotificationRef
	_, receiverHome, resp := runUplinkReport(t, func(_, _ string, ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		got = ref
		return orchestrator.UplinkNotifyResult{Acknowledged: true}
	}, "--ring", "ring", "blocked", "waiting")
	if got.MessageID == "" || got.SenderIdentity != "task_with_slash" {
		t.Fatalf("ref=%+v", got)
	}
	env, err := orchestrator.NewStore(receiverHome).ReadEnvelope(got.SenderIdentity, got.MessageID)
	if err != nil || env == nil {
		t.Fatalf("env=%+v err=%v", env, err)
	}
	if got.Encode() == env.Payload {
		t.Fatal("notification must be a ref, not raw payload")
	}
	if resp.Data.Injection == nil || resp.Data.Injection.Outcome != "notified" {
		t.Fatalf("response=%+v", resp.Data.Injection)
	}
}
