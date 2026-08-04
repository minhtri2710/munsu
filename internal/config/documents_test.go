package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateBaseRejectsIndependentSchemaVersions(t *testing.T) {
	base := validBase()
	base.SchemaVersion = "future"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("Validate() error = %v, want schemaVersion refusal", err)
	}
}

func TestResolveProjectConfigDistinctProjectsAndCaptainFallback(t *testing.T) {
	base := validBase()
	alpha, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{SoldierHarness: "claude", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}, CaptainProfile{Harness: "pi"}), BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveProject(base, validFacts("beta", "/beta", "direct-pr", ProjectOverlay{SoldierHarness: "codex"}, CaptainProfile{}), BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if alpha.SoldierHarness != "claude" || beta.SoldierHarness != "codex" {
		t.Fatalf("resolved harnesses = %q/%q", alpha.SoldierHarness, beta.SoldierHarness)
	}
	if alpha.DefaultMode != "direct-pr" || beta.DefaultMode != "direct-pr" {
		t.Fatalf("project mode alias not resolved: %q/%q", alpha.DefaultMode, beta.DefaultMode)
	}
	if alpha.CaptainProfile.Harness != "pi" || alpha.CaptainProfile.Model != "base-model" {
		t.Fatalf("captain fallback = %+v", alpha.CaptainProfile)
	}
	if len(alpha.DispatchProfiles) != 1 || alpha.DispatchProfiles[0].Name != "alpha" || len(beta.DispatchProfiles) != 1 || beta.DispatchProfiles[0].Name != "base" {
		t.Fatalf("dispatch profiles alpha=%+v beta=%+v", alpha.DispatchProfiles, beta.DispatchProfiles)
	}
}

func TestResolveProjectOverlayDefaultModeOverridesProjectModeAlias(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DefaultMode: "no-mistakes"}, CaptainProfile{})
	resolved, err := ResolveProject(base, facts, BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DefaultMode != "no-mistakes" {
		t.Fatalf("DefaultMode = %q, want overlay value", resolved.DefaultMode)
	}
}

func TestResolveProjectConfigBoundaryOverridesAndImmutability(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}, CaptainProfile{})
	before := facts.Overlay.DispatchProfiles[0].Harness
	resolved, err := ResolveProject(base, facts, BoundaryOverrides{Model: "env-model", DefaultMode: "direct-pr"})
	if err != nil {
		t.Fatal(err)
	}
	resolved.DispatchProfiles[0].Harness = "changed"
	if facts.Overlay.DispatchProfiles[0].Harness != before {
		t.Fatal("resolver mutated or shared dispatch profile storage")
	}
	if resolved.Model != "env-model" || resolved.DefaultMode != "direct-pr" {
		t.Fatalf("boundary overrides not applied: %+v", resolved)
	}
}

func TestProjectDigestIsDeterministicAndTargeted(t *testing.T) {
	base := validBase()
	alpha := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{}, CaptainProfile{})
	beta := validFacts("beta", "/beta", "direct-pr", ProjectOverlay{}, CaptainProfile{})
	a1, _ := ProjectDigest(base, alpha)
	a2, _ := ProjectDigest(base, alpha)
	b1, _ := ProjectDigest(base, beta)
	if a1 != a2 {
		t.Fatalf("digest is not deterministic: %s != %s", a1, a2)
	}
	alpha.Overlay.Model = "changed"
	a3, _ := ProjectDigest(base, alpha)
	b2, _ := ProjectDigest(base, beta)
	if a1 == a3 {
		t.Fatal("alpha digest did not change")
	}
	if b1 != b2 {
		t.Fatal("beta digest changed for alpha-only overlay")
	}
	alpha.Overlay.Model = ""
	base.Config.Model = "new-base"
	a4, _ := ProjectDigest(base, alpha)
	b3, _ := ProjectDigest(base, beta)
	if a1 == a4 || b1 == b3 {
		t.Fatal("base change must change every project digest")
	}
	withProfile := alpha
	withProfile.CaptainProfile.Model = "captain-only"
	resolvedCaptain, _ := ResolveProject(base, withProfile, BoundaryOverrides{})
	resolvedBoundary, _ := ResolveProject(base, alpha, BoundaryOverrides{Model: "boundary-only"})
	if resolvedCaptain.Digest != a4 || resolvedBoundary.Digest != a4 {
		t.Fatal("Captain profile or boundary override entered project digest")
	}
	withOverlay := alpha
	withOverlay.Overlay.DefaultMode = "no-mistakes"
	withOverlayDigest, _ := ProjectDigest(base, withOverlay)
	withMode := withOverlay
	withMode.Mode = "direct-pr"
	withModeDigest, _ := ProjectDigest(base, withMode)
	if withOverlayDigest != withModeDigest {
		t.Fatal("project mode overrode explicit overlay DefaultMode in digest")
	}
}

