package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownLaunchRefusesNilEndpoint(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parentHome := t.TempDir()
	captainHome := seedCaptainForTest(t, parentHome, "launch-endpoint")
	writeCanonicalPiIntegration(t, captainHome)

	err := Launch(captainHome, parentHome, nil)
	if err == nil || err.Error() != "captain launch endpoint capability is required" {
		t.Fatalf("Launch error = %v, want nil-endpoint refusal", err)
	}
}

func TestGuardBurnDownPreflightConfigPushRefusesParentHomeEscape(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(captainHome, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parentHome, filepath.Join(captainHome, "config", "parent-home")); err != nil {
		t.Fatal(err)
	}

	err := preflightConfigPushDestinations(parentHome, captainHome)
	if err == nil || !strings.Contains(err.Error(), "parent-home config destination escapes captain container") {
		t.Fatalf("preflightConfigPushDestinations error = %v, want parent-home escape refusal", err)
	}
}
