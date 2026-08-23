//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestIssue593ReproductionHarness records the direct general downlink path and
// the counterfactual envelope path without changing the sender implementation.
func TestIssue593ReproductionHarness(t *testing.T) {
	generalHome := filepath.Join(t.TempDir(), "general-home")
	if err := os.MkdirAll(generalHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(generalHome, "task-soldier", map[string]string{"window": "soldier-window"}); err != nil {
		t.Fatal(err)
	}

	result := SendToSoldier(generalHome, "task-soldier", filepath.Base(generalHome), "do work", &harnessEndpoint{})
	if result.Err != nil || !result.Sent {
		t.Fatalf("direct general dispatch failed unexpectedly: sent=%v err=%v", result.Sent, result.Err)
	}

	envelopes, err := home.NewStore(generalHome).ListInbox(filepath.Base(generalHome))
	if err != nil || len(envelopes) != 1 {
		t.Fatalf("reading published envelope: count=%d err=%v", len(envelopes), err)
	}
	if envelopes[0].SenderRank != home.RankCaptain {
		t.Fatalf("published sender rank = %q, want captain", envelopes[0].SenderRank)
	}
	receiver, err := home.NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	_, err = receiver.Receive(home.NotificationRef{MessageID: envelopes[0].MessageID, SenderIdentity: filepath.Base(generalHome)})
	if err == nil || !strings.Contains(err.Error(), "receiver identity mismatch") {
		t.Fatalf("receiver result = %v, want durable receiver identity refusal", err)
	}

	correct := &home.Envelope{
		SenderRank:     home.RankGeneral,
		SenderIdentity: filepath.Base(generalHome),
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     "task-soldier",
		TaskID:         "task-soldier",
		Payload:        "do work",
	}
	correct.MessageID, _ = home.NewMessageID()
	correct.PayloadHash = home.PayloadHashHex(correct.Payload)
	if err := home.ValidateEnvelope(correct); err == nil {
		t.Fatal("expected the correct general-to-soldier envelope to be refused")
	} else if !strings.Contains(err.Error(), "general can only send to captain") {
		t.Fatalf("unexpected counterfactual validation error: %v", err)
	}

	ack := &home.ProcessingAck{
		MessageID:      correct.MessageID,
		SenderRank:     home.RankGeneral,
		SenderIdentity: correct.SenderIdentity,
		ReceiverRank:   correct.ReceiverRank,
		ReceiverID:     correct.ReceiverID,
		TaskID:         correct.TaskID,
		PayloadHash:    correct.PayloadHash,
	}
	if err := home.ValidateAck(correct, ack); err != nil {
		t.Fatalf("matching general sender rank should pass field binding: %v", err)
	}
}

type harnessEndpoint struct{}

func (*harnessEndpoint) Alive(string, map[string]string) (bool, error) { return true, nil }
func (*harnessEndpoint) Busy(string, map[string]string) (bool, error)  { return false, nil }
func (*harnessEndpoint) Send(string, map[string]string, string) home.BoundSendResult {
	return home.BoundSendResult{Acknowledged: true, Status: "submitted"}
}
