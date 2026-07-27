package supervision

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/mailbox"
)

type testMailboxSender struct{ payloads []string }

func (*testMailboxSender) Alive(string, map[string]string) (bool, error) { return true, nil }
func (s *testMailboxSender) Send(_ string, _ map[string]string, payload string) mailbox.BoundSendResult {
	s.payloads = append(s.payloads, payload)
	return mailbox.BoundSendResult{Status: "submitted", Acknowledged: true}
}

func TestRunCycleWithProbeAndSenderRejectsNilHooksWithoutMarkingRecoveryDone(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, nil); err == nil {
		t.Fatal("expected missing hooks error")
	}
	if _, loaded := recoveryDone.Load(home); loaded {
		t.Fatal("nil hooks marked recovery complete")
	}
}

func TestRunCycleWithProbeAndSenderRejectsNilSenderWithoutMarkingRecoveryDone(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, nil, NoopWatcherHooks{}); err == nil {
		t.Fatal("expected missing sender error")
	}
	if _, loaded := recoveryDone.Load(home); loaded {
		t.Fatal("nil sender marked recovery complete")
	}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, NoopWatcherHooks{}); err != nil {
		t.Fatalf("valid sender could not retry recovery: %v", err)
	}
}

func TestRunCycleRecoveryScanFailureCanRetry(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	inboxRoot := filepath.Join(home, "state", mailbox.InboxDir)
	if err := os.MkdirAll(filepath.Dir(inboxRoot), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inboxRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, NoopWatcherHooks{}); err == nil {
		t.Fatal("expected inbox scan error")
	}
	if _, loaded := recoveryDone.Load(home); loaded {
		t.Fatal("failed scan marked recovery complete")
	}
	if err := os.Remove(inboxRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, NoopWatcherHooks{}); err != nil {
		t.Fatalf("corrected recovery did not retry: %v", err)
	}
}

func TestRunCycleRecoveryUsesExplicitSenderAndWritesMarker(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	env := &mailbox.Envelope{
		SchemaVersion: mailbox.SchemaVersion, MessageID: "watcher-recovery", SenderRank: mailbox.RankGeneral,
		SenderIdentity: "general", ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		Kind: "command", TaskID: "task-1", Payload: "wake", PayloadHash: mailbox.PayloadHashHex("wake"), CreatedAt: 1,
	}
	if err := mailbox.NewStore(home).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "task-1.meta"), []byte("backend=tmux\nwindow=pane-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sender := &testMailboxSender{}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, sender, NoopWatcherHooks{}); err != nil {
		t.Fatal(err)
	}
	if len(sender.payloads) != 1 || sender.payloads[0] != "wake" {
		t.Fatalf("sender payloads = %v", sender.payloads)
	}
	if _, err := os.Stat(mailbox.RecoveryMarkerPath(home, env.MessageID)); err != nil {
		t.Fatalf("recovery marker missing: %v", err)
	}
}
