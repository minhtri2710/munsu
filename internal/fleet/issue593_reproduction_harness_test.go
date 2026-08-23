//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestIssue593ReproductionHarness pins both halves of the direct General
// downlink fix and keeps the Captain control path covered.
func TestIssue593ReproductionHarness(t *testing.T) {
	generalHome := filepath.Join(t.TempDir(), "general-home")
	captainHome := filepath.Join(t.TempDir(), "captain-home")
	for _, dir := range []string{generalHome, captainHome} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := home.WriteMeta(dir, "task-soldier", map[string]string{"window": "soldier-window"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := home.WriteHomeIdentity(captainHome, "captain-home", home.RankCaptain); err != nil {
		t.Fatal(err)
	}

	generalResult := SendToSoldier(generalHome, "task-soldier", "general-home", "general work", &harnessEndpoint{})
	if generalResult.Err != nil || !generalResult.Sent {
		t.Fatalf("General dispatch failed: sent=%v err=%v", generalResult.Sent, generalResult.Err)
	}
	generalEnvelopes, err := home.NewStore(generalHome).ListInbox("general-home")
	if err != nil || len(generalEnvelopes) != 1 {
		t.Fatalf("reading General envelope: count=%d err=%v", len(generalEnvelopes), err)
	}
	generalEnvelope := generalEnvelopes[0]
	if generalEnvelope.SenderRank != home.RankGeneral {
		t.Fatalf("persisted General sender rank = %q, want general", generalEnvelope.SenderRank)
	}
	if err := home.ValidateEnvelope(generalEnvelope); err != nil {
		t.Fatalf("persisted General envelope rejected: %v", err)
	}

	captainResult := SendToSoldier(captainHome, "task-soldier", "captain-home", "captain work", &harnessEndpoint{})
	if captainResult.Err != nil || !captainResult.Sent {
		t.Fatalf("Captain dispatch failed: sent=%v err=%v", captainResult.Sent, captainResult.Err)
	}
	captainEnvelopes, err := home.NewStore(captainHome).ListInbox("captain-home")
	if err != nil || len(captainEnvelopes) != 1 {
		t.Fatalf("reading Captain envelope: count=%d err=%v", len(captainEnvelopes), err)
	}
	if captainEnvelopes[0].SenderRank != home.RankCaptain {
		t.Fatalf("persisted Captain sender rank = %q, want captain", captainEnvelopes[0].SenderRank)
	}
	if err := home.ValidateEnvelope(captainEnvelopes[0]); err != nil {
		t.Fatalf("persisted Captain envelope rejected: %v", err)
	}

	closed := *generalEnvelope
	closed.ReceiverRank = home.RankGeneral
	if err := home.ValidateEnvelope(&closed); err == nil || !strings.Contains(err.Error(), "general can only send to captain or soldier") {
		t.Fatalf("closed General-to-General transition result = %v", err)
	}
}

type harnessEndpoint struct{}

func (*harnessEndpoint) Alive(string, map[string]string) (bool, error) { return true, nil }
func (*harnessEndpoint) Busy(string, map[string]string) (bool, error)  { return false, nil }
func (*harnessEndpoint) Send(string, map[string]string, string) home.BoundSendResult {
	return home.BoundSendResult{Acknowledged: true, Status: "submitted"}
}
