package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

type outboxFakeBackend struct {
	alive    bool
	sent     []string
	sendErr  error
	windowID string
}

func (f *outboxFakeBackend) NewWindow(session, name string) (string, error) {
	return f.windowID, nil
}
func (f *outboxFakeBackend) SendKeys(windowID, text string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, text)
	return nil
}
func (f *outboxFakeBackend) Capture(windowID string, lines int) (string, error) {
	return "", nil
}
func (f *outboxFakeBackend) Alive(windowID string) bool { return f.alive }
func (f *outboxFakeBackend) Teardown(windowID string) error {
	return nil
}

func installOutboxBackend(t *testing.T, bk session.Backend) {
	t.Helper()
	orig := backendForTask
	backendForTask = func(parentHome string, meta map[string]string) (session.Backend, string, error) {
		return bk, "fake", nil
	}
	t.Cleanup(func() { backendForTask = orig })
}

func writeCaptainMeta(t *testing.T, parent, smID, smHome, window string) {
	t.Helper()
	canon, err := canonicalHome(smHome)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		"kind":    "captain",
		"sm_id":   smID,
		"home":    canon,
		"window":  window,
		"backend": "fake",
	}
	if err := task.WriteMeta(parent, taskIDForCaptain(smID), meta); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueSendOutbox_PersistsMarkedMessage(t *testing.T) {
	parent := t.TempDir()
	smID := "munsu"
	msg := marker.MarkFromGeneral("report progress")

	if err := EnqueueSendOutbox(parent, smID, msg); err != nil {
		t.Fatalf("EnqueueSendOutbox: %v", err)
	}

	paths, err := listSendOutboxPaths(parent, smID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths=%d want 1", len(paths))
	}
	entry, err := readSendOutboxEntry(paths[0])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if entry["message"] != msg {
		t.Errorf("message=%q want %q", entry["message"], msg)
	}
	if entry["id"] != smID {
		t.Errorf("id=%q want %q", entry["id"], smID)
	}
	if !marker.IsFromGeneral(entry["message"]) {
		t.Error("queued message must retain from-general marker")
	}
}

func TestEnqueueSendOutbox_RejectsMultiline(t *testing.T) {
	parent := t.TempDir()
	err := EnqueueSendOutbox(parent, "x", "line1\nline2")
	if err == nil {
		t.Fatal("expected error for multiline message")
	}
}

func TestFlushSendOutbox_RetainsWhenDead(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "dead-captain"
	window := "@dead"

	msg := marker.MarkFromGeneral("still need this")
	if err := EnqueueSendOutbox(parent, smID, msg); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: false, windowID: window}
	installOutboxBackend(t, fake)

	err := FlushSendOutbox(parent, Info{ID: smID, Home: smHome})
	if err == nil {
		t.Fatal("expected flush error when endpoint dead")
	}
	if !strings.Contains(err.Error(), "not alive") {
		t.Errorf("error should mention not alive, got: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Errorf("should not send when dead, sent=%v", fake.sent)
	}

	paths, listErr := listSendOutboxPaths(parent, smID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(paths) != 1 {
		t.Fatalf("outbox should retain entry, got %d paths", len(paths))
	}
}

func TestFlushSendOutbox_DeliversWhenAlive(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "live-captain"
	window := "@live"

	msg1 := marker.MarkFromGeneral("first")
	msg2 := marker.MarkFromGeneral("second")
	if err := EnqueueSendOutbox(parent, smID, msg1); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueSendOutbox(parent, smID, msg2); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: true, windowID: window}
	installOutboxBackend(t, fake)

	if err := FlushSendOutbox(parent, Info{ID: smID, Home: smHome}); err != nil {
		t.Fatalf("FlushSendOutbox: %v", err)
	}
	if len(fake.sent) != 2 {
		t.Fatalf("sent=%d want 2: %v", len(fake.sent), fake.sent)
	}
	if fake.sent[0] != msg1 || fake.sent[1] != msg2 {
		t.Errorf("FIFO order broken: %v", fake.sent)
	}
	if !marker.IsFromGeneral(fake.sent[0]) {
		t.Error("delivered line must keep marker")
	}

	paths, err := listSendOutboxPaths(parent, smID)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("outbox should be empty after flush, got %d", len(paths))
	}
}

func TestFlushSendOutbox_SendErrorRetainsRemaining(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "partial"
	window := "@w"

	if err := EnqueueSendOutbox(parent, smID, marker.MarkFromGeneral("a")); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueSendOutbox(parent, smID, marker.MarkFromGeneral("b")); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{
		alive:    true,
		windowID: window,
		sendErr:  fmt.Errorf("inject failed"),
	}
	installOutboxBackend(t, fake)

	err := FlushSendOutbox(parent, Info{ID: smID, Home: smHome})
	if err == nil {
		t.Fatal("expected send error")
	}
	paths, listErr := listSendOutboxPaths(parent, smID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	// First send failed so both remain (nothing removed before successful send).
	if len(paths) != 2 {
		t.Fatalf("want 2 retained, got %d", len(paths))
	}
}

func TestCaptainIDFromTask(t *testing.T) {
	if got := CaptainIDFromTask("captain:munsu", map[string]string{"sm_id": "munsu"}); got != "munsu" {
		t.Errorf("got %q", got)
	}
	if got := CaptainIDFromTask("captain:munsu", nil); got != "munsu" {
		t.Errorf("prefix fallback got %q", got)
	}
	if got := CaptainIDFromTask("captain:other", map[string]string{"sm_id": "real"}); got != "real" {
		t.Errorf("sm_id prefer got %q", got)
	}
}

func TestSendOutboxDirConstant(t *testing.T) {
	// Documented path fragment for operators/errors.
	if SendOutboxDir != ".captain-send-outbox" {
		t.Fatalf("SendOutboxDir=%q", SendOutboxDir)
	}
	_ = os.TempDir()
	_ = filepath.Join
}
