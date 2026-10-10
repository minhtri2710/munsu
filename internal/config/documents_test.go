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
	alpha, err := ResolveProject(base, validFacts("alpha", "/alpha", ProjectOverlay{SoldierHarness: "claude", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveProject(base, validFacts("beta", "/beta", ProjectOverlay{SoldierHarness: "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	if alpha.SoldierHarness != "claude" || beta.SoldierHarness != "codex" {
		t.Fatalf("resolved harnesses = %q/%q", alpha.SoldierHarness, beta.SoldierHarness)
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

func TestResolveProjectConfigOverlayAppliesAndResolverIsImmutable(t *testing.T) {
	base := validBase()
	base.Config.TamperCheck = "base-floor --base <base>"
	facts := validFacts("alpha", "/alpha", ProjectOverlay{Model: "overlay-model", TamperCheck: "floor --base <base>", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}})
	before := facts.Overlay.DispatchProfiles[0].Harness
	resolved, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	resolved.DispatchProfiles[0].Harness = "changed"
	if facts.Overlay.DispatchProfiles[0].Harness != before {
		t.Fatal("resolver mutated or shared dispatch profile storage")
	}
}

func TestProjectDigestIsDeterministicAndTargeted(t *testing.T) {
	base := validBase()
	alpha := validFacts("alpha", "/alpha", ProjectOverlay{})
	beta := validFacts("beta", "/beta", ProjectOverlay{})
	a1 := resolvedDigest(t, base, alpha)
	a2 := resolvedDigest(t, base, alpha)
	b1 := resolvedDigest(t, base, beta)
	if a1 != a2 {
		t.Fatalf("digest is not deterministic: %s != %s", a1, a2)
	}
	alpha.Overlay.Model = "changed"
	a3 := resolvedDigest(t, base, alpha)
	b2 := resolvedDigest(t, base, beta)
	if a1 == a3 {
		t.Fatal("alpha digest did not change")
	}
	if b1 != b2 {
		t.Fatal("beta digest changed for alpha-only overlay")
	}
	alpha.Overlay.Model = ""
	alpha.Overlay.TamperCheck = "floor --base <base>"
	a5 := resolvedDigest(t, base, alpha)
	if a1 == a5 {
		t.Fatal("tamper-check did not change the project digest")
	}
	alpha.Overlay.TamperCheck = ""
	base.Config.Model = "new-base"
	a4 := resolvedDigest(t, base, alpha)
	b3 := resolvedDigest(t, base, beta)
	if a1 == a4 || b1 == b3 {
		t.Fatal("base change must change every project digest")
	}
	captainBase := base
	captainBase.CaptainProfile.Model = "captain-only"
	captainDigest := resolvedDigest(t, captainBase, alpha)
	if captainDigest != a4 {
		t.Fatal("Captain profile entered project digest")
	}
}

func TestProjectDigestCoversFinalResolvedBackend(t *testing.T) {
	base := validBase() // Backend: "tmux" fleet default
	facts := validFacts("alpha", "/alpha", ProjectOverlay{})
	baseDigest := resolvedDigest(t, base, facts)

	// A project overlay Backend that resolves to the same final value as the
	// base Backend must not change the digest (identical final config).
	sameFinal := resolvedDigest(t, base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "tmux"}))
	if baseDigest != sameFinal {
		t.Fatal("identical final resolved Backend produced a different digest")
	}
	// An overlay Backend changing the final value must change the digest.
	overlayBackend := resolvedDigest(t, base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
	if baseDigest == overlayBackend {
		t.Fatal("overlay Backend change did not change the digest")
	}
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Backend != "herdr" {
		t.Fatalf("Backend = %q, want herdr", resolved.Backend)
	}
}

func TestResolveProjectBackendPrecedenceAndRequired(t *testing.T) {
	base := validBase() // Backend: "tmux" fleet default
	facts := validFacts("alpha", "/alpha", ProjectOverlay{})

	baseOnly, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	if baseOnly.Backend != "tmux" {
		t.Fatalf("Backend = %q, want base default", baseOnly.Backend)
	}

	project, err := ResolveProject(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
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
	facts := validFacts("alpha", "/alpha", ProjectOverlay{DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude", Match: []string{"alpha"}, Use: []DispatchCandidate{{Harness: "claude"}}}}})
	snapshot, err := NewResolvedSnapshotWithToolProbe(base, facts, nil)
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
	newSnapshot, err := NewResolvedSnapshotWithToolProbe(base, facts, nil)
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
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", ProjectOverlay{}))
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
	resolved, err := ResolveProject(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
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
			Backend:        "tmux",
			DispatchProfiles: []DispatchProfile{
				{Name: "base", Harness: "pi"},
			},
		},
		CaptainProfile: CaptainProfile{Harness: "pi", Model: "base-model"},
	}
}

func resolvedDigest(t *testing.T, base FleetBaseDocument, facts ProjectFacts) string {
	t.Helper()
	resolved, err := ResolveProject(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	return resolved.Digest
}

func validFacts(name, path string, overlay ProjectOverlay) ProjectFacts {
	return ProjectFacts{
		Name:    name,
		Path:    path,
		Overlay: overlay,
	}
}

func TestValidateProjectToolsRefusesUnsupportedEntries(t *testing.T) {
	cases := []struct {
		name    string
		overlay ProjectOverlay
		want    string
	}{
		{"unknown review adapter", ProjectOverlay{Review: &ToolEntry{Adapter: "bogus"}, Forge: &ToolEntry{Adapter: "github"}}, "review tool has unknown adapter \"bogus\""},
		{"github forge with path", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github", Path: "/usr/bin/gh-axi"}}, "forge adapter github does not accept path or args"},
		{"github forge with args", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github", Args: []string{"--x"}}}, "forge adapter github does not accept path or args"},
		{"no-mistakes review without forge", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}}, "review adapter no-mistakes requires a configured forge tool"},
		{"no-mistakes review with relative path", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes", Path: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github"}}, "review adapter no-mistakes path must be absolute"},
		{"gitlab forge arg with NUL", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "gitlab", Args: []string{"a\x00b"}}}, "forge adapter gitlab arg 0 contains NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProjectTools(tc.overlay)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateProjectTools() error = %v, want refusal containing %q", err, tc.want)
			}
		})
	}
	valid := ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github"}}
	if err := ValidateProjectTools(valid); err != nil {
		t.Fatalf("ValidateProjectTools(no-mistakes + github) = %v, want nil", err)
	}
}

