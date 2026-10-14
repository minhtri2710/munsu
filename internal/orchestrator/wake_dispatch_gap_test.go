package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// The submitted prompt names the claimed wake exactly: its Epoch:Seq as key
// and event id, the lease as claim id, and the resolve command with each in
// its own flag. The lease is held by munsu:herdr.
func TestDispatchWakePromptNamesTheClaim(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	submit := req.Submit.(*mockSubmitPort)
	if result, err := DispatchWake(req); err != nil || result.Outcome != WakeSubmitted {
		t.Fatalf("DispatchWake = %+v, %v; want submitted", result, err)
	}
	leases, err := mhome.ListClaimedLeases(home)
	if err != nil || len(leases) != 1 || len(leases[0].Wakes) != 1 {
		t.Fatalf("claimed leases = %+v, %v; want one lease holding one wake", leases, err)
	}
	lease := leases[0]
	if lease.Consumer != "munsu:herdr" {
		t.Fatalf("lease consumer = %q, want munsu:herdr", lease.Consumer)
	}
	w := lease.Wakes[0]
	event := w.Epoch + ":" + w.Seq
	want := "[mu-system:wake]\nkey: " + event + "\nclaim_id: " + lease.LeaseID + "\nevent_id: " + event +
		"\n\ntest payload\n\nReview this durable wake, then run:\nmunsu wake resolve --claim-id \"" + lease.LeaseID +
		"\" --event-id \"" + event + "\" --summary \"<non-empty summary>\""
	if submit.prompt != want {
		t.Fatalf("prompt:\n%s\nwant:\n%s", submit.prompt, want)
	}
}

// A claim that fails leaves the wake queued and is a skip, not an error.
func TestDispatchWakeClaimErrorSkips(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	if err := os.WriteFile(filepath.Join(home, "state", ".wake-leases"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped || result.Reason != "claim-error" {
		t.Fatalf("DispatchWake = %+v, %v; want skipped claim-error, nil", result, err)
	}
	if req.Submit.(*mockSubmitPort).prompt != "" {
		t.Fatal("a failed claim still submitted a prompt")
	}
}
