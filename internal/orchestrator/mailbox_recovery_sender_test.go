package orchestrator

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
			inboxRoot := filepath.Join(home, "state", InboxDir)
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
			inboxRoot := filepath.Join(home, "state", InboxDir)
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
		if path == filepath.Join(stateRoot, InboxDir) {
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
