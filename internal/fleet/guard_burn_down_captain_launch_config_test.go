package fleet

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// backslashBearingLeaf returns a directory leaf that puts a backslash — the one
// separator strconv.Quote escapes — into any path built from it, so a %q
// regression breaks literal containment and not only the quote check.
//
// windows already supplies that backslash as its path separator and rejects a
// literal one inside a filename (a drive-letter "C:" is not a legal component),
// so there the leaf stays plain; unix separates with '/', which Quote passes
// through untouched, so on unix the leaf carries the backslashes itself.
func backslashBearingLeaf(name string) string {
	if runtime.GOOS == "windows" {
		return name
	}
	return `C:\Users\alice\` + name
}

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
	captainHome := filepath.Join(parentHome, "captains", backslashBearingLeaf("mismatch"))
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "mismatch"); err != nil {
		t.Fatal(err)
	}
	registeredHome := filepath.Join(t.TempDir(), backslashBearingLeaf("registered"))
	if err := os.MkdirAll(registeredHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parentHome, "mismatch", registeredHome, "", ""); err != nil {
		t.Fatal(err)
	}

	err := publishResolvedSnapshot(parentHome, captainHome)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("publishResolvedSnapshot error = %v, want registered-home mismatch refusal", err)
	}
	canonicalCaptain, canonicalErr := canonicalCaptainHome(captainHome)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	canonicalRegistered, canonicalErr := canonicalCaptainHome(registeredHome)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	for _, path := range []string{canonicalRegistered, canonicalCaptain} {
		if !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), strconv.Quote(path)) {
			t.Errorf("publishResolvedSnapshot home path rendering for %q = %q", path, err)
		}
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
