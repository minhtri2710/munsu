package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

type recordingMailboxSender struct {
	ack      bool
	payloads []string
}

func (*recordingMailboxSender) Alive(string, map[string]string) (bool, error) { return true, nil }
func (s *recordingMailboxSender) Send(_ string, _ map[string]string, payload string) home.BoundSendResult {
	s.payloads = append(s.payloads, payload)
	return home.BoundSendResult{Status: "submitted", Acknowledged: s.ack}
}

func TestSendMailboxToCaptainNilSenderRetainsDurableWork(t *testing.T) {
	parent, captainHome, captainID := setupTestHomes(t)
	result := SendMailboxToCaptain(Info{ID: captainID, Home: captainHome}, parent, "status", nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "captain mailbox sender capability is required") || result.Acknowledged {
		t.Fatalf("result = %+v", result)
	}
	if env, _ := home.NewStore(captainHome).ReadEnvelope(baseIdentity(parent), result.MessageID); env == nil {
		t.Fatal("receiver envelope missing")
	}
	if pending, _ := home.NewStore(parent).ReadPending(baseIdentity(parent), result.MessageID); pending == nil {
		t.Fatal("sender pending missing")
	}
}

func TestMailboxSenderInstancesAreIsolated(t *testing.T) {
	parent1, home1, id1 := setupTestHomes(t)
	parent2, home2, id2 := setupTestHomes(t)
	one := &recordingMailboxSender{ack: true}
	two := &recordingMailboxSender{ack: true}
	SendMailboxToCaptain(Info{ID: id1, Home: home1}, parent1, "one", one)
	SendMailboxToCaptain(Info{ID: id2, Home: home2}, parent2, "two", two)
	if len(one.payloads) != 1 || len(two.payloads) != 1 || one.payloads[0] == two.payloads[0] {
		t.Fatalf("one=%v two=%v", one.payloads, two.payloads)
	}
}

func baseIdentity(homeDir string) string {
	identity, _, _ := home.ReadHomeIdentity(homeDir)
	return identity
}

func TestConvergeRejectsNilMailboxSender(t *testing.T) {
	parent := t.TempDir()
	_, err := Converge(parent, []Info{{ID: "captain", Home: t.TempDir()}}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: &captainNotificationTransport{acknowledged: true}})
	if err == nil || !strings.Contains(err.Error(), "captain mailbox sender capability is required") {
		t.Fatalf("error = %v", err)
	}
}
