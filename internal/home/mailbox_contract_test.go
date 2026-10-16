package home

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These rows moved from internal/orchestrator (audit-b D17): each kills a
// mailbox guard the other home tests do not enter.

func contractReceiver(t *testing.T, homeDir, identity string, rank Rank) *Receiver {
	t.Helper()
	if rank == RankCaptain {
		if err := SeedCaptainProvenance(homeDir, identity); err != nil {
			t.Fatalf("SeedCaptainProvenance: %v", err)
		}
	}
	recv, err := NewReceiver(homeDir, seatsOf(homeDir))
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return recv
}

func TestParseNotificationRefRefusesEmptyFields(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{`{"message_id":"","sender_identity":"s1"}`, "message ID"},
		{`{"message_id":"m1","sender_identity":""}`, "sender identity"},
	} {
		if _, err := ParseNotificationRef(tc.ref); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseNotificationRef(%s) = %v, want a %s error", tc.ref, err, tc.want)
		}
	}
}

// writeRawEnvelope writes env into homeDir's inbox without WriteEnvelope's
// validation, so the receiver's own check is the one under test.
func writeRawEnvelope(t *testing.T, homeDir string, env *Envelope) {
	t.Helper()
	inboxDir := filepath.Join(homeDir, "state", InboxDir, env.SenderIdentity)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(inboxDir, env.MessageID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func invalidTransitionEnvelope(receiverID string) *Envelope {
	return &Envelope{
		MessageID: "test-invalid-transition", SenderRank: RankGeneral, SenderIdentity: "general-main",
		ReceiverRank: RankGeneral, ReceiverID: receiverID, Payload: "hello", PayloadHash: PayloadHashHex("hello"),
		SchemaVersion: SchemaVersion, CreatedAt: time.Now().UnixNano(),
	}
}

func TestReceiverValidatesTheEnvelopeOnReceiveAndAck(t *testing.T) {
	t.Run("receive", func(t *testing.T) {
		homeDir := filepath.Join(t.TempDir(), "soldier-1")
		env := invalidTransitionEnvelope("soldier-1")
		writeRawEnvelope(t, homeDir, env)
		recv := contractReceiver(t, homeDir, "soldier-1", RankGeneral)
		_, err := recv.Receive(NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity})
		if err == nil || !strings.Contains(err.Error(), "validate envelope") {
			t.Fatalf("Receive = %v, want a validate envelope error", err)
		}
	})
	t.Run("ack", func(t *testing.T) {
		homeDir := filepath.Join(t.TempDir(), "soldier-1")
		env := invalidTransitionEnvelope("soldier-1")
		writeRawEnvelope(t, homeDir, env)
		recv := contractReceiver(t, homeDir, env.ReceiverID, env.ReceiverRank)
		_, err := recv.Ack(NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity})
		want := "ack validate envelope: envelope: general can only send to captain or soldier"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Ack = %v, want %q", err, want)
		}
	})
}

func TestReceiverRefusesATamperedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(path string, data []byte) []byte
	}{
		{"payload changed", func(_ string, data []byte) []byte {
			return []byte(strings.Replace(string(data), "original content", "tampered content", 1))
		}},
		{"hash field changed", func(_ string, data []byte) []byte {
			var raw map[string]any
			json.Unmarshal(data, &raw)
			raw["payload_hash"] = strings.Repeat("0", 64)
			out, _ := json.MarshalIndent(raw, "", "  ")
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			env := &Envelope{SenderRank: RankGeneral, SenderIdentity: "general-1", ReceiverRank: RankCaptain, ReceiverID: "captain-1", Payload: "original content"}
			if err := NewStore(homeDir).WriteEnvelope(env); err != nil {
				t.Fatalf("WriteEnvelope: %v", err)
			}
			path := filepath.Join(homeDir, "state", InboxDir, "general-1", env.MessageID+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.tamper(path, data), 0o644); err != nil {
				t.Fatal(err)
			}
			recv := contractReceiver(t, homeDir, "captain-1", RankCaptain)
			_, err = recv.Receive(NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-1"})
			if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
				t.Fatalf("Receive = %v, want a hash mismatch error", err)
			}
		})
	}
}

func TestStoreWriteAckRefusesAConflictingOutcome(t *testing.T) {
	store := NewStore(t.TempDir())
	ack := func(outcome string) *ProcessingAck {
		return &ProcessingAck{
			MessageID: "msg-conflict", SenderRank: RankSoldier, SenderIdentity: "soldier-1",
			ReceiverRank: RankCaptain, ReceiverID: "captain-1", PayloadHash: PayloadHashHex("first"),
			ProcessedAt: time.Now().UnixNano(), Outcome: outcome,
		}
	}
	if err := store.WriteAck(ack("done")); err != nil {
		t.Fatalf("first WriteAck: %v", err)
	}
	if err := store.WriteAck(ack("failed")); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("second WriteAck = %v, want a conflict error", err)
	}
}