// TestResolveRefusesMalformedConfiguredTools pins the refusals that keep a
// configured tool from resolving outside the Fleet probe or from carrying a
// step the snapshot cannot trust.
func TestResolveRefusesMalformedConfiguredTools(t *testing.T) {
	base := validBase()
	facts := validFacts("alpha", "/alpha", ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github"}})
	bare := validFacts("alpha", "/alpha", ProjectOverlay{})
	ready := ResolvedStep{Adapter: "no-mistakes", Path: "/bin/no-mistakes", ProbeState: "ready"}
	absent := func(string, ToolEntry) ResolvedStep {
		return ResolvedStep{Adapter: "no-mistakes", ProbeState: "absent", Reason: "not on PATH"}
	}
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"fleet base carries a review tool", func() error {
			b := validBase()
			b.Config.Review = &ToolEntry{Adapter: "no-mistakes"}
			return b.Validate()
		}, "project-only"},
		{"configured tools bypass the Fleet probe", func() error {
			_, err := ResolveProject(base, facts)
			return err
		}, "must be resolved by Fleet"},
		{"configured tool not Ready at probe", func() error {
			_, err := ResolveProjectWithToolProbe(base, facts, absent)
			return err
		}, "review adapter no-mistakes probe absent"},
		{"step not matching the overlay", func() error {
			_, err := ResolveProjectWithSteps(base, bare, ready, baselineStep())
			return err
		}, "do not match the project overlay"},
		{"baseline step carrying tool data", func() error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Baseline: true, Adapter: "no-mistakes", ProbeState: "baseline"}, baselineStep())
			return err
		}, "baseline carries configured tool data"},
		{"review adapter outside the review set", func() error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Adapter: "github", Path: "/bin/gh-axi", ProbeState: "ready"}, baselineStep())
			return err
		}, "resolved review adapter github probe ready"},
		{"relative executable path", func() error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Adapter: "no-mistakes", Path: "no-mistakes", ProbeState: "ready"}, baselineStep())
			return err
		}, "requires an absolute executable path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want refusal containing %q", err, tc.want)
			}
		})
	}
}

// TestResolvedSnapshotWithStepsRefusesInvalidSteps pins the snapshot rebase
// refusals: an empty snapshot, and a no-mistakes review without a forge step.
func TestResolvedSnapshotWithStepsRefusesInvalidSteps(t *testing.T) {
	snap, err := NewResolvedSnapshotWithToolProbe(validBase(), validFacts("alpha", "/alpha", ProjectOverlay{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := ResolvedStep{Adapter: "no-mistakes", Path: "/bin/no-mistakes", ProbeState: "ready"}
	if _, err := (ResolvedSnapshot{}).WithSteps(baselineStep(), baselineStep()); err == nil || !strings.Contains(err.Error(), "resolved snapshot is empty") {
		t.Fatalf("WithSteps on an empty snapshot = %v, want an empty-snapshot refusal", err)
	}
	if _, err := snap.WithSteps(ready, baselineStep()); err == nil || !strings.Contains(err.Error(), "requires a configured forge tool") {
		t.Fatalf("WithSteps(no-mistakes, baseline forge) = %v, want a forge refusal", err)
	}
}
