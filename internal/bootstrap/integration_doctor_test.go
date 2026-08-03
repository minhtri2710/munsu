package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

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
