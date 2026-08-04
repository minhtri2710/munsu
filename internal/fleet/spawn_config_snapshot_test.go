package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	homepkg "github.com/minhtri2710/munsu/internal/home"
)

func TestResolveSpawnProjectConfigCrossRankIdentical(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	general, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	if err := fleetconfig.StorePublishedSnapshot(home, general.Frozen.Config()); err != nil {
		t.Fatal(err)
	}
	captain, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "captain")
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(general.Soldier, captain.Soldier) {
		t.Fatalf("effective soldier configs differ\ngeneral=%+v\ncaptain=%+v", general.Soldier, captain.Soldier)
	}
	if general.SnapshotDigest == "" || general.SnapshotDigest != captain.SnapshotDigest {
		t.Fatalf("snapshot digest mismatch general=%q captain=%q", general.SnapshotDigest, captain.SnapshotDigest)
	}
	if general.Soldier.Mode != "direct-PR" {
		t.Fatalf("mode = %q, want normalized direct-PR", general.Soldier.Mode)
	}
}

func TestResolveSpawnProjectConfigMultiProjectProfilesAndOverridesIsolated(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	alphaDocs, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha", TaskDescription: "write docs"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	alphaUI, err := ResolveSpawnProjectConfig(home, Args{
		ProjectName:     "alpha",
		TaskDescription: "polish ui",
		ModelFlag:       "spawn-model",
		EffortFlag:      "spawn-effort",
	}, "general")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "beta", TaskDescription: "polish ui"}, "general")
	if err != nil {
		t.Fatal(err)
	}

	if alphaDocs.Soldier.Harness != "claude" || alphaDocs.Soldier.Model != "alpha-docs" || alphaDocs.Soldier.Effort != "low" {
		t.Fatalf("alpha docs selection = %+v, want claude/alpha-docs/low", alphaDocs.Soldier)
	}
	if alphaUI.Soldier.Harness != "pi" || alphaUI.Soldier.Model != "spawn-model" || alphaUI.Soldier.Effort != "spawn-effort" {
		t.Fatalf("alpha ui selection = %+v, want pi/spawn-model/spawn-effort", alphaUI.Soldier)
	}
	if alphaDocs.SnapshotDigest != alphaUI.SnapshotDigest {
		t.Fatalf("per-spawn overrides changed snapshot digest docs=%q ui=%q", alphaDocs.SnapshotDigest, alphaUI.SnapshotDigest)
	}
	if beta.Soldier.Harness != "codex" || beta.Soldier.Model != "beta-model" || beta.Soldier.Effort != "" {
		t.Fatalf("beta selection changed by alpha override: %+v", beta.Soldier)
	}
	if alphaDocs.SnapshotDigest == beta.SnapshotDigest {
		t.Fatal("distinct project snapshots must not share a digest")
	}
}

func TestResolveSpawnProjectConfigFreezesSnapshotAndDigest(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	before := resolved.Frozen.Config()

	alphaOverlay, err := fleetconfig.LoadProjectOverlay(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	alphaOverlay.Model = "mutated-model"
	alphaOverlay.DispatchProfiles[0].Model = "mutated-profile-model"
	if err := fleetconfig.StoreProjectOverlay(home, "alpha", alphaOverlay); err != nil {
		t.Fatal(err)
	}

	after := resolved.Frozen.Config()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("frozen snapshot changed after disk mutation\nbefore=%+v\nafter=%+v", before, after)
	}
	fresh, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SnapshotDigest == resolved.SnapshotDigest {
		t.Fatal("fresh resolution should observe changed digest after config mutation")
	}
}

