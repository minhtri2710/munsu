package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
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
