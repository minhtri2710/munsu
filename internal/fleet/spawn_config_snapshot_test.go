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

	base, captains, projects, err := fleetconfig.LoadDocuments(home)
	if err != nil {
		t.Fatal(err)
	}
	projects.Projects[0].Config.Model = "mutated-model"
	projects.Projects[0].Config.DispatchProfiles[0].Model = "mutated-profile-model"
	if err := fleetconfig.StoreDocuments(home, base, captains, projects); err != nil {
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

	r.writeTaskMeta()
	meta, err := homepkg.ReadMeta(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if meta["config_snapshot_digest"] != resolved.SnapshotDigest {
		t.Fatalf("config_snapshot_digest = %q, want %q", meta["config_snapshot_digest"], resolved.SnapshotDigest)
	}
}

func TestConfigPushPropagatesTypedConfigDocuments(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "alpha")
	if err := os.MkdirAll(filepath.Join(captain, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captain, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captain, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeSpawnSnapshotDocuments(t, parent)

	if err := ConfigPush(parent, captain); err != nil {
		t.Fatal(err)
	}
	general, err := ResolveSpawnProjectConfig(parent, Args{ProjectName: "alpha"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	captainResolved, err := ResolveSpawnProjectConfig(captain, Args{ProjectName: "alpha"}, "captain")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(general.Soldier, captainResolved.Soldier) || general.SnapshotDigest != captainResolved.SnapshotDigest {
		t.Fatalf("captain typed config diverged\ngeneral=%+v %s\ncaptain=%+v %s", general.Soldier, general.SnapshotDigest, captainResolved.Soldier, captainResolved.SnapshotDigest)
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
			base, captains, projects, err := fleetconfig.LoadDocuments(home)
			if err != nil {
				t.Fatal(err)
			}
			projects.Projects[0].Config.DispatchProfiles[0].Harness = "bogus"
			if err := fleetconfig.StoreDocuments(home, base, captains, projects); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateInvalidProfile},
		{name: "missing harness", project: "alpha", mutate: func(home string) {
			base, captains, projects, err := fleetconfig.LoadDocuments(home)
			if err != nil {
				t.Fatal(err)
			}
			base.Config.SoldierHarness = ""
			projects.Projects[0].Config.DispatchProfiles = nil
			if err := fleetconfig.StoreDocuments(home, base, captains, projects); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateInvalidProfile},
		{name: "partial snapshot", project: "alpha", mutate: func(home string) {
			if err := os.Remove(filepath.Join(home, fleetconfig.BaseDocumentPath)); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateIncompatibleSnapshot},
		{name: "incompatible snapshot", project: "alpha", mutate: func(home string) {
			path := filepath.Join(home, fleetconfig.ProjectDocumentPath)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), fleetconfig.ProjectRegistrySchemaVersion, "munsu.config.projects/v999", 1))
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

func writeSpawnSnapshotDocuments(t *testing.T, home string) {
	t.Helper()
	base := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config: fleetconfig.ProjectOverlay{
			SoldierHarness: "pi",
			Model:          "base-model",
			DefaultMode:    "direct-pr",
		},
		CaptainProfile: fleetconfig.CaptainProfile{Harness: "pi", Model: "captain-model"},
	}
	captains := fleetconfig.CaptainRegistryDocument{
		SchemaVersion: fleetconfig.CaptainRegistrySchemaVersion,
		Captains: []fleetconfig.CaptainRecord{
			{ID: "alpha-captain", Home: filepath.Join(home, "captains", "alpha"), Project: "alpha"},
			{ID: "beta-captain", Home: filepath.Join(home, "captains", "beta"), Project: "beta"},
		},
	}
	projects := fleetconfig.ProjectRegistryDocument{
		SchemaVersion: fleetconfig.ProjectRegistrySchemaVersion,
		Projects: []fleetconfig.ProjectRecord{
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
		},
	}
	if err := fleetconfig.StoreDocuments(home, base, captains, projects); err != nil {
		t.Fatal(err)
	}
}
