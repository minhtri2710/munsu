package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestResolveDeliveryModeFromProject_MalformedBaseFailsClosed proves that a
// malformed base document blocks project resolution with a typed error and
// never falls back to the fleet base or to auto-detection.
func TestResolveDeliveryModeFromProject_MalformedBaseFailsClosed(t *testing.T) {
	home := t.TempDir()
	basePath := filepath.Join(home, fleetconfig.BaseDocumentPath)
	if err := os.MkdirAll(filepath.Dir(basePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveDeliveryModeFromProject(home, "alpha", "")
	if err == nil {
		t.Fatal("malformed base must fail closed, not fall back to base/auto")
	}
}

// TestResolveDeliveryModeFromProject_MalformedOverlayFailsClosed proves that a
// malformed project overlay blocks project resolution with a typed error and
// never falls back to the fleet base.
func TestResolveDeliveryModeFromProject_MalformedOverlayFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	overlayPath := filepath.Join(home, fleetconfig.ProjectOverlayDocumentPath)
	if err := os.WriteFile(overlayPath, []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveDeliveryModeFromProject(home, "alpha", "")
	if err == nil {
		t.Fatal("malformed project overlay must fail closed, not fall back to base")
	}
}

// TestResolveDeliveryModeFromProject_UnknownProjectFailsClosed proves that an
// unregistered project produces a typed unknown-project failure, never a
// fallback to the fleet base or to auto-detection.
func TestResolveDeliveryModeFromProject_UnknownProjectFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home) // registers alpha, beta

	_, err := ResolveDeliveryModeFromProject(home, "missing", "")
	if err == nil {
		t.Fatal("unknown project must fail closed, not fall back to base/auto")
	}
	var remediation *fleetconfig.RemediationError
	if !errors.As(err, &remediation) || remediation.Code != fleetconfig.RemediateUnknownProject {
		t.Fatalf("error = %T %v, want unknown-project remediation", err, err)
	}
}

// TestResolveDeliveryModeFromProject_ProjectErrorNoBaseFallback proves that a
// project resolution error is not masked by a valid base default: the base
// default mode must not be consulted when the project snapshot fails.
func TestResolveDeliveryModeFromProject_ProjectErrorNoBaseFallback(t *testing.T) {
	home := t.TempDir()
	// Valid base carrying a default mode, but no project registered.
	storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.ProjectOverlay{DefaultMode: "direct-pr"},
	}, nil, nil)

	_, err := ResolveDeliveryModeFromProject(home, "ghost", "")
	if err == nil {
		t.Fatal("unregistered project must fail closed, not fall back to base default")
	}
}

// TestResolveDeliveryModeFromProject_RequireNoMistakesRefusesAutoFallback
// proves that a project setting require-no-mistakes refuses the auto fallback
// for both reasons the refusal names — an absent binary and one present on
// PATH but incompatible — instead of silently delivering under direct-PR.
func TestResolveDeliveryModeFromProject_RequireNoMistakesRefusesAutoFallback(t *testing.T) {
	require := true
	newHome := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		// No default mode: the require gate is only reachable on the auto path.
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness:    "pi",
				Model:             "base-model",
				Backend:           "tmux",
				RequireNoMistakes: &require,
			},
			CaptainProfile: fleetconfig.CaptainProfile{Harness: "pi", Model: "captain-model"},
		}, []testProjectRecord{{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")}}, nil)
		return home
	}

	t.Run("absent binary", func(t *testing.T) {
		home := newHome(t)
		t.Setenv("PATH", t.TempDir())

		mode, err := ResolveDeliveryModeFromProject(home, "alpha", "")
		if err == nil {
			t.Fatalf("require-no-mistakes with no binary must refuse, got mode=%q", mode)
		}
		if !strings.Contains(err.Error(), "require-no-mistakes is set") {
			t.Fatalf("refusal must come from the require gate, got %v", err)
		}
		if mode != "" {
			t.Errorf("mode must be empty on refusal, got %q", mode)
		}
	})

	t.Run("incompatible binary on PATH", func(t *testing.T) {
		home := newHome(t)
		testutil.PrependPath(t, createFakeNoMistakesVersion(t, "0.5.0"))

		mode, err := ResolveDeliveryModeFromProject(home, "alpha", "")
		if err == nil {
			t.Fatalf("require-no-mistakes with an incompatible binary must refuse, got mode=%q", mode)
		}
		if !strings.Contains(err.Error(), "require-no-mistakes is set") {
			t.Fatalf("refusal must come from the require gate, got %v", err)
		}
		if mode != "" {
			t.Errorf("mode must be empty on refusal, got %q", mode)
		}
	})
}

// TestResolveDeliveryModeFromProject_SuccessResolvesSnapshot proves the
// successful path resolves the mode from the single immutable project snapshot
// and honors an explicit mode override.
func TestResolveDeliveryModeFromProject_SuccessResolvesSnapshot(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home) // base default-mode direct-pr
	t.Setenv("PATH", t.TempDir())        // ensure auto cannot pick no-mistakes

	mode, err := ResolveDeliveryModeFromProject(home, "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("mode = %q, want direct-PR from snapshot", mode)
	}

	mode, err = ResolveDeliveryModeFromProject(home, "alpha", "local-only")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local-only" {
		t.Errorf("explicit mode = %q, want local-only", mode)
	}
}
