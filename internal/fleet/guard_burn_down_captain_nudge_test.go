package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func writeGuardNudgeMeta(t *testing.T, parentHome, captainHome, id string, mutate func(map[string]string)) {
	t.Helper()
	canonical, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		"kind":   "captain",
		"sm_id":  id,
		"home":   canonical,
		"window": "captain-window",
	}
	mutate(meta)
	if err := home.WriteMeta(parentHome, taskIDForCaptain(id), meta); err != nil {
		t.Fatal(err)
	}
}

func TestGuardBurnDownSendNudgeRefusesWrongMetaKind(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", func(meta map[string]string) {
		meta["kind"] = "ship"
	})

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), `meta kind="ship", expected captain`) {
		t.Fatalf("sendNudge error = %v, want wrong-kind refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesWrongMetaID(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", func(meta map[string]string) {
		meta["sm_id"] = "other"
	})

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), `meta sm_id="other" does not match`) {
		t.Fatalf("sendNudge error = %v, want wrong-id refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesMetaHomeMismatch(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", func(meta map[string]string) {
		meta["home"] = t.TempDir()
	})

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "meta home=") || !strings.Contains(err.Error(), "does not match canonical home") {
		t.Fatalf("sendNudge error = %v, want home-mismatch refusal", err)
	}
}