func TestRunnerWriteTaskMetaRecordsConfigSnapshotDigest(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)
	resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		args:                Args{ID: "task-1", ProjectName: "alpha"},
		homeDir:             home,
		projPath:            resolved.ProjectPath,
		wtPath:              filepath.Join(home, "worktree"),
		harness:             resolved.Soldier.Harness,
		model:               resolved.Soldier.Model,
		effort:              resolved.Soldier.Effort,
		effectiveMode:       resolved.Soldier.Mode,
		windowID:            "win-1",
		endpoint:            CreatedEndpoint{Backend: "fake"},
		projectConfig:       resolved,
		projectConfigLoaded: true,
	}

	if err := r.writeTaskMeta(); err != nil {
		t.Fatal(err)
	}
	meta, err := homepkg.ReadMeta(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if meta["config_snapshot_digest"] != resolved.SnapshotDigest {
		t.Fatalf("config_snapshot_digest = %q, want %q", meta["config_snapshot_digest"], resolved.SnapshotDigest)
	}
}

func TestCaptainSpawnConsumesPublishedSnapshotWithoutLocalResolution(t *testing.T) {
	parent := t.TempDir()
	captain := seedCaptainForTest(t, parent, "alpha-captain")
	writeSpawnSnapshotDocuments(t, parent)

	resolved, err := ResolveProjectSnapshot(parent, "alpha", fleetconfig.BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fleetconfig.StorePublishedSnapshot(captain, resolved.Config()); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(captain, fleetconfig.BaseDocumentPath)); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected captain base document to be absent, got %v", err)
	}
	if err := os.Remove(filepath.Join(captain, fleetconfig.ProjectOverlayDocumentPath)); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected captain project overlay document to be absent, got %v", err)
	}

	got, err := ResolveSpawnProjectConfig(captain, Args{ProjectName: "alpha"}, "captain")
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotDigest != resolved.Config().Digest || got.ProjectName != "alpha" {
		t.Fatalf("captain did not consume published snapshot: %+v", got)
	}
}

func TestCaptainSpawnFailsClosedWithoutPublishedSnapshot(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)
	_, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "captain")
	if err == nil {
		t.Fatal("captain resolution should require a published snapshot")
	}
	var remediation *fleetconfig.RemediationError
	if !errors.As(err, &remediation) || remediation.Code != fleetconfig.RemediateIncompatibleSnapshot {
		t.Fatalf("error = %T %v, want incompatible-snapshot remediation", err, err)
	}
}

