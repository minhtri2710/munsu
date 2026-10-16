package orchestrator

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type recoverySender struct {
	alive    bool
	result   mhome.BoundSendResult
	payloads []string
	sentTo   []taskauthority.EndpointBinding
	harness  string
}

func (s *recoverySender) Alive(_ string, _ taskauthority.EndpointBinding, harness string) (bool, error) {
	s.harness = harness
	return s.alive, nil
}
func (s *recoverySender) Send(_ string, ep taskauthority.EndpointBinding, payload string) mhome.BoundSendResult {
	s.payloads = append(s.payloads, payload)
	s.sentTo = append(s.sentTo, ep)
	return s.result
}

// Recovery delivers to the pane of the envelope's task: a captain's from its
// .meta (config-reread envelopes name captain:<id>), a soldier's from its
// canonical binding.
func TestRecoverInboxDeliversToTheTaskPane(t *testing.T) {
	for _, tc := range []struct {
		name, taskID string
		seed         func(t *testing.T, home string)
		want         taskauthority.EndpointBinding
		harness      string
	}{
		{"captain", "captain:c1", func(t *testing.T, home string) {
			if err := mhome.WriteMeta(home, "captain:c1", map[string]string{"backend": "herdr", "window": "s:p1", "session_owner": "s", "harness": "pi"}); err != nil {
				t.Fatal(err)
			}
		}, taskauthority.EndpointBinding{Backend: "herdr", Handle: "s:p1", SessionOwner: "s"}, "pi"},
		{"soldier", "task-1", func(t *testing.T, home string) {
			seedBoundSoldier(t, home, "task-1", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@1"})
		}, taskauthority.EndpointBinding{Backend: "tmux", Handle: "@1", LeaseID: "lease-ep", FenceToken: "fence-ep", Incarnation: "inc-ep", BoundAtUnix: 1}, "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.seed(t, home)
			env := recoveryEnvelope("")
			env.TaskID = tc.taskID
			sender := &recoverySender{alive: true, result: mhome.BoundSendResult{Acknowledged: true, Status: "submitted"}}
			attempt := RecoverInboxWithSender(sender, home, env)
			if attempt.Err != nil || len(sender.sentTo) != 1 || sender.sentTo[0] != tc.want || sender.harness != tc.harness {
				t.Fatalf("attempt=%+v sentTo=%+v harness=%q, want %+v harness %q", attempt, sender.sentTo, sender.harness, tc.want, tc.harness)
			}
		})
	}
}

func recoveryEnvelope(kind string) *mhome.Envelope {
	return &mhome.Envelope{
		SchemaVersion:  mhome.SchemaVersion,
		MessageID:      "recovery-message",
		SenderRank:     mhome.RankGeneral,
		SenderIdentity: "general",
		ReceiverRank:   mhome.RankCaptain,
		ReceiverID:     "captain-1",
		Kind:           kind,
		TaskID:         "task-1",
		Payload:        "recover me",
		PayloadHash:    mhome.PayloadHashHex("recover me"),
		CreatedAt:      1,
	}
}

