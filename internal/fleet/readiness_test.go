package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

func TestSoldierHarnessPreflightRefusesUnknownHarness(t *testing.T) {
	err := SoldierHarnessPreflight(t.TempDir(), "no-such-harness")
	if err == nil || !strings.Contains(err.Error(), "not a known adapter") {
		t.Fatalf("err = %v, want not a known adapter", err)
	}
}

// The probe predicts the refusal a spawn of the project would hit: here the
// project's soldier harness binary is not on PATH.
func TestProbeProjectReadinessPredictsTheHarnessRefusal(t *testing.T) {
	homeDir := t.TempDir()
	repo := initRepo(t, t.TempDir())
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
	}, []testProjectRecord{{Name: "alpha", Path: repo}}, nil)
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")

	pr, err := ProbeProjectReadiness(homeDir, "alpha")
	if err != nil {
		t.Fatalf("ProbeProjectReadiness: %v", err)
	}
	var pre *harness.PreflightError
	if !errors.As(pr.Err, &pre) || pre.Reason != "binary-absent" || pr.Harness != "pi" {
		t.Fatalf("readiness = %+v, want the pi binary-absent refusal", pr)
	}
}
