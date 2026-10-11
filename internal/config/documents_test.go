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
	alpha, err := resolveBaseline(base, validFacts("alpha", "/alpha", ProjectOverlay{SoldierHarness: "claude", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := resolveBaseline(base, validFacts("beta", "/beta", ProjectOverlay{SoldierHarness: "codex"}))
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
	resolved, err := resolveBaseline(base, facts)
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
	resolved, err := resolveBaseline(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Backend != "herdr" {
		t.Fatalf("Backend = %q, want herdr", resolved.Backend)
	}
}

func TestResolveProjectBackendPrecedence(t *testing.T) {
	base := validBase() // Backend: "tmux" fleet default
	facts := validFacts("alpha", "/alpha", ProjectOverlay{})

	baseOnly, err := resolveBaseline(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	if baseOnly.Backend != "tmux" {
		t.Fatalf("Backend = %q, want base default", baseOnly.Backend)
	}

	project, err := resolveBaseline(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
	if err != nil {
		t.Fatal(err)
	}
	if project.Backend != "herdr" {
		t.Fatalf("project overlay Backend = %q, want herdr", project.Backend)
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
	resolved, err := resolveBaseline(base, validFacts("alpha", "/alpha", ProjectOverlay{}))
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
	resolved, err := resolveBaseline(base, validFacts("alpha", "/alpha", ProjectOverlay{Backend: "herdr"}))
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
		Config: FleetBaseConfig{
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
	resolved, err := resolveBaseline(base, facts)
	if err != nil {
		t.Fatal(err)
	}
	return resolved.Digest
}

// resolveBaseline resolves a project whose review and forge tools are both
// baseline, the state of an overlay with no tool entries.
func resolveBaseline(base FleetBaseDocument, facts ProjectFacts) (ResolvedProjectConfig, error) {
	return ResolveProjectWithSteps(base, facts, baselineStep(), baselineStep())
}

func validFacts(name, path string, overlay ProjectOverlay) ProjectFacts {
	return ProjectFacts{
		Name:    name,
		Path:    path,
		Overlay: overlay,
	}
}

// TestToolStepRefusals pins every refusal on the way from an overlay or a
// captured step to a resolved project configuration. An empty want is the
// nearest valid input and must resolve.
func TestToolStepRefusals(t *testing.T) {
	base := validBase()
	pair := ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github"}}
	facts := validFacts("alpha", "/alpha", pair)
	bare := validFacts("alpha", "/alpha", ProjectOverlay{})
	probe := func(state string) func(string, ToolEntry) ResolvedStep {
		return func(string, ToolEntry) ResolvedStep {
			return ResolvedStep{ProbeState: state, Path: "/bin/tool", Reason: "not on PATH"}
		}
	}
	ready := ResolvedStep{Adapter: "no-mistakes", Path: "/bin/no-mistakes", ProbeState: "ready"}
	readyForge := ResolvedStep{Adapter: "github", Path: "/bin/gh-axi", ProbeState: "ready"}
	snapshot := func(t *testing.T) ResolvedSnapshot {
		snap, err := NewResolvedSnapshotWithToolProbe(base, bare, nil)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	cases := []struct {
		name string
		run  func(t *testing.T) error
		want string
	}{
		{"unknown review adapter", func(*testing.T) error {
			return ValidateProjectTools(ProjectOverlay{Review: &ToolEntry{Adapter: "bogus"}, Forge: &ToolEntry{Adapter: "github"}})
		}, "review tool has unknown adapter \"bogus\""},
		{"github forge with path", func(*testing.T) error {
			return ValidateProjectTools(ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github", Path: "/usr/bin/gh-axi"}})
		}, "forge adapter github does not accept path or args"},
		{"github forge with args", func(*testing.T) error {
			return ValidateProjectTools(ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github", Args: []string{"--x"}}})
		}, "forge adapter github does not accept path or args"},
		{"no-mistakes review with relative path", func(*testing.T) error {
			return ValidateProjectTools(ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes", Path: "no-mistakes"}, Forge: &ToolEntry{Adapter: "github"}})
		}, "review adapter no-mistakes path must be absolute"},
		{"no-mistakes review without forge in the overlay", func(*testing.T) error {
			return ValidateProjectTools(ProjectOverlay{Review: &ToolEntry{Adapter: "no-mistakes"}})
		}, "review adapter no-mistakes requires a configured forge tool"},
		{"no-mistakes review with forge in the overlay", func(*testing.T) error {
			return ValidateProjectTools(pair)
		}, ""},
		{"no-mistakes review without forge in a captured step", func(t *testing.T) error {
			_, err := snapshot(t).WithSteps(ready, baselineStep())
			return err
		}, "review adapter no-mistakes requires a configured forge tool"},
		{"no-mistakes review with forge in a captured step", func(t *testing.T) error {
			_, err := snapshot(t).WithSteps(ready, readyForge)
			return err
		}, ""},
		{"empty snapshot", func(*testing.T) error {
			_, err := (ResolvedSnapshot{}).WithSteps(baselineStep(), baselineStep())
			return err
		}, "resolved snapshot is empty"},
		{"configured tool not Ready at probe", func(*testing.T) error {
			_, err := ResolveProjectWithToolProbe(base, facts, probe("absent"))
			return err
		}, "resolved review adapter no-mistakes probe absent"},
		{"configured tool Ready at probe", func(*testing.T) error {
			_, err := ResolveProjectWithToolProbe(base, facts, probe("ready"))
			return err
		}, ""},
		{"review adapter outside the review set", func(*testing.T) error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Adapter: "github", Path: "/bin/gh-axi", ProbeState: "ready"}, baselineStep())
			return err
		}, "resolved review adapter github probe ready"},
		{"step not matching the overlay", func(*testing.T) error {
			_, err := ResolveProjectWithSteps(base, bare, ready, baselineStep())
			return err
		}, "do not match the project overlay"},
		{"baseline step carrying tool data", func(*testing.T) error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Baseline: true, Adapter: "no-mistakes", ProbeState: "baseline"}, baselineStep())
			return err
		}, "baseline carries configured tool data"},
		{"relative executable path", func(*testing.T) error {
			_, err := ResolveProjectWithSteps(base, bare, ResolvedStep{Adapter: "no-mistakes", Path: "no-mistakes", ProbeState: "ready"}, baselineStep())
			return err
		}, "requires an absolute executable path"},
		{"no session backend identity anywhere", func(*testing.T) error {
			noBackend := FleetBaseDocument{SchemaVersion: FleetBaseSchemaVersion, Config: FleetBaseConfig{SoldierHarness: "pi"}}
			_, err := resolveBaseline(noBackend, bare)
			return err
		}, "resolved no session backend identity"},
		{"session backend identity from the fleet base", func(*testing.T) error {
			_, err := resolveBaseline(base, bare)
			return err
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want refusal containing %q", err, tc.want)
			}
		})
	}
}
