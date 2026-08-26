package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestGuardBurnDownEnsureOrHealRequirementRefusesUnexpectedEnvelopeKey(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "unexpected-key")
	senderIdentity, senderRank, err := home.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}
	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	const (
		generation = 1
		digest     = "0000000000000000000000000000000000000000000000000000000000000000"
	)
	envelopeID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, generation, digest)
	env := &home.Envelope{
		MessageID:      envelopeID,
		SenderRank:     senderRank,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankCaptain,
		ReceiverID:     captainIdentity,
		TaskID:         taskIDForCaptain(captainIdentity),
		Key:            "unexpected-key",
		Payload:        "CONFIG_REREAD: generation=1 digest=" + digest,
	}
	if err := home.NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}

	recorder := &boundSenderRecorder{actual: &fakeBoundSender{acknowledged: true}}
	_, _, _, err = ensureOrHealRequirement(parent, captainHome, generation, digest, recorder)
	if err == nil || !strings.Contains(err.Error(), "unexpected key") {
		t.Fatalf("ensureOrHealRequirement error = %v, want unexpected-key refusal", err)
	}
	if recorder.called {
		t.Fatal("unexpected-key refusal must happen before notification")
	}
}
