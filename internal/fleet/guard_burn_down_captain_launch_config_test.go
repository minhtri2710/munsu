package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// quoteEscapedLeaf appends U+200B ZERO WIDTH SPACE to a directory leaf, so
// every path built from it contains a rune strconv.Quote renders as an escape.
//
// unicode.IsPrint reports false for U+200B (category Cf), and that is the
// predicate strconv.Quote consults: it emits the six characters \u200b in place
// of the rune. No filesystem rule stands in the way — MS-FSCC gives the Windows
// filename exclusions as " \ / : | < > ? * and U+0000-U+001F, and U+200B is in
// none of them — so unlike a literal backslash this leaf needs no platform
// branch to be both creatable and escape-bearing.
//
// The two call sites below assert that a refusal message renders its home path
// raw. That assertion has two clauses — the raw path is present, and the
// strconv.Quote form is absent — and only an escape-bearing path makes a %q
// regression fail the first clause as well as the second. A plain leaf would
// silently reduce the test to the second clause alone.
//
// Creatability under U+200B is verified here on darwin only. Whether the
// windows-latest image accepts it is pinned by the windows-observation run on
// merged main, not by anything this tree executes.
func quoteEscapedLeaf(name string) string {
	return name + "\u200b"
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
	captainHome := filepath.Join(parentHome, "captains", quoteEscapedLeaf("mismatch"))
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "mismatch"); err != nil {
		t.Fatal(err)
	}
	registeredHome := filepath.Join(t.TempDir(), quoteEscapedLeaf("registered"))
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
