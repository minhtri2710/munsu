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
// U+200B is Unicode category Cf, and Go's printability predicate reports false
// for it: strconv.Quote consults strconv.IsPrint, whose own doc defines it
// identically to unicode.IsPrint, so Quote emits the six characters \u200b in
// place of the rune. The documented MS-FSCC general filename exclusions
// (" \ / : | < > ? * and U+0000-U+001F) do not list U+200B, so unlike a literal
// backslash this leaf needs no platform branch to be written as well as escaped.
// Acceptance on the target windows image is a windows-observation question, not
// something the specification settles.
//
// The two call sites below assert that a refusal message renders its home path
// raw, in two clauses: the raw path is present, and the strconv.Quote form is
// absent. What the leaf buys is that a %q regression fails the first clause as
// well as the second on both platforms. A plain leaf already does that on
// windows, where %q doubles the native \ separators; on unix, where Quote passes
// / through untouched, a plain leaf leaves only the second clause. U+200B
// removes the dependence on separator behaviour rather than supplying a
// property windows lacked.
//
// Creatability under U+200B is verified here on darwin only. Whether the
// windows-latest image accepts it through MkdirAll and EvalSymlinks is pinned by
// the windows-observation run on merged main, not by anything this tree executes.
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

	err := Launch(captainHome, parentHome, nil, fakeIntegrationPort{})
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