func TestResolvedSnapshotIsFrozenAndReturnsDeepCopies(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude", Match: []string{"alpha"}, Use: []DispatchCandidate{{Harness: "claude"}}}}}, CaptainProfile{})
	snapshot, err := NewResolvedSnapshot(base, facts, BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	first := snapshot.Config()
	first.DispatchProfiles[0].Match[0] = "changed"
	first.DispatchProfiles[0].Use[0].Harness = "changed"
	if snapshot.Config().DispatchProfiles[0].Match[0] != "alpha" || snapshot.Config().DispatchProfiles[0].Use[0].Harness != "claude" {
		t.Fatal("snapshot accessor shares nested mutable storage")
	}
	facts.Overlay.Model = "new-on-disk"
	if snapshot.Config().Model == "new-on-disk" {
		t.Fatal("existing snapshot observed later facts mutation")
	}
	newSnapshot, err := NewResolvedSnapshot(base, facts, BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if newSnapshot.Config().Model != "new-on-disk" {
		t.Fatal("new snapshot did not observe facts mutation")
	}
}

func TestResolveProjectDoesNotReadEnvironment(t *testing.T) {
	t.Setenv("MUNSU_MODEL_OVERRIDE", "environment-model")
	base := validBase()
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", "", ProjectOverlay{}, CaptainProfile{}), BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "base-model" {
		t.Fatalf("resolver read process environment: %q", resolved.Model)
	}
	overridden, err := ResolveProject(base, validFacts("alpha", "/alpha", "", ProjectOverlay{}, CaptainProfile{}), BoundaryOverrides{Model: "typed-boundary"})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Model != "typed-boundary" {
		t.Fatalf("typed override = %q", overridden.Model)
	}
}

func TestFleetBaseRoundTripAndStrictDecode(t *testing.T) {
	home := t.TempDir()
	base := validBase()
	if err := StoreFleetBase(home, base); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFleetBase(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("round trip mismatch\nbase=%+v\ngot=%+v", got, base)
	}
	path := filepath.Join(home, BaseDocumentPath)
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "{", "{\"unknown\":true,", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFleetBase(home); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadFleetBase() error = %v, want strict decode refusal", err)
	}
}

func TestPublishedSnapshotRoundTripAndStrictValidation(t *testing.T) {
	home := t.TempDir()
	base := validBase()
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{}, CaptainProfile{}), BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := StorePublishedSnapshot(home, resolved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPublishedSnapshot(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Config(), resolved) {
		t.Fatalf("published snapshot mismatch\nwant=%+v\ngot=%+v", resolved, loaded.Config())
	}

	path := filepath.Join(home, PublishedSnapshotPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), PublishedSnapshotSchemaVersion, "future", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublishedSnapshot(home); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("LoadPublishedSnapshot() error = %v, want schemaVersion refusal", err)
	}
}

func validBase() FleetBaseDocument {
	return FleetBaseDocument{
		SchemaVersion: FleetBaseSchemaVersion,
		Config: ProjectOverlay{
			SoldierHarness: "pi",
			Model:          "base-model",
			DefaultMode:    "no-mistakes",
			DispatchProfiles: []DispatchProfile{
				{Name: "base", Harness: "pi"},
			},
		},
		CaptainProfile: CaptainProfile{Harness: "pi", Model: "base-model"},
	}
}

func validFacts(name, path, mode string, overlay ProjectOverlay, captainProfile CaptainProfile) ProjectFacts {
	return ProjectFacts{
		Name:           name,
		Path:           path,
		Mode:           mode,
		Overlay:        overlay,
		CaptainProfile: captainProfile,
	}
}
