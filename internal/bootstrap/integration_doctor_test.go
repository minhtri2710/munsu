package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

// TestCheckPipelineReadiness_TypedRequireNoMistakes verifies the doctor's
// pipeline_readiness gate reads the typed fleet base document's
// requireNoMistakes field, not the legacy flat config file.
func TestCheckPipelineReadiness_TypedRequireNoMistakes(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no-mistakes binary absent

	t.Run("typed base requires no-mistakes", func(t *testing.T) {
		home := t.TempDir()
		if err := config.StoreFleetBase(home, config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        config.ProjectOverlay{RequireNoMistakes: &[]bool{true}[0]},
		}); err != nil {
			t.Fatal(err)
		}
		entry := checkPipelineReadiness(home)
		if entry.Status != StatusAbsent {
			t.Fatalf("status = %s, want absent (entry: %+v)", entry.Status, entry)
		}
		if !strings.Contains(entry.Detail, "require-no-mistakes set") {
			t.Errorf("detail = %q, want require-no-mistakes set", entry.Detail)
		}
	})

	t.Run("legacy flat file alone is ignored", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config", "require-no-mistakes"), []byte("true\n"), 0644); err != nil {
			t.Fatal(err)
		}
		entry := checkPipelineReadiness(home)
		if entry.Status != StatusCurrent || entry.Detail != "direct-PR mode (no require-no-mistakes)" {
			t.Fatalf("entry = %+v, want direct-PR (flat file must not gate)", entry)
		}
	})

	t.Run("fresh home is direct-PR", func(t *testing.T) {
		entry := checkPipelineReadiness(t.TempDir())
		if entry.Status != StatusCurrent || entry.Detail != "direct-PR mode (no require-no-mistakes)" {
			t.Fatalf("entry = %+v, want direct-PR", entry)
		}
	})

	t.Run("malformed base fails closed", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, config.BaseDocumentPath), []byte("{not json"), 0644); err != nil {
			t.Fatal(err)
		}
		entry := checkPipelineReadiness(home)
		if entry.Status != StatusStale {
			t.Fatalf("status = %s, want stale for malformed base (entry: %+v)", entry.Status, entry)
		}
	})
}

func TestCheckCaptainRegistry_NoInitializedHome(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-home")
	entry := checkCaptainRegistry(missing)
	if entry.Status != StatusAbsent {
		t.Fatalf("status = %s, want %s (entry: %+v)", entry.Status, StatusAbsent, entry)
	}
	// Read-only: the probe must not create the home directory.
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("doctor created the home directory; want read-only probe")
	}
}

func TestCheckCaptainRegistry_EmptyRegistry(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	entry := checkCaptainRegistry(parent)
	if entry.Status != StatusAbsent {
		t.Fatalf("status = %s, want %s (entry: %+v)", entry.Status, StatusAbsent, entry)
	}
	if !strings.Contains(entry.Detail, "no captains registered") {
		t.Errorf("detail = %q, want mention of no captains", entry.Detail)
	}
	if entry.RepairCmd == "" {
		t.Errorf("expected a repair command for the empty registry case")
	}
}

func TestCheckCaptainRegistry_Registered(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := t.TempDir()
	if err := fleet.Register(parent, "sm-alpha", captainHome, "", ""); err != nil {
		t.Fatal(err)
	}
	entry := checkCaptainRegistry(parent)
	if entry.Status != StatusCurrent {
		t.Fatalf("status = %s, want %s (entry: %+v)", entry.Status, StatusCurrent, entry)
	}
	if !strings.Contains(entry.Detail, "1 captain(s)") {
		t.Errorf("detail = %q, want registered count", entry.Detail)
	}
}

func TestCheckCaptainRegistry_Malformed(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(parent, "state", "fleet-registry", "captains.json")
	if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := checkCaptainRegistry(parent)
	if entry.Status != StatusStale {
		t.Fatalf("status = %s, want %s (entry: %+v)", entry.Status, StatusStale, entry)
	}
}

func TestCheckConvergeReadiness(t *testing.T) {
	convergeLock := func(homeDir string) string {
		return filepath.Join(homeDir, "state", fleet.ConvergeLockName)
	}

	t.Run("absent lock reports ready", func(t *testing.T) {
		entry := checkConvergeReadiness(t.TempDir())
		if entry.Status != StatusCurrent {
			t.Fatalf("status = %s, want %s (entry: %+v)", entry.Status, StatusCurrent, entry)
		}
		if !strings.Contains(entry.Detail, "no converge lock") {
			t.Errorf("detail = %q, want mention of no converge lock", entry.Detail)
		}
		if entry.RepairCmd != "" {
			t.Errorf("unexpected repair command for absent lock: %q", entry.RepairCmd)
		}
	})

	t.Run("free lock file is stale but harmless", func(t *testing.T) {
		homeDir := t.TempDir()
		lockPath := convergeLock(homeDir)
		if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, []byte("leftover\n"), 0644); err != nil {
			t.Fatal(err)
		}
		entry := checkConvergeReadiness(homeDir)
		if entry.Status != StatusCurrent {
			t.Fatalf("status = %s, want %s for a free lock (entry: %+v)", entry.Status, StatusCurrent, entry)
		}
		if entry.RepairCmd != "" {
			t.Errorf("unexpected repair command for a free lock: %q", entry.RepairCmd)
		}
	})

	t.Run("held lock is a live converge, not a fault", func(t *testing.T) {
		homeDir := t.TempDir()
		lockPath := convergeLock(homeDir)
		if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
			t.Fatal(err)
		}
		release := holdConvergeLock(t, lockPath)
		defer release()
		entry := checkConvergeReadiness(homeDir)
		if entry.Status != StatusCurrent {
			t.Fatalf("status = %s, want %s for a held lock (entry: %+v)", entry.Status, StatusCurrent, entry)
		}
		if !strings.Contains(entry.Detail, "in progress") {
			t.Errorf("detail = %q, want mention of converge in progress", entry.Detail)
		}
		if entry.RepairCmd != "" {
			t.Errorf("a live converge must not get an rm -f repair, got %q", entry.RepairCmd)
		}
	})
}
