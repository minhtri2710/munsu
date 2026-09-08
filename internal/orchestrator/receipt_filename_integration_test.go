//go:build integration

package orchestrator

import (
	"os"
	"strings"
	"testing"
)

// TestDeliverWake_RejectsDottedKey verifies the sole receipt writer fails
// closed on a non-slug terminal key before writing any artifact, so a dotted
// key can never enter the filename encoding.
func TestDeliverWake_RejectsDottedKey(t *testing.T) {
	soldierHome, captainHome := deliverEnv(t, true)
	_, err := DeliverWake(DeliverRequest{
		HomeDir:    soldierHome,
		ParentHome: captainHome,
		TaskID:     "task-a",
		State:      "done",
		Message:    "complete",
		Role:       "soldier",
		Key:        "2.k",
	})
	if err == nil {
		t.Fatal("DeliverWake with a dotted key should error")
	}
	p, perr := ReceiptPath(captainHome, "task-a", "2.k")
	if perr != nil {
		t.Fatal(perr)
	}
	if _, serr := os.Stat(p); serr == nil {
		t.Error("no receipt should be written when the key is rejected")
	}
}

// recordingTransport captures the payloads a nudge would submit and reports
// them acknowledged, so the activation count and message are observable.
type recordingTransport struct{ payloads []string }

func (r *recordingTransport) Attempt(homeDir string, target TargetResult, payload string) ActivationAttempt {
	r.payloads = append(r.payloads, payload)
	return ActivationAttempt{Acknowledged: true, SubmitStatus: "ok"}
}

// TestActivateOnReceipt_DottedTaskIDNamesCorrectTask asserts the nudge names
// the real task (v1.2), not the misparsed v1, and that a second pass is
// idempotent. The idempotency half alone is a green-proxy: because
// stem+"."+termKey concatenates identically regardless of the split point, the
// buggy first-dot parse also marks and re-checks the same activation-seen file
// and so also nudges zero on the second call. The load-bearing assertion is
// that the FIRST nudge names v1.2 and not v1/[key=2.k].
func TestActivateOnReceipt_DottedTaskIDNamesCorrectTask(t *testing.T) {
	captainHome := receiptEnv(t)
	parentHome := parentHomeWithMeta(t, captainHome, "test-captain", "p1", "w1")
	if err := WriteReceipt(captainHome, "v1.2", "k", "done", "complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	tr := &recordingTransport{}
	count := ActivateOnReceiptWithTransport(captainHome, parentHome, tr)
	if count != 1 {
		t.Fatalf("first activation count = %d, want 1", count)
	}
	if len(tr.payloads) != 1 {
		t.Fatalf("recorded %d payloads, want 1", len(tr.payloads))
	}
	if !strings.Contains(tr.payloads[0], "soldier v1.2 [key=k]") {
		t.Errorf("nudge = %q, want it to name task v1.2 with key k", tr.payloads[0])
	}
	if strings.Contains(tr.payloads[0], "key=2.k") {
		t.Errorf("nudge misparsed the key: %q", tr.payloads[0])
	}
	if again := ActivateOnReceiptWithTransport(captainHome, parentHome, tr); again != 0 {
		t.Errorf("second activation count = %d, want 0 (idempotent)", again)
	}
}
