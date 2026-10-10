package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

func runUplinkReport(t *testing.T, notify func(string, string, orchestrator.NotificationRef) orchestrator.UplinkNotifyResult, args ...string) (string, string, Response[MessageResult]) {
	t.Helper()
	senderHome, receiverHome := t.TempDir(), t.TempDir()
	t.Setenv("MUNSU_HOME", senderHome)
	t.Setenv("MUNSU_TASK_ID", "task:with:colon")
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
		return orchestrator.QueuedNotification()
	}, "--ring", "no-ring", "done", "complete")
	pending, err := orchestrator.NewStore(receiverHome).ListPending("task_with_colon")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if !orchestrator.HasQueuedWakes(receiverHome) {
		t.Fatal("receiver wake missing")
	}
	if !orchestrator.HasAnyOpenReport(receiverHome, "task:with:colon") {
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
		return orchestrator.QueuedNotification()
	}, "--ring", "ring", "failed", "failed")
	if resp.Data.Injection == nil || resp.Data.Injection.Outcome != "queued" {
		t.Fatalf("response=%+v", resp.Data.Injection)
	}
}

func TestReportCmdTransportFailureFailsLoud(t *testing.T) {
	senderHome, receiverHome := t.TempDir(), t.TempDir()
	t.Setenv("MUNSU_HOME", senderHome)
	t.Setenv("MUNSU_TASK_ID", "task:failed-transport")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", receiverHome)
	transportErr := errors.New("backend failed")
	transport := sessionUplinkTransport{
		identity: func(string) (string, error) { return "tmux", nil },
		resolve: func(string) (backend.Backend, string, error) {
			return uplinkPromptBackend{result: backend.PromptResult{
				Status: backend.PromptBackendFailed,
				Err:    transportErr,
			}}, "tmux", nil
		},
	}
	cmd := newReportCmdWithNotifier(func(sender, _ string, ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		return transport.Notify(sender, orchestrator.TargetResult{
			Source: orchestrator.RuntimeSource,
			Handle: "pane",
		}, ref.Encode())
	})
	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"report", "--output", "json", "failed", "backend execution error"})
	err := root.Execute()
	if err == nil {
		t.Fatal("invoked transport failure must fail loud, not return success")
	}
	if !errors.Is(err, orchestrator.ErrReportDurable) {
		t.Fatalf("err = %v, want ErrReportDurable", err)
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("err = %v, want backend failure", err)
	}
	if !strings.Contains(err.Error(), "notification remains pending for reconciliation and retry") {
		t.Fatalf("err = %v, want reconciliation guidance", err)
	}
	// Soldier reports use the owning parent home as the sender home, which is
	// the same durable home as this direct-dispatch receiver.
	pending, pendingErr := orchestrator.NewStore(receiverHome).ListAllPending()
	if pendingErr != nil || len(pending) != 1 {
		t.Fatalf("sender pending records = %d, err = %v; want one durable retry record", len(pending), pendingErr)
	}
	envelope, envelopeErr := orchestrator.NewStore(receiverHome).ReadEnvelope(pending[0].SenderIdentity, pending[0].MessageID)
	if envelopeErr != nil || envelope == nil {
		t.Fatalf("receiver envelope = %v, err = %v; want durable committed report", envelope, envelopeErr)
	}
	t.Logf("CLI error: %v", err)
	t.Logf("durable message %s: receiver envelope committed; sender pending retry record retained", pending[0].MessageID)
}

func TestReportCmdImmediateNotificationUsesRefAndReturnsNotified(t *testing.T) {
	var got orchestrator.NotificationRef
	_, receiverHome, resp := runUplinkReport(t, func(_, _ string, ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		got = ref
		return orchestrator.AcknowledgedNotification()
	}, "--ring", "ring", "blocked", "waiting")
	if got.MessageID == "" || got.SenderIdentity != "task_with_colon" {
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

// Under direct General dispatch the soldier's parent home is the General
// itself. The persisted envelope must say so: issue #562 reproduced a durable
// artifact stamped receiver_rank "captain" in a topology with no captain,
// which the receiving General's own inbox then refused.
func TestReportCmdStampsReceiverRankFromTheReceivingHome(t *testing.T) {
	var ref orchestrator.NotificationRef
	_, receiverHome, _ := runUplinkReport(t, func(_, _ string, got orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		ref = got
		return orchestrator.AcknowledgedNotification()
	}, "--ring", "ring", "failed", "direct dispatch failure")

	_, homeRank, err := orchestrator.ReadHomeIdentity(receiverHome)
	if err != nil {
		t.Fatal(err)
	}
	if homeRank != orchestrator.RankGeneral {
		t.Fatalf("fixture receiver home rank = %q, want %q", homeRank, orchestrator.RankGeneral)
	}
	env, err := orchestrator.NewStore(receiverHome).ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil || env == nil {
		t.Fatalf("env=%+v err=%v", env, err)
	}
	if env.ReceiverRank != orchestrator.RankGeneral {
		t.Fatalf("receiver_rank = %q, want %q for a home of rank %q", env.ReceiverRank, orchestrator.RankGeneral, homeRank)
	}
	if _, err := orchestrator.NewReceiver(receiverHome); err != nil {
		t.Fatal(err)
	}
}

// TestReportCmdSoldierUplinkAppendsStatusLineOnce proves a soldier's uplink
// report keeps the .status projection DeliverWake writes, once per report.
func TestReportCmdSoldierUplinkAppendsStatusLineOnce(t *testing.T) {
	ring := []string{"--ring", "no-ring", "done", "PR https://github.com/org/repo/pull/42 checks green"}
	senderHome, _, _ := runUplinkReport(t, nil, ring...)
	if _, err := os.Stat(senderHome); err != nil {
		t.Fatal(err)
	}
	// replay the identical report against the same homes
	cmd := newReportCmdWithNotifier(nil)
	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append([]string{"report", "--output", "json"}, ring...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lines, err := home.ReadStatus(senderHome, "task:with:colon")
	if err != nil {
		t.Fatal(err)
	}
	want := "done: PR https://github.com/org/repo/pull/42 checks green [key=default]"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("status lines = %q, want exactly [%q]", lines, want)
	}
}
