package fleet

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
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

// The probe reports the backend the project resolves to (base merged with its
// overlay), as spawn does: unset refuses at resolution, a missing binary after
// the harness passes.
func TestProbeProjectReadinessChecksTheProjectBackend(t *testing.T) {
	for _, tc := range []struct {
		name, baseBackend, overlayBackend string
		onPath                            []string
		wantResolveErr, wantErr           string
	}{
		{"overlay herdr absent", "tmux", "herdr", []string{"pi", "tmux"}, "", "herdr: not found on PATH"},
		{"overlay herdr present", "tmux", "herdr", []string{"pi", "herdr"}, "", ""},
		{"no backend anywhere", "", "", []string{"pi", "tmux", "herdr"}, "resolved no session backend identity", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			repo := initRepo(t, t.TempDir())
			storeTestDocuments(t, homeDir, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.FleetBaseConfig{Backend: tc.baseBackend, SoldierHarness: "pi", Model: "gpt-5"},
			}, []testProjectRecord{{Name: "alpha", Path: repo, Config: config.ProjectOverlay{Backend: tc.overlayBackend}}}, nil)
			bin := t.TempDir()
			for _, tool := range tc.onPath {
				testutil.WriteFakeExecutable(t, filepath.Join(bin, tool), "#!/bin/sh\nexit 0\n")
			}
			t.Setenv("PATH", bin+":/usr/bin:/bin")

			pr, err := ProbeProjectReadiness(homeDir, "alpha")
			if tc.wantResolveErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantResolveErr) {
					t.Fatalf("ProbeProjectReadiness err = %v, want %q", err, tc.wantResolveErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProbeProjectReadiness: %v", err)
			}
			if tc.wantErr == "" && pr.Err != nil || tc.wantErr != "" && (pr.Err == nil || !strings.Contains(pr.Err.Error(), tc.wantErr)) {
				t.Fatalf("readiness err = %v, want %q", pr.Err, tc.wantErr)
			}
		})
	}
}

// ResolveProjectBackend merges the overlay over the base, and an unregistered
// project reports the registry's refusal.
func TestResolveProjectBackend(t *testing.T) {
	homeDir := t.TempDir()
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{Backend: "tmux"},
	}, []testProjectRecord{
		{Name: "plain", Path: t.TempDir()},
		{Name: "over", Path: t.TempDir(), Config: config.ProjectOverlay{Backend: "herdr"}},
	}, nil)
	for _, tc := range []struct{ project, want, wantErr string }{
		{"plain", "tmux", ""},
		{"over", "herdr", ""},
		{"ghost", "", "project ghost not found"},
	} {
		got, err := ResolveProjectBackend(homeDir, tc.project)
		if got != tc.want || (tc.wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("ResolveProjectBackend(%q) = %q, %v; want %q, %q", tc.project, got, err, tc.want, tc.wantErr)
		}
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
