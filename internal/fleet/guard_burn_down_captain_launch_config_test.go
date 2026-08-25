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
	t.Logf("launch refused before endpoint side effects: %v", err)
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

func TestGuardBurnDownPublishResolvedSnapshotRefusesRegisteredHomeMismatch(t *testing.T) {
	parentHome := t.TempDir()
	setupTypedParentHome(t, parentHome, "mismatch")
	captainHome := filepath.Join(parentHome, "captains", "mismatch")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "mismatch"); err != nil {
		t.Fatal(err)
	}
	registeredHome := t.TempDir()
	if err := Register(parentHome, "mismatch", registeredHome, "", ""); err != nil {
		t.Fatal(err)
	}

	err := publishResolvedSnapshot(parentHome, captainHome)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("publishResolvedSnapshot error = %v, want registered-home mismatch refusal", err)
	}
}

func TestGuardBurnDownPublishResolvedSnapshotRefusesUnboundCaptain(t *testing.T) {
	parentHome := t.TempDir()
	setupTypedParentHome(t, parentHome, "unbound")
	captainHome := filepath.Join(parentHome, "captains", "unbound")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "unbound"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parentHome, "unbound", captainHome, "", ""); err != nil {
		t.Fatal(err)
	}

	err := publishResolvedSnapshot(parentHome, captainHome)
	if err == nil || !strings.Contains(err.Error(), "is not bound to a project in the Fleet registry") {
		t.Fatalf("publishResolvedSnapshot error = %v, want unbound-captain refusal", err)
	}
}
