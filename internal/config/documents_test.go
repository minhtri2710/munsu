package config

import (
	"encoding/json"
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

func TestResolveProjectConfigDistinctProjectsAndCaptainProfileFromBase(t *testing.T) {
	base := validBase()
	alpha, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{SoldierHarness: "claude", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveProject(base, validFacts("beta", "/beta", "direct-pr", ProjectOverlay{SoldierHarness: "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	if alpha.SoldierHarness != "claude" || beta.SoldierHarness != "codex" {
		t.Fatalf("resolved harnesses = %q/%q", alpha.SoldierHarness, beta.SoldierHarness)
	}
	if alpha.DefaultMode != "direct-pr" || beta.DefaultMode != "direct-pr" {
		t.Fatalf("project mode alias not resolved: %q/%q", alpha.DefaultMode, beta.DefaultMode)
	}
	// The resolved Captain profile is the fleet-default base.CaptainProfile
	// for every project; there is no per-project override layer.
	if alpha.CaptainProfile.Harness != "pi" || alpha.CaptainProfile.Model != "base-model" {
		t.Fatalf("captain profile from base = %+v", alpha.CaptainProfile)
	}
	if beta.CaptainProfile != alpha.CaptainProfile {
		t.Fatalf("captain profile differs across projects: %+v vs %+v", alpha.CaptainProfile, beta.CaptainProfile)
	}
	if len(alpha.DispatchProfiles) != 1 || alpha.DispatchProfiles[0].Name != "alpha" || len(beta.DispatchProfiles) != 1 || beta.DispatchProfiles[0].Name != "base" {
		t.Fatalf("dispatch profiles alpha=%+v beta=%+v", alpha.DispatchProfiles, beta.DispatchProfiles)
	}
}

func TestResolveProjectOverlayDefaultModeOverridesProjectModeAlias(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DefaultMode: "no-mistakes"})
	resolved, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DefaultMode != "no-mistakes" {
		t.Fatalf("DefaultMode = %q, want overlay value", resolved.DefaultMode)
	}
}

func TestResolveProjectConfigOverlayAppliesAndResolverIsImmutable(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Model: "overlay-model", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}})
	before := facts.Overlay.DispatchProfiles[0].Harness
	resolved, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	resolved.DispatchProfiles[0].Harness = "changed"
	if facts.Overlay.DispatchProfiles[0].Harness != before {
		t.Fatal("resolver mutated or shared dispatch profile storage")
	}
	if resolved.Model != "overlay-model" || resolved.DefaultMode != "direct-pr" {
		t.Fatalf("overlay values not applied: %+v", resolved)
	}
}

func TestProjectDigestIsDeterministicAndTargeted(t *testing.T) {
	base := validBase()
	alpha := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{})
	beta := validFacts("beta", "/beta", "direct-pr", ProjectOverlay{})
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
	captainBase := base
	captainBase.CaptainProfile.Model = "captain-only"
	captainDigest, _ := ProjectDigest(captainBase, alpha)
	if captainDigest != a4 {
		t.Fatal("Captain profile entered project digest")
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

func TestProjectDigestCoversFinalResolvedBackend(t *testing.T) {
	base := validBase() // Backend: "tmux" fleet default
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{})
	baseDigest, _ := ProjectDigest(base, facts)

	// A project overlay Backend that resolves to the same final value as the
	// base Backend must not change the digest (identical final config).
	sameFinal, _ := ProjectDigest(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Backend: "tmux"}))
	if baseDigest != sameFinal {
		t.Fatal("identical final resolved Backend produced a different digest")
	}
	// An overlay Backend changing the final value must change the digest.
	overlayBackend, _ := ProjectDigest(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Backend: "herdr"}))
	if baseDigest == overlayBackend {
		t.Fatal("overlay Backend change did not change the digest")
	}
	// An overlay change to another operation setting must also be bound.
	overlayMode, _ := ProjectDigest(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DefaultMode: "local-only"}))
	if baseDigest == overlayMode {
		t.Fatal("overlay DefaultMode change did not change the digest")
	}
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Backend: "herdr"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Backend != "herdr" {
		t.Fatalf("Backend = %q, want herdr", resolved.Backend)
	}
	if resolved.Digest != overlayBackend {
		t.Fatalf("resolved digest %s does not match canonical digest payload %s", resolved.Digest, overlayBackend)
	}
}

func TestResolveProjectBackendPrecedenceAndRequired(t *testing.T) {
	base := validBase() // Backend: "tmux" fleet default
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{})

	baseOnly, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	if baseOnly.Backend != "tmux" {
		t.Fatalf("Backend = %q, want base default", baseOnly.Backend)
	}

	project, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Backend: "herdr"}))
	if err != nil {
		t.Fatal(err)
	}
	if project.Backend != "herdr" {
		t.Fatalf("project overlay Backend = %q, want herdr", project.Backend)
	}

	// No Backend anywhere after resolution is a typed validation failure,
	// never auto-detection or an env/PATH default.
	noBackendBase := FleetBaseDocument{
		SchemaVersion: FleetBaseSchemaVersion,
		Config:        ProjectOverlay{SoldierHarness: "pi"},
	}
	if _, err := ResolveProject(noBackendBase, facts); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("resolving with no Backend identity = %v, want typed validation failure", err)
	}
}

