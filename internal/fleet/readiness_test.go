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

// A profile whose match rule names the project must not change the default
// harness the probe reports: a spawn with no task resolves to the default.
func TestProbeProjectReadinessIgnoresProfilesMatchedByProjectName(t *testing.T) {
	homeDir := t.TempDir()
	repo := initRepo(t, t.TempDir())
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
	}, []testProjectRecord{{Name: "web", Path: repo, Config: config.ProjectOverlay{
		DispatchProfiles: []config.DispatchProfile{{Name: "web-ui", Match: []string{"web"}, Harness: "codex"}},
	}}}, nil)
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")

	pr, err := ProbeProjectReadiness(homeDir, "web")
	if err != nil {
		t.Fatalf("ProbeProjectReadiness: %v", err)
	}
	var pre *harness.PreflightError
	if pr.Harness != "pi" || !errors.As(pr.Err, &pre) || pre.Harness != "pi" {
		t.Fatalf("readiness = %+v, want the default pi refusal", pr)
	}
}

// A harness named only by a dispatch profile is probed on its own and reported
// as absent when its binary is missing.
func TestProbeProjectReadinessReportsProfileOnlyHarness(t *testing.T) {
	homeDir := t.TempDir()
	repo := initRepo(t, t.TempDir())
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
	}, []testProjectRecord{{Name: "alpha", Path: repo, Config: config.ProjectOverlay{
		DispatchProfiles: []config.DispatchProfile{{Name: "review", Match: []string{"never"}, Use: []config.DispatchCandidate{{Harness: "codex"}}}},
	}}}, nil)
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")

	pr, err := ProbeProjectReadiness(homeDir, "alpha")
	if err != nil {
		t.Fatalf("ProbeProjectReadiness: %v", err)
	}
	if len(pr.Profiles) != 1 || pr.Profiles[0].Harness != "codex" {
		t.Fatalf("profiles = %+v, want one codex entry", pr.Profiles)
	}
	var pre *harness.PreflightError
	if !errors.As(pr.Profiles[0].Err, &pre) || pre.Reason != "binary-absent" {
		t.Fatalf("codex profile readiness = %v, want binary-absent", pr.Profiles[0].Err)
	}
}
