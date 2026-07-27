package mailbox

import (
	"os"
	"path/filepath"
	"testing"
)

type recoverySender struct {
	alive    bool
	result   BoundSendResult
	payloads []string
}

func (s *recoverySender) Alive(string, map[string]string) (bool, error) { return s.alive, nil }
func (s *recoverySender) Send(_ string, _ map[string]string, payload string) BoundSendResult {
	s.payloads = append(s.payloads, payload)
	return s.result
}

func recoveryEnvelope(kind string) *Envelope {
	return &Envelope{
		SchemaVersion:  SchemaVersion,
		MessageID:      "recovery-message",
		SenderRank:     RankGeneral,
		SenderIdentity: "general",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Kind:           kind,
		TaskID:         "task-1",
		Payload:        "recover me",
		PayloadHash:    PayloadHashHex("recover me"),
		CreatedAt:      1,
	}
}

func writeRecoveryEnvelope(t *testing.T, home string, env *Envelope) {
	t.Helper()
	if err := NewStore(home).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "task-1.meta"), []byte("backend=tmux\nwindow=pane-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverAllInboxesWithSenderUsesExplicitCapability(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("command")
	writeRecoveryEnvelope(t, home, env)
	sender := &recoverySender{alive: true, result: BoundSendResult{Status: "submitted", Acknowledged: true}}

	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || !attempts[0].Delivered {
		t.Fatalf("attempts = %+v", attempts)
	}
	if len(sender.payloads) != 1 || sender.payloads[0] != env.Payload {
		t.Fatalf("payloads = %v", sender.payloads)
	}
	if _, err := os.Stat(RecoveryMarkerPath(home, env.MessageID)); err != nil {
		t.Fatalf("recovery marker missing: %v", err)
	}
}

func TestRecoverAllInboxesWithSenderSkipsUplinkReport(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("uplink-report")
	writeRecoveryEnvelope(t, home, env)
	sender := &recoverySender{alive: true, result: BoundSendResult{Status: "submitted", Acknowledged: true}}

	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || !attempts[0].Skipped || attempts[0].Delivered {
		t.Fatalf("attempts = %+v", attempts)
	}
	if len(sender.payloads) != 0 {
		t.Fatalf("uplink report was sent: %v", sender.payloads)
	}
}

func TestRecoverAllInboxesWithSenderFailureLeavesEnvelopeAndMarkerAbsent(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("command")
	writeRecoveryEnvelope(t, home, env)
	sender := &recoverySender{alive: true, result: BoundSendResult{Status: "backend-failed", Err: os.ErrPermission}}

	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Err == nil || attempts[0].Delivered {
		t.Fatalf("attempts = %+v", attempts)
	}
	if _, err := NewStore(home).ReadEnvelope(env.SenderIdentity, env.MessageID); err != nil {
		t.Fatalf("envelope missing after failed recovery: %v", err)
	}
	if _, err := os.Stat(RecoveryMarkerPath(home, env.MessageID)); !os.IsNotExist(err) {
		t.Fatalf("marker after failed recovery: %v", err)
	}
}
