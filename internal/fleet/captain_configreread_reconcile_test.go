package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestReconcileConfigRereadPendingRemovesOnlyAckedStaleGenerations(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := seedCaptainForTest(t, parent, "reread-reconcile")
	const digest = "config-digest"
	if err := WriteConfigRereadGen(captainHome, 2, digest); err != nil {
		t.Fatal(err)
	}

	senderID, senderRank, err := home.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}
	captainID, err := home.ValidateCaptainProvenance(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	makeEnvelope := func(generation int, generationDigest string) *home.Envelope {
		id := ConfigRereadEnvelopeID(senderID, captainID, generation, generationDigest)
		return &home.Envelope{
			MessageID: id, SenderRank: senderRank, SenderIdentity: senderID,
			ReceiverRank: home.RankCaptain, ReceiverID: captainID,
			TaskID: taskIDForCaptain(captainID), Key: ConfigRereadKey,
			Payload: ConfigRereadMessage(generation, generationDigest),
		}
	}
	stale := makeEnvelope(1, "old-digest")
	latest := makeEnvelope(2, digest)
	senderStore := home.NewStore(parent)
	for _, envelope := range []*home.Envelope{stale, latest} {
		if err := senderStore.WritePending(envelope); err != nil {
			t.Fatalf("WritePending(%s): %v", envelope.MessageID, err)
		}
	}
	ack := &home.ProcessingAck{
		MessageID: latest.MessageID, SenderIdentity: senderID, ReceiverID: captainID,
		ProcessedAt: 1, Outcome: home.OutcomeAccepted,
	}
	if err := home.NewStore(captainHome).WriteAck(ack); err != nil {
		t.Fatalf("WriteAck latest: %v", err)
	}

	if err := ReconcileConfigRereadPending(parent, captainHome); err != nil {
		t.Fatalf("ReconcileConfigRereadPending: %v", err)
	}
	if pending, err := senderStore.ReadPending(senderID, stale.MessageID); err != nil || pending != nil {
		t.Fatalf("stale pending = (%+v, %v), want removed", pending, err)
	}
	if pending, err := senderStore.ReadPending(senderID, latest.MessageID); err != nil || pending == nil {
		t.Fatalf("latest pending = (%+v, %v), want retained", pending, err)
	}
}