func TestResolveSpawnProjectConfigFailsClosedWithTypedRemediation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		mutate  func(string)
		want    fleetconfig.RemediationCode
	}{
		{name: "unknown project", project: "missing", want: fleetconfig.RemediateUnknownProject},
		{name: "invalid profile", project: "alpha", mutate: func(home string) {
			overlay, err := fleetconfig.LoadProjectOverlay(home, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			overlay.DispatchProfiles[0].Harness = "bogus"
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateInvalidProfile},
		{name: "missing harness", project: "alpha", mutate: func(home string) {
			base, err := fleetconfig.LoadFleetBase(home)
			if err != nil {
				t.Fatal(err)
			}
			base.Config.SoldierHarness = ""
			if err := fleetconfig.StoreFleetBase(home, base); err != nil {
				t.Fatal(err)
			}
			overlay, err := fleetconfig.LoadProjectOverlay(home, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			overlay.DispatchProfiles = nil
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateInvalidProfile},
		{name: "partial snapshot", project: "alpha", mutate: func(home string) {
			if err := os.Remove(filepath.Join(home, fleetconfig.BaseDocumentPath)); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateIncompatibleSnapshot},
		{name: "incompatible snapshot", project: "alpha", mutate: func(home string) {
			path := filepath.Join(home, "state", "fleet-registry", "projects.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "munsu.fleet.registry/v1", "munsu.fleet.registry/v999", 1))
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateIncompatibleSnapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeSpawnSnapshotDocuments(t, home)
			if tc.mutate != nil {
				tc.mutate(home)
			}

			_, err := ResolveSpawnProjectConfig(home, Args{ProjectName: tc.project}, "general")
			if err == nil {
				t.Fatal("ResolveSpawnProjectConfig() error = nil, want fail-closed remediation")
			}
			var remediation *fleetconfig.RemediationError
			if !errors.As(err, &remediation) {
				t.Fatalf("error %T %v does not expose RemediationError", err, err)
			}
			if remediation.Code != tc.want || remediation.Repair == "" {
				t.Fatalf("remediation = %+v, want code %q with repair", remediation, tc.want)
			}
		})
	}
}

func TestResolveSpawnProjectConfigConsumesRequireNoMistakes(t *testing.T) {
	// Base default mode unset + requireNoMistakes=true, no no-mistakes binary
	// on PATH → resolution must refuse fallback (not silently direct-PR).
	t.Run("require-no-mistakes absent binary refuses", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		home := t.TempDir()
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness:    "pi",
				RequireNoMistakes: &[]bool{true}[0],
				Backend:           "tmux",
			},
		}, []testProjectRecord{
			{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")},
		}, nil)

		_, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
		if err == nil {
			t.Fatal("expected error when require-no-mistakes is set but binary is absent")
		}
		if !strings.Contains(err.Error(), "require-no-mistakes") {
			t.Errorf("error should mention require-no-mistakes, got: %v", err)
		}
	})

	// Base default mode unset + requireNoMistakes=true with a compatible binary
	// on PATH → mode resolves to no-mistakes.
	t.Run("require-no-mistakes with binary resolves no-mistakes", func(t *testing.T) {
		tmpDir := t.TempDir()
		binPath := filepath.Join(tmpDir, "no-mistakes")
		content := `#!/bin/sh
case "$1" in
  --version)
    echo "no-mistakes version v1.40.0 (test)"
    exit 0
    ;;
  axi)
    if [ "$2" = "status" ] && [ "$3" = "--help" ]; then
      echo "Show the active run in detail"
      echo "Usage:"
      echo "  no-mistakes axi status [flags]"
      exit 0
    fi
    ;;
esac
exit 0
`
		if err := os.WriteFile(binPath, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))
		home := t.TempDir()
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness:    "pi",
				RequireNoMistakes: &[]bool{true}[0],
				Backend:           "tmux",
			},
		}, []testProjectRecord{
			{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")},
		}, nil)

		resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Soldier.Mode != "no-mistakes" {
			t.Errorf("mode = %q, want no-mistakes", resolved.Soldier.Mode)
		}
	})

	// Base default mode unset + requireNoMistakes unset, no binary → auto
	// falls back to direct-PR (unset → direct-PR default semantics preserved).
	t.Run("unset require-no-mistakes falls back to direct-PR", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		home := t.TempDir()
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config:        fleetconfig.ProjectOverlay{SoldierHarness: "pi", Backend: "tmux"},
		}, []testProjectRecord{
			{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")},
		}, nil)

		resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Soldier.Mode != "direct-PR" {
			t.Errorf("mode = %q, want direct-PR", resolved.Soldier.Mode)
		}
	})
}

func writeSpawnSnapshotDocuments(t *testing.T, home string) {
	t.Helper()
	base := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config: fleetconfig.ProjectOverlay{
			SoldierHarness: "pi",
			Model:          "base-model",
			DefaultMode:    "direct-pr",
			Backend:        "tmux",
		},
		CaptainProfile: fleetconfig.CaptainProfile{Harness: "pi", Model: "captain-model"},
	}
	projects := []testProjectRecord{
		{
			Name: "alpha",
			Path: filepath.Join(home, "projects", "alpha"),
			Config: fleetconfig.ProjectOverlay{
				Model: "alpha-model",
				DispatchProfiles: []fleetconfig.DispatchProfile{
					{Name: "alpha-docs", Match: []string{"docs"}, Harness: "claude", Model: "alpha-docs", Effort: "low"},
					{Name: "alpha-ui", Match: []string{"ui"}, Harness: "pi", Model: "alpha-ui", Effort: "high"},
				},
			},
		},
		{
			Name: "beta",
			Path: filepath.Join(home, "projects", "beta"),
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness: "codex",
				Model:          "beta-model",
			},
		},
	}
	captains := []testCaptainRecord{
		{ID: "alpha-captain", Home: filepath.Join(home, "captains", "alpha"), Project: "alpha"},
		{ID: "beta-captain", Home: filepath.Join(home, "captains", "beta"), Project: "beta"},
	}
	storeTestDocuments(t, home, base, projects, captains)
}
