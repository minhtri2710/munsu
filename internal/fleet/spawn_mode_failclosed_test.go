package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
)

// TestResolveBriefProject_MalformedBaseFailsClosed proves that a
// malformed base document blocks project resolution with a typed error and
// never falls back to the fleet base or to auto-detection.
func TestResolveBriefProject_MalformedBaseFailsClosed(t *testing.T) {
	home := t.TempDir()
	basePath := filepath.Join(home, fleetconfig.BaseDocumentPath)
	if err := os.MkdirAll(filepath.Dir(basePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, err := ResolveBriefProject(home, "alpha", true)
	if err == nil {
		t.Fatal("malformed base must fail closed, not fall back to base/auto")
	}
}

// TestResolveBriefProject_MalformedOverlayFailsClosed proves that a
// malformed project overlay blocks project resolution with a typed error and
// never falls back to the fleet base.
func TestResolveBriefProject_MalformedOverlayFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	overlayPath := filepath.Join(home, fleetconfig.ProjectOverlayDocumentPath)
	if err := os.WriteFile(overlayPath, []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, err := ResolveBriefProject(home, "alpha", true)
	if err == nil {
		t.Fatal("malformed project overlay must fail closed, not fall back to base")
	}
}

// TestResolveBriefProject_UnknownProjectFailsClosed proves that an
// unregistered project produces a typed unknown-project failure, never a
// fallback to the fleet base or to auto-detection.
func TestResolveBriefProject_UnknownProjectFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home) // registers alpha, beta

	_, _, err := ResolveBriefProject(home, "missing", true)
	if err == nil {
		t.Fatal("unknown project must fail closed, not fall back to base/auto")
	}
	var remediation *fleetconfig.RemediationError
	if !errors.As(err, &remediation) || remediation.Code != fleetconfig.RemediateUnknownProject {
		t.Fatalf("error = %T %v, want unknown-project remediation", err, err)
	}
}

// TestResolveBriefProjectDerivesModeFromConfiguredTools pins the derivation
// table at the spawn boundary: the mode comes from the project's configured
// review and forge tools, and a baseline step never selects a mode of its own.
func TestResolveBriefProjectDerivesModeFromConfiguredTools(t *testing.T) {
	installFakeGH(t)
	github := &fleetconfig.ToolEntry{Adapter: "github"}
	for _, tc := range []struct {
		name          string
		review, forge *fleetconfig.ToolEntry
		want          string
	}{
		{name: "no tools is local-only", want: "local-only"},
		{name: "baseline review with forge is direct-PR", forge: github, want: "direct-PR"},
		{name: "no-mistakes review with forge is no-mistakes", review: &fleetconfig.ToolEntry{Adapter: "no-mistakes", Path: filepath.Join(createFakeNoMistakesReady(t), "no-mistakes")}, forge: github, want: "no-mistakes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
				SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
				Config:        fleetconfig.FleetBaseConfig{Backend: "tmux", SoldierHarness: "pi"},
			}, []testProjectRecord{
				{Name: "alpha", Path: filepath.Join(home, "projects", "alpha"), Config: fleetconfig.ProjectOverlay{Review: tc.review, Forge: tc.forge}},
			}, nil)
			mode, _, err := ResolveBriefProject(home, "alpha", true)
			if err != nil {
				t.Fatal(err)
			}
			if mode != tc.want {
				t.Errorf("mode = %q, want %q", mode, tc.want)
			}
		})
	}
}

// TestRunnerResolveModeFailsOnUnreadableRegistry proves spawn mode resolution
// fails the launch when the home's project registry cannot be read, instead
// of dropping the registry mode and auto-detecting a delivery mode.
func TestRunnerResolveModeFailsOnUnreadableRegistry(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	// A symlinked root is a home the registry refuses to open.
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(t.TempDir(), homeDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveBriefProject(homeDir, "alpha", true); err == nil {
		t.Fatal("fixture: registry read must fail")
	}
	r := &Runner{args: Args{ID: "task", ProjectName: "alpha"}, homeDir: homeDir, dispatchPolicy: DispatchPolicyGeneralDirect}
	if err := r.resolveMode(); err == nil {
		t.Fatalf("unreadable registry must fail spawn mode resolution, resolved %q", r.effectiveMode)
	}
	if r.effectiveMode != "" {
		t.Errorf("effective mode = %q after failed resolution, want empty", r.effectiveMode)
	}
}
