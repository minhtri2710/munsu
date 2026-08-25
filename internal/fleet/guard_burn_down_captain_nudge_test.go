package fleet

import (
	"os"
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
	if mutate != nil {
		mutate(meta)
	}
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

func TestGuardBurnDownSendNudgeRefusesEmptyWindow(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", func(meta map[string]string) {
		meta["window"] = ""
	})

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "no window in meta") {
		t.Fatalf("sendNudge error = %v, want empty-window refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesMarkerIDMismatch(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", nil)
	if err := writeNudgeMarker(parentHome, "captain", captainHome, "commit", "digest", "message"); err != nil {
		t.Fatal(err)
	}
	markerPath := nudgeMarkerPath(parentHome, "captain")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	marker = []byte(strings.Replace(string(marker), "id=captain", "id=other", 1))
	if err := os.WriteFile(markerPath, marker, 0644); err != nil {
		t.Fatal(err)
	}

	err = sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "does not match registry id") {
		t.Fatalf("sendNudge error = %v, want marker-id refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesEmptyMarkerCommit(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", nil)
	if err := writeNudgeMarker(parentHome, "captain", captainHome, "", "digest", "message"); err != nil {
		t.Fatal(err)
	}

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "marker has empty commit") {
		t.Fatalf("sendNudge error = %v, want empty-commit refusal", err)
	}
}