func TestResolvedSnapshotIsFrozenAndReturnsDeepCopies(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude", Match: []string{"alpha"}, Use: []DispatchCandidate{{Harness: "claude"}}}}})
	snapshot, err := NewResolvedSnapshot(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	first := snapshot.Config()
	first.DispatchProfiles[0].Match[0] = "changed"
	first.DispatchProfiles[0].Use[0].Harness = "changed"
	if snapshot.Config().DispatchProfiles[0].Match[0] != "alpha" || snapshot.Config().DispatchProfiles[0].Use[0].Harness != "claude" {
		t.Fatal("snapshot accessor shares nested mutable storage")
	}
	if snapshot.Config().Backend != "tmux" {
		t.Fatalf("frozen snapshot Backend = %q, want tmux", snapshot.Config().Backend)
	}
	facts.Overlay.Model = "new-on-disk"
	facts.Overlay.Backend = "herdr"
	if snapshot.Config().Model == "new-on-disk" || snapshot.Config().Backend == "herdr" {
		t.Fatal("existing snapshot observed later facts mutation")
	}
	newSnapshot, err := NewResolvedSnapshot(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	if newSnapshot.Config().Model != "new-on-disk" || newSnapshot.Config().Backend != "herdr" {
		t.Fatal("new snapshot did not observe facts mutation")
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
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{}))
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

func TestPublishedSnapshotStrictBackendRoundTripAndFailClosed(t *testing.T) {
	home := t.TempDir()
	base := validBase()
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", "direct-pr", ProjectOverlay{Backend: "herdr"}))
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
	if loaded.Config().Backend != "herdr" {
		t.Fatalf("published snapshot Backend = %q, want herdr", loaded.Config().Backend)
	}

	path := filepath.Join(home, PublishedSnapshotPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(root["config"], &cfg); err != nil {
		t.Fatal(err)
	}

	// Missing Backend in a current-v1 snapshot fails closed on load.
	missing := cloneRaw(cfg)
	delete(missing, "backend")
	writeSnapshotConfig(t, path, root, missing)
	if _, err := LoadPublishedSnapshot(home); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("LoadPublishedSnapshot() error = %v, want backend refusal", err)
	}

	// Explicitly empty Backend is malformed and also fails closed.
	empty := cloneRaw(cfg)
	empty["backend"] = json.RawMessage(`""`)
	writeSnapshotConfig(t, path, root, empty)
	if _, err := LoadPublishedSnapshot(home); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("LoadPublishedSnapshot() error = %v, want backend refusal", err)
	}

	// Unknown but syntactically valid identities still deserialize: Config
	// validates shape only; the runtime capability decision (internal/backend)
	// owns the supported-identity bound and fails closed there.
	unknown := cloneRaw(cfg)
	unknown["backend"] = json.RawMessage(`"docker"`)
	writeSnapshotConfig(t, path, root, unknown)
	loadedUnknown, err := LoadPublishedSnapshot(home)
	if err != nil {
		t.Fatalf("LoadPublishedSnapshot() with unknown identity = %v, want deserialization permitted", err)
	}
	if loadedUnknown.Config().Backend != "docker" {
		t.Fatalf("Backend = %q, want docker", loadedUnknown.Config().Backend)
	}
}

func TestProjectOverlayDocumentCarriesBackend(t *testing.T) {
	home := t.TempDir()
	overlay := ProjectOverlay{SoldierHarness: "pi", Backend: "herdr", DispatchProfiles: []DispatchProfile{{Name: "p", Harness: "pi"}}}
	if err := StoreProjectOverlay(home, "alpha", overlay); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectOverlay(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != "herdr" || got.SoldierHarness != "pi" || len(got.DispatchProfiles) != 1 {
		t.Fatalf("overlay document round trip = %+v", got)
	}
	// StoreProjectOverlay deep-copies the overlay: later mutation of the input
	// must not leak into the stored document.
	overlay.Backend = "mutated"
	stored, err := LoadProjectOverlay(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Backend != "herdr" {
		t.Fatal("StoreProjectOverlay did not deep-copy the overlay Backend")
	}
}

func cloneRaw(src map[string]json.RawMessage) map[string]json.RawMessage {
	dst := make(map[string]json.RawMessage, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func writeSnapshotConfig(t *testing.T, path string, root map[string]json.RawMessage, cfg map[string]json.RawMessage) {
	t.Helper()
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	root["config"] = cfgJSON
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func validBase() FleetBaseDocument {
	return FleetBaseDocument{
		SchemaVersion: FleetBaseSchemaVersion,
		Config: ProjectOverlay{
			SoldierHarness: "pi",
			Model:          "base-model",
			DefaultMode:    "no-mistakes",
			Backend:        "tmux",
			DispatchProfiles: []DispatchProfile{
				{Name: "base", Harness: "pi"},
			},
		},
		CaptainProfile: CaptainProfile{Harness: "pi", Model: "base-model"},
	}
}

func validFacts(name, path, mode string, overlay ProjectOverlay) ProjectFacts {
	return ProjectFacts{
		Name:    name,
		Path:    path,
		Mode:    mode,
		Overlay: overlay,
	}
}
