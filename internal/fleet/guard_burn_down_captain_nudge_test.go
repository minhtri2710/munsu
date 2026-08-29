package fleet

import (
	"os"
	"path/filepath"
	"strconv"
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

	endpoint := &testNudgeEndpoint{}
	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, endpoint)
	if err == nil || !strings.Contains(err.Error(), `meta kind="ship", expected captain`) {
		t.Fatalf("sendNudge error = %v, want wrong-kind refusal", err)
	}
	if endpoint.calls != 0 {
		t.Fatalf("nudge endpoint calls = %d, want refusal before send", endpoint.calls)
	}
	t.Logf("nudge refused before endpoint side effects: %v", err)
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
	mismatchedHome := t.TempDir()
	writeGuardNudgeMeta(t, parentHome, captainHome, "captain", func(meta map[string]string) {
		meta["home"] = mismatchedHome
	})

	err := sendNudge(parentHome, Info{ID: "captain", Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "meta home=") || !strings.Contains(err.Error(), "does not match canonical home") {
		t.Fatalf("sendNudge error = %v, want home-mismatch refusal", err)
	}
	for _, path := range []string{mismatchedHome, captainHome} {
		if !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), strconv.Quote(path)) {
			t.Errorf("sendNudge home path rendering for %q = %q", path, err)
		}
	}
}

func TestGuardBurnDownSendNudgeRefusesMarkerHomeMismatch(t *testing.T) {
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	mismatchedHome := t.TempDir()
	markerPath := nudgeMarkerPath(parentHome, id)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "home="+captainHome, "home="+mismatchedHome, 1))
	if err := os.WriteFile(markerPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	err = sendNudge(parentHome, Info{ID: id, Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "marker home=") || !strings.Contains(err.Error(), "does not match canonical home") {
		t.Fatalf("sendNudge error = %v, want marker-home mismatch refusal", err)
	}
	for _, path := range []string{mismatchedHome, captainHome} {
		if !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), strconv.Quote(path)) {
			t.Errorf("sendNudge marker home path rendering for %q = %q", path, err)
		}
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

func newGuardNudgeValidFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	guardGitTestRun(t, captainHome, "init", "-b", "main")
	guardGitTestRun(t, captainHome, "config", "user.name", "Munsu Test")
	guardGitTestRun(t, captainHome, "config", "user.email", "munsu@example.invalid")
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# captain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	guardGitTestRun(t, captainHome, "add", "AGENTS.md")
	guardGitTestRun(t, captainHome, "commit", "-m", "initial instructions")
	commit := guardGitTestRun(t, captainHome, "rev-parse", "HEAD")
	digest, err := instructionSurfaceDigest(captainHome, commit)
	if err != nil {
		t.Fatal(err)
	}
	id := "captain"
	writeGuardNudgeMeta(t, parentHome, captainHome, id, nil)
	message := "instruction surface changed in " + commit[:8]
	if err := writeNudgeMarker(parentHome, id, captainHome, commit, digest, message); err != nil {
		t.Fatal(err)
	}
	return parentHome, captainHome, id, digest
}

func TestGuardBurnDownSendNudgeRefusesInstructionDigestMismatch(t *testing.T) {
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	markerPath := nudgeMarkerPath(parentHome, id)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "instructions=", "instructions=wrong-", 1))
	if err := os.WriteFile(markerPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	err = sendNudge(parentHome, Info{ID: id, Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "marker instruction digest does not match commit") {
		t.Fatalf("sendNudge error = %v, want instruction-digest refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesMessageMismatch(t *testing.T) {
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	markerPath := nudgeMarkerPath(parentHome, id)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "message=instruction surface changed", "message=wrong message", 1))
	if err := os.WriteFile(markerPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	err = sendNudge(parentHome, Info{ID: id, Home: captainHome}, &testNudgeEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "marker message") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("sendNudge error = %v, want message-mismatch refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesNilEndpoint(t *testing.T) {
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	err := sendNudge(parentHome, Info{ID: id, Home: captainHome}, nil)
	if err == nil || err.Error() != "captain nudge endpoint capability is required" {
		t.Fatalf("sendNudge error = %v, want nil-endpoint refusal", err)
	}
}

func TestGuardBurnDownSendNudgeRefusesUnacknowledgedResult(t *testing.T) {
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	endpoint := &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: false}}
	err := sendNudge(parentHome, Info{ID: id, Home: captainHome}, endpoint)
	if err == nil || !strings.Contains(err.Error(), "send not acknowledged (status=submitted)") {
		t.Fatalf("sendNudge error = %v, want unacknowledged refusal", err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("nudge endpoint calls = %d, want 1", endpoint.calls)
	}
}