func writeRecoveryEnvelope(t *testing.T, home string, env *mhome.Envelope) {
	t.Helper()
	if _, err := taskEndpoint(home, env.TaskID); err != nil {
		seedBoundSoldier(t, home, env.TaskID, taskauthority.EndpointBinding{Backend: "tmux", Handle: "pane-1"})
	}
	if err := mhome.NewStore(home).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverAllInboxesAllowsAbsentRoot(t *testing.T) {
	home := t.TempDir()
	called := false

	attempts, err := recoverAllInboxesWithFS(&recoverySender{}, home, os.Stat, func(string) ([]os.DirEntry, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("absent inbox root error = %v, want nil", err)
	}
	if attempts != nil {
		t.Fatalf("absent inbox root attempts = %v, want nil", attempts)
	}
	if called {
		t.Fatal("ReadDir called for absent inbox root")
	}
}

func TestRecoverAllInboxesRejectsNonDirectoryRegardlessOfReadDirResult(t *testing.T) {
	// These cases are identical after the type check, but diverge under the
	// pre-fix mutation where ReadDir is consulted and its result is classified.
	for _, readDirResult := range []struct {
		name string
		err  error
	}{
		{name: "nil error", err: nil},
		{name: "not found", err: fs.ErrNotExist},
	} {
		t.Run(readDirResult.name, func(t *testing.T) {
			home := t.TempDir()
			inboxRoot := filepath.Join(home, "state", mhome.InboxDir)
			if err := os.MkdirAll(filepath.Dir(inboxRoot), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inboxRoot, []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}

			_, err := recoverAllInboxesWithFS(&recoverySender{}, home, os.Stat, func(string) ([]os.DirEntry, error) {
				return nil, readDirResult.err
			})
			if err == nil {
				t.Fatalf("regular inbox file with ReadDir %s returned nil error", readDirResult.name)
			}
		})
	}
}

func TestRecoverAllInboxesPropagatesReadDirFailureForDirectory(t *testing.T) {
	for _, readDirErr := range []error{errors.New("read directory failed"), fs.ErrNotExist} {
		t.Run(readDirErr.Error(), func(t *testing.T) {
			home := t.TempDir()
			inboxRoot := filepath.Join(home, "state", mhome.InboxDir)
			if err := os.MkdirAll(inboxRoot, 0700); err != nil {
				t.Fatal(err)
			}

			_, err := recoverAllInboxesWithFS(&recoverySender{}, home, os.Stat, func(string) ([]os.DirEntry, error) {
				return nil, readDirErr
			})
			if err == nil || !strings.Contains(err.Error(), readDirErr.Error()) {
				t.Fatalf("directory ReadDir failure = %v, want propagated error", err)
			}
		})
	}
}

func TestRecoverAllInboxesRejectsNonDirectoryStateRoot(t *testing.T) {
	home := t.TempDir()
	stateRoot := filepath.Join(home, "state")
	if err := os.WriteFile(stateRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	readDirCalled := false
	stat := func(path string) (os.FileInfo, error) {
		if path == filepath.Join(stateRoot, mhome.InboxDir) {
			return nil, fs.ErrNotExist
		}
		return os.Stat(path)
	}

	_, err := recoverAllInboxesWithFS(&recoverySender{}, home, stat, func(string) ([]os.DirEntry, error) {
		readDirCalled = true
		return nil, nil
	})
	if err == nil {
		t.Fatal("regular state root returned nil error")
	}
	if readDirCalled {
		t.Fatal("ReadDir called after state root was rejected")
	}
}

func TestRecoverAllInboxesWithSenderUsesExplicitCapability(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("command")
	writeRecoveryEnvelope(t, home, env)
	sender := &recoverySender{alive: true, result: mhome.BoundSendResult{Status: "submitted", Acknowledged: true}}

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

func TestRecoverAllInboxesWithSenderDeliversOnlyValidatedInboxRecords(t *testing.T) {
	home := t.TempDir()
	valid := recoveryEnvelope("command")
	valid.MessageID = "valid-recovery"
	writeRecoveryEnvelope(t, home, valid)

	forged := recoveryEnvelope("command")
	forged.MessageID = "forged-recovery"
	writeRecoveryEnvelope(t, home, forged)
	forged.Payload = "forged payload"
	data, err := json.MarshalIndent(forged, "", "  ")
	if err != nil {
		t.Fatalf("marshal forged envelope: %v", err)
	}
	path := filepath.Join(home, "state", mhome.InboxDir, forged.SenderIdentity, forged.MessageID+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write forged envelope: %v", err)
	}

	sender := &recoverySender{alive: true, result: mhome.BoundSendResult{Status: "submitted", Acknowledged: true}}
	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || !attempts[0].Delivered || attempts[0].MessageID != valid.MessageID {
		t.Fatalf("attempts = %+v, want only valid envelope", attempts)
	}
	if len(sender.payloads) != 1 || sender.payloads[0] != valid.Payload {
		t.Fatalf("recovery payloads = %v, want only %q", sender.payloads, valid.Payload)
	}
}

func TestRecoverAllInboxesWithSenderSkipsAckOnlyInboxRecord(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("command")
	writeRecoveryEnvelope(t, home, env)
	store := mhome.NewStore(home)
	ack := &mhome.ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    2,
		Outcome:        mhome.OutcomeAccepted,
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}
	payloadPath := filepath.Join(home, "state", mhome.InboxDir, env.SenderIdentity, env.MessageID+".json")
	if err := os.Remove(payloadPath); err != nil {
		t.Fatalf("remove payload: %v", err)
	}

	sender := &recoverySender{alive: true, result: mhome.BoundSendResult{Status: "submitted", Acknowledged: true}}
	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %+v, want no recovery for ack-only record", attempts)
	}
	if len(sender.payloads) != 0 {
		t.Fatalf("ack-only record was redelivered: %v", sender.payloads)
	}
	if !store.IsAcked(env) {
		t.Fatal("ack tombstone was not retained")
	}
}

func TestRecoverAllInboxesWithSenderFailureLeavesEnvelopeAndMarkerAbsent(t *testing.T) {
	home := t.TempDir()
	env := recoveryEnvelope("command")
	writeRecoveryEnvelope(t, home, env)
	sender := &recoverySender{alive: true, result: mhome.BoundSendResult{Status: "backend-failed", Err: os.ErrPermission}}

	attempts, err := RecoverAllInboxesWithSender(sender, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Err == nil || attempts[0].Delivered {
		t.Fatalf("attempts = %+v", attempts)
	}
	if _, err := mhome.NewStore(home).ReadEnvelope(env.SenderIdentity, env.MessageID); err != nil {
		t.Fatalf("envelope missing after failed recovery: %v", err)
	}
	if _, err := os.Stat(RecoveryMarkerPath(home, env.MessageID)); !os.IsNotExist(err) {
		t.Fatalf("marker after failed recovery: %v", err)
	}
}
