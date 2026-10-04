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

	_, _, err := ResolveBriefProject(home, "alpha", "", true)
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

	_, _, err := ResolveBriefProject(home, "alpha", "", true)
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

	_, _, err := ResolveBriefProject(home, "missing", "", true)
	if err == nil {
		t.Fatal("unknown project must fail closed, not fall back to base/auto")
	}
	var remediation *fleetconfig.RemediationError
	if !errors.As(err, &remediation) || remediation.Code != fleetconfig.RemediateUnknownProject {
		t.Fatalf("error = %T %v, want unknown-project remediation", err, err)
	}
}

// TestResolveBriefProject_ProjectErrorNoBaseFallback proves that a
// project resolution error is not masked by a valid base default: the base
// default mode must not be consulted when the project snapshot fails.
func TestResolveBriefProject_ProjectErrorNoBaseFallback(t *testing.T) {
	home := t.TempDir()
	// Valid base carrying a default mode, but no project registered.
	storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.ProjectOverlay{DefaultMode: "direct-pr"},
	}, nil, nil)

	_, _, err := ResolveBriefProject(home, "ghost", "", true)
	if err == nil {
		t.Fatal("unregistered project must fail closed, not fall back to base default")
	}
}

// TestResolveBriefProject_RequireNoMistakesRefusesAutoFallback
// proves that a project setting require-no-mistakes refuses the auto fallback
// for both reasons the refusal names — an absent binary and one present on
// PATH but incompatible — instead of silently delivering under direct-PR.
func TestResolveBriefProject_RequireNoMistakesRefusesAutoFallback(t *testing.T) {
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

		mode, _, err := ResolveBriefProject(home, "alpha", "", true)
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

		mode, _, err := ResolveBriefProject(home, "alpha", "", true)
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

// TestResolveBriefProject_SuccessResolvesSnapshot proves the
// successful path resolves the mode and the tamper check from the single
// immutable project snapshot, honors an explicit mode override, and with a
// recorded contract (selectMode false) selects no mode.
func TestResolveBriefProject_SuccessResolvesSnapshot(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home) // base default-mode direct-pr
	t.Setenv("PATH", t.TempDir())        // ensure auto cannot pick no-mistakes

	mode, _, err := ResolveBriefProject(home, "alpha", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("mode = %q, want direct-PR from snapshot", mode)
	}

	mode, _, err = ResolveBriefProject(home, "alpha", "local-only", true)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local-only" {
		t.Errorf("explicit mode = %q, want local-only", mode)
	}

	storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.ProjectOverlay{Backend: "tmux", TamperCheck: "floor --base <base>", RequireNoMistakes: &[]bool{true}[0]},
	}, []testProjectRecord{{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")}}, nil)
	mode, tamper, err := ResolveBriefProject(home, "alpha", "", false)
	if err != nil {
		t.Fatalf("recorded contract must skip mode selection even with require-no-mistakes and no binary: %v", err)
	}
	if mode != "" || tamper != "floor --base <base>" {
		t.Errorf("contracted resolve = mode %q tamper %q, want empty mode and the base tamper check", mode, tamper)
	}
	if _, _, err := ResolveBriefProject(home, "alpha", "", true); err == nil {
		t.Fatal("selecting a mode with require-no-mistakes and no binary must refuse")
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
	if _, _, err := Mode(homeDir, "alpha"); err == nil {
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
