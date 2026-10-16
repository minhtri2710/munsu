package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	homepkg "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestResolveSpawnProjectConfigRejectsUnresolvedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy DispatchPolicy
	}{
		{name: "zero policy", policy: DispatchPolicy("")},
		{name: "invalid policy", policy: DispatchPolicy("unknown")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSpawnProjectConfig(t.TempDir(), Args{ProjectName: "alpha"}, tc.policy, nil)
			if err == nil || !strings.Contains(err.Error(), "unresolved dispatch policy") {
				t.Fatalf("error = %v, want unresolved-policy refusal", err)
			}
			if !reflect.DeepEqual(got, SpawnProjectConfig{}) {
				t.Fatalf("config = %+v, want empty config", got)
			}
		})
	}
}

func TestRunnerResolveModeRejectsUnresolvedPolicy(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	r := NewRunner(Args{ID: "alpha-task", ProjectName: "alpha", HomeDir: home})
	r.homeDir = home
	if err := r.resolveMode(); err == nil || !strings.Contains(err.Error(), "unresolved dispatch policy") {
		t.Fatalf("resolveMode error = %v, want unresolved-policy refusal", err)
	}
	if !reflect.DeepEqual(r.projectConfig, SpawnProjectConfig{}) {
		t.Fatalf("project config = %+v, want empty config", r.projectConfig)
	}
	if r.projectConfigLoaded {
		t.Fatal("project config marked loaded after unresolved-policy refusal")
	}
}

func TestResolveSpawnProjectConfigExplicitIdentityAssertions(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args Args
	}{
		{name: "matching backend", args: Args{ProjectName: "alpha", Backend: "tmux"}},
		{name: "matching harness", args: Args{ProjectName: "alpha", HarnessFlag: resolved.Soldier.Harness}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSpawnProjectConfig(home, tc.args, DispatchPolicyGeneralDirect, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.SnapshotDigest != resolved.SnapshotDigest {
				t.Fatalf("digest = %q, want stable digest %q", got.SnapshotDigest, resolved.SnapshotDigest)
			}
		})
	}
}

func TestResolveSpawnProjectConfigRejectsConflictingIdentityAssertions(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)
	for _, tc := range []struct {
		name string
		args Args
	}{
		{name: "backend", args: Args{ProjectName: "alpha", Backend: "herdr"}},
		{name: "harness", args: Args{ProjectName: "alpha", HarnessFlag: "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveSpawnProjectConfig(home, tc.args, DispatchPolicyGeneralDirect, nil); err == nil || !strings.Contains(err.Error(), "conflicts with resolved project snapshot") {
				t.Fatalf("error = %v, want conflicting identity failure", err)
			}
		})
	}
}

func TestResolveSpawnProjectConfigCrossRankIdentical(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	general, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fleetconfig.StorePublishedSnapshot(home, general.Frozen.Config()); err != nil {
		t.Fatal(err)
	}
	captain, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyCaptainMediated, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(general.Soldier, captain.Soldier) {
		t.Fatalf("effective soldier configs differ\ngeneral=%+v\ncaptain=%+v", general.Soldier, captain.Soldier)
	}
	if general.SnapshotDigest == "" || general.SnapshotDigest != captain.SnapshotDigest {
		t.Fatalf("snapshot digest mismatch general=%q captain=%q", general.SnapshotDigest, captain.SnapshotDigest)
	}
	if general.Soldier.Mode != "local-only" {
		t.Fatalf("mode = %q, want local-only: alpha configures no review or forge tool", general.Soldier.Mode)
	}
}

func TestResolveSpawnProjectConfigMultiProjectProfilesAndOverridesIsolated(t *testing.T) {
	home := t.TempDir()
	writeSpawnSnapshotDocuments(t, home)

	alphaDocs, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha", TaskDescription: "write docs"}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	alphaUI, err := ResolveSpawnProjectConfig(home, Args{
		ProjectName:     "alpha",
		TaskDescription: "polish ui",
		ModelFlag:       "spawn-model",
		EffortFlag:      "spawn-effort",
	}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "beta", TaskDescription: "polish ui"}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}

	if alphaDocs.Soldier.Harness != "claude" || alphaDocs.Soldier.Model != "alpha-docs" || alphaDocs.Soldier.Effort != "low" || alphaDocs.Soldier.Scaffold != "" {
		t.Fatalf("alpha docs selection = %+v, want claude/alpha-docs/low with the lean scaffold", alphaDocs.Soldier)
	}
	if alphaUI.Soldier.Harness != "pi" || alphaUI.Soldier.Model != "spawn-model" || alphaUI.Soldier.Effort != "spawn-effort" || alphaUI.Soldier.Scaffold != fleetconfig.ScaffoldGuided {
		t.Fatalf("alpha ui selection = %+v, want pi/spawn-model/spawn-effort with the guided scaffold", alphaUI.Soldier)
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

	resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyGeneralDirect, nil)
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
	fresh, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SnapshotDigest == resolved.SnapshotDigest {
		t.Fatal("fresh resolution should observe changed digest after config mutation")
	}
}

func TestCaptainSpawnConsumesPublishedSnapshotWithoutLocalResolution(t *testing.T) {
	parent := t.TempDir()
	captain := seedCaptainForTest(t, parent, "alpha-captain")
	writeSpawnSnapshotDocuments(t, parent)

	resolved, err := ResolveProjectSnapshot(parent, "alpha")
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

	got, err := ResolveSpawnProjectConfig(captain, Args{ProjectName: "alpha"}, DispatchPolicyCaptainMediated, nil)
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
	_, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, DispatchPolicyCaptainMediated, nil)
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
		{name: "unknown scaffold", project: "alpha", mutate: func(home string) {
			overlay, err := fleetconfig.LoadProjectOverlay(home, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			overlay.DispatchProfiles[0].Scaffold = "strict"
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
		}, want: fleetconfig.RemediateInvalidProfile},
		{name: "guided scaffold resolves", project: "alpha", mutate: func(home string) {
			overlay, err := fleetconfig.LoadProjectOverlay(home, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			overlay.DispatchProfiles[0].Scaffold = fleetconfig.ScaffoldGuided
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
		}},
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

			_, err := ResolveSpawnProjectConfig(home, Args{ProjectName: tc.project}, DispatchPolicyGeneralDirect, nil)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ResolveSpawnProjectConfig() error = %v, want acceptance: the nearest valid case of a refusal above", err)
				}
				return
			}
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

// TestYoloDoesNotRelaxConfiguredReviewTool pins the accepted +yolo contract:
// yolo is a Fleet lifecycle flag (pre-flight tangle bypass) and never lowers
// a configured forge tool, which resolves solely from the fleet base document
// and project overlay.
func TestYoloDoesNotRelaxConfiguredForgeTool(t *testing.T) {
	home := t.TempDir()
	if _, err := homepkg.Init(home); err != nil {
		t.Fatal(err)
	}
	if err := fleetconfig.StoreFleetBase(home, fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.FleetBaseConfig{SoldierHarness: "pi", Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Add(home, "alpha", filepath.Join(home, "projects", "alpha"), true); err != nil {
		t.Fatal(err)
	}
	if err := fleetconfig.StoreProjectOverlay(home, "alpha", fleetconfig.ProjectOverlay{
		Forge: &fleetconfig.ToolEntry{Adapter: "gitlab", Path: filepath.Join(home, "glab")},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveProjectSnapshot(home, "alpha")
	if err == nil || !strings.Contains(err.Error(), "forge adapter gitlab") {
		t.Fatalf("+yolo project resolved a not-Ready configured forge tool: %v", err)
	}
}

// seedTaskTreehouse records tool on task "held" in the named record only:
// a worktree binding, a launch intent, a retirement with cleanup pending, or a
// retirement whose cleanup completed.
func seedTaskTreehouse(t *testing.T, homeDir, record, tool string) {
	t.Helper()
	auth, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	canonicalCreateTask(t, auth, "held", "ship", "")
	if record == "launch" {
		begin := taskauthority.CanonicalBeginSpawnRequest{
			HomeID: auth.HomeID(), TaskID: mustTaskID(t, "held"), Precondition: domain.Of(1, 1),
			SnapshotDigest: strings.Repeat("a", 64), Backend: "tmux", Harness: "pi", Model: "opus", Effort: "high",
			Mode: "direct-PR", Kind: "ship", Project: "alpha", ParentTaskID: "parent", LaunchID: "launch-held",
			WindowLabel: "window-held", WorktreeReservationID: "wt-res", WorktreeFenceToken: "wt-fence", WorktreeTool: tool,
			EndpointReservationID: "ep-res", EndpointFenceToken: "ep-fence", SeatPathDirs: []string{"/usr/bin"}, EndpointIncarnation: "inc-held", Reason: "spawn",
			SeatEnv: testSeatEnv(),
		}
		if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-held", begin), begin); err != nil {
			t.Fatal(err)
		}
		return
	}
	bind := taskauthority.CanonicalBindWorktreeRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, "held"), Precondition: domain.Of(1, 1), Reason: "spawn",
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-held", Path: filepath.Join(homeDir, "worktrees", "held"), GitDir: "git", CommonDir: "common",
			BaseHead: strings.Repeat("a", 40), LeaseID: "lease-wt", FenceToken: "fence-wt", Treehouse: tool, BoundAtUnix: 1,
		},
	}
	if _, err := auth.BindWorktree(mustFleetOperation(t, "op-bind-held", bind), bind); err != nil {
		t.Fatal(err)
	}
	if record == "worktree" {
		return
	}
	agg, err := auth.Get(mustTaskID(t, "held"))
	if err != nil {
		t.Fatal(err)
	}
	retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, "held"), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "retire"}
	if _, err := auth.Retire(mustFleetOperation(t, "op-retire-held", retire), retire); err != nil {
		t.Fatal(err)
	}
	if record == "cleaned" {
		if err := auth.ReconcileRetirementCleanup(mustTaskID(t, "held"), agg.Generation, taskauthority.CleanupCompleted, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWorktreeToolResolution pins the worktree port: git worktree unless the
// project configures treehouse, and a configured treehouse refuses unless Ready.
func TestWorktreeToolResolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure bool
		ready     bool
		policy    DispatchPolicy
		project   string // set: resolve through ProjectWorktreeTool for this project
		homeTools bool   // resolve through HomeWorktreeTools
		taskTool  string // seed task "held" with a missing treehouse in this record: worktree, launch, retirement, cleaned
		wantErr   string
	}{
		{name: "configured treehouse not Ready is refused", configure: true, policy: DispatchPolicyGeneralDirect, wantErr: "resolved worktree adapter treehouse probe missing"},
		{name: "configured Ready treehouse is allowed", configure: true, ready: true, policy: DispatchPolicyGeneralDirect},
		{name: "published treehouse no longer Ready is refused", configure: true, policy: DispatchPolicyCaptainMediated, wantErr: "configured worktree tool is not Ready"},
		{name: "published Ready treehouse is allowed", configure: true, ready: true, policy: DispatchPolicyCaptainMediated},
		{name: "unconfigured uses git worktree with treehouse on PATH", policy: DispatchPolicyGeneralDirect},
		{name: "published snapshot of another project refuses the project tool", configure: true, ready: true, policy: DispatchPolicyCaptainMediated, project: "beta", wantErr: `does not match project "beta"`},
		{name: "published snapshot of the project allows the project tool", configure: true, ready: true, policy: DispatchPolicyCaptainMediated, project: "alpha"},
		{name: "home with a missing project treehouse refuses", configure: true, policy: DispatchPolicyGeneralDirect, homeTools: true, wantErr: "project alpha: worktree adapter treehouse probe missing"},
		{name: "home with a Ready project treehouse lists it", configure: true, ready: true, policy: DispatchPolicyGeneralDirect, homeTools: true},
		{name: "task-bound missing treehouse refuses and names the way out", policy: DispatchPolicyGeneralDirect, homeTools: true, taskTool: "worktree", wantErr: "task held: worktree adapter treehouse probe missing"},
		{name: "task launch intent with a missing treehouse refuses", policy: DispatchPolicyGeneralDirect, homeTools: true, taskTool: "launch", wantErr: "task held: worktree adapter treehouse probe missing"},
		{name: "task retirement record with a missing treehouse refuses", policy: DispatchPolicyGeneralDirect, homeTools: true, taskTool: "retirement", wantErr: "task held: worktree adapter treehouse probe missing"},
		{name: "cleaned-up task's missing treehouse is skipped", policy: DispatchPolicyGeneralDirect, homeTools: true, taskTool: "cleaned"},
		{name: "published treehouse no longer Ready refuses the project tool", configure: true, policy: DispatchPolicyCaptainMediated, project: "alpha", wantErr: "configured worktree tool is not Ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := homepkg.Init(home); err != nil {
				t.Fatal(err)
			}
			if err := fleetconfig.StoreFleetBase(home, fleetconfig.FleetBaseDocument{
				SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
				Config:        fleetconfig.FleetBaseConfig{SoldierHarness: "pi", Backend: "tmux"},
			}); err != nil {
				t.Fatal(err)
			}
			if err := Add(home, "alpha", filepath.Join(home, "projects", "alpha"), true); err != nil {
				t.Fatal(err)
			}
			pathTrace := filepath.Join(t.TempDir(), "path-treehouse")
			testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n: > "+pathTrace+"\n")
			toolTrace := filepath.Join(t.TempDir(), "configured-treehouse")
			tool := filepath.Join(t.TempDir(), "treehouse")
			var overlay fleetconfig.ProjectOverlay
			if tc.configure {
				overlay.Worktree = &fleetconfig.ToolEntry{Adapter: "treehouse", Path: tool}
			}
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
			if tc.ready || tc.policy == DispatchPolicyCaptainMediated {
				testutil.WriteFakeExecutable(t, tool, "#!/bin/sh\n: > "+toolTrace+"\n")
			}
			if tc.policy == DispatchPolicyCaptainMediated {
				snap, err := ResolveProjectSnapshot(home, "alpha")
				if err != nil {
					t.Fatal(err)
				}
				if err := fleetconfig.StorePublishedSnapshot(home, snap.Config()); err != nil {
					t.Fatal(err)
				}
				if !tc.ready {
					if err := os.Remove(tool); err != nil {
						t.Fatal(err)
					}
				}
			}

			gone := filepath.Join(t.TempDir(), "gone-treehouse")
			if tc.taskTool != "" {
				seedTaskTreehouse(t, home, tc.taskTool, gone)
			}
			var got SpawnProjectConfig
			var err error
			if tc.homeTools {
				var tools []string
				if tools, err = HomeWorktreeTools(home); err == nil {
					got.WorktreeTool = tools[len(tools)-1]
				}
			} else if tc.project != "" {
				got.WorktreeTool, err = ProjectWorktreeTool(home, tc.project)
			} else {
				got, err = ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, tc.policy, nil)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if hint := "reinstall treehouse at " + gone + " to retire task held, or reclaim one project with --project <name>"; tc.taskTool != "" && !strings.Contains(err.Error(), hint) {
					t.Fatalf("error = %v, want hint %q", err, hint)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.WorktreeStatus(home, got.WorktreeTool); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(pathTrace); !os.IsNotExist(err) {
				t.Fatalf("treehouse on PATH was invoked (stat %v)", err)
			}
			if _, err := os.Stat(toolTrace); tc.configure != (err == nil) {
				t.Fatalf("configured treehouse invoked = %v, want %v", err == nil, tc.configure)
			}
		})
	}
}

// TestQuotaToolDispatch pins the quota port at spawn: use candidates are taken
// in order unless the project configures quota-axi, a configured quota-axi that
// is not Ready refuses, and a Ready one that fails at dispatch refuses too.
func TestQuotaToolDispatch(t *testing.T) {
	report := `{"providers":[{"provider":"codex","state":{"status":"fresh"},"windows":[{"id":"five_hour","kind":"general","percentRemaining":10}]},` +
		`{"provider":"pi","state":{"status":"fresh"},"windows":[{"id":"five_hour","kind":"general","percentRemaining":90}]}]}`
	reports := "#!/bin/sh\ncat <<'EOF'\n" + report + "\nEOF\n"
	for _, tc := range []struct {
		name      string
		configure bool
		script    string
		policy    DispatchPolicy
		want      string
		wantErr   string
	}{
		{name: "configured quota-axi not Ready is refused", configure: true, policy: DispatchPolicyGeneralDirect, wantErr: "resolved quota adapter quota-axi probe missing"},
		{name: "configured Ready quota-axi is allowed", configure: true, script: reports, policy: DispatchPolicyGeneralDirect, want: "pi"},
		{name: "published quota-axi no longer Ready is refused", configure: true, policy: DispatchPolicyCaptainMediated, wantErr: "configured quota tool is not Ready"},
		{name: "published Ready quota-axi is allowed", configure: true, script: reports, policy: DispatchPolicyCaptainMediated, want: "pi"},
		{name: "Ready quota-axi failing at dispatch is refused", configure: true, script: "#!/bin/sh\nexit 1\n", policy: DispatchPolicyGeneralDirect, wantErr: "quota-balanced dispatch refused"},
		{name: "unconfigured takes the first candidate with quota-axi on PATH", policy: DispatchPolicyGeneralDirect, want: "codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := homepkg.Init(home); err != nil {
				t.Fatal(err)
			}
			if err := fleetconfig.StoreFleetBase(home, fleetconfig.FleetBaseDocument{
				SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
				Config: fleetconfig.FleetBaseConfig{Backend: "tmux", DispatchProfiles: []fleetconfig.DispatchProfile{{
					Name: "balanced", Match: []string{"*"},
					Use: []fleetconfig.DispatchCandidate{{Harness: "codex"}, {Harness: "pi"}},
				}}},
			}); err != nil {
				t.Fatal(err)
			}
			if err := Add(home, "alpha", filepath.Join(home, "projects", "alpha"), true); err != nil {
				t.Fatal(err)
			}
			pathTrace := filepath.Join(t.TempDir(), "path-quota-axi")
			testutil.FakeOnPath(t, "quota-axi", "#!/bin/sh\n: > "+pathTrace+"\n")
			tool := filepath.Join(t.TempDir(), "quota-axi")
			var overlay fleetconfig.ProjectOverlay
			if tc.configure {
				overlay.Quota = &fleetconfig.ToolEntry{Adapter: "quota-axi", Path: tool}
			}
			if err := fleetconfig.StoreProjectOverlay(home, "alpha", overlay); err != nil {
				t.Fatal(err)
			}
			if tc.script != "" || tc.policy == DispatchPolicyCaptainMediated {
				testutil.WriteFakeExecutable(t, tool, tc.script)
			}
			if tc.policy == DispatchPolicyCaptainMediated {
				snap, err := ResolveProjectSnapshot(home, "alpha")
				if err != nil {
					t.Fatal(err)
				}
				if err := fleetconfig.StorePublishedSnapshot(home, snap.Config()); err != nil {
					t.Fatal(err)
				}
				if tc.script == "" {
					if err := os.Remove(tool); err != nil {
						t.Fatal(err)
					}
				}
			}

			got, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, tc.policy, nil)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Soldier.Harness != tc.want {
				t.Fatalf("harness = %q, want %q", got.Soldier.Harness, tc.want)
			}
			if _, err := os.Stat(pathTrace); !os.IsNotExist(err) {
				t.Fatalf("quota-axi on PATH was invoked (stat %v)", err)
			}
		})
	}
}

func writeSpawnSnapshotDocuments(t *testing.T, home string) {
	t.Helper()
	base := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config: fleetconfig.FleetBaseConfig{
			SoldierHarness: "pi",
			Model:          "base-model",
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
					{Name: "alpha-ui", Match: []string{"ui"}, Harness: "pi", Model: "alpha-ui", Effort: "high", Scaffold: fleetconfig.ScaffoldGuided},
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

// A general home resolves its session backend from the typed fleet base only.
// There is no auto-detection and no fallback identity, so a base document
// carrying no backend is a typed failure rather than a device/PATH choice.
func TestResolveGeneralHomeBackendRefusesEmptyIdentity(t *testing.T) {
	home := t.TempDir()
	if _, err := homepkg.Init(home); err != nil {
		t.Fatal(err)
	}
	base := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.FleetBaseConfig{SoldierHarness: "pi"},
	}
	if err := fleetconfig.StoreFleetBase(home, base); err != nil {
		t.Fatal(err)
	}
	if fleetconfig.PublishedSnapshotAvailable(home) {
		t.Fatal("fixture invalid: a published snapshot exists, so the base document is not what is read")
	}

	got, err := ResolveGeneralHomeBackend(home)
	if err == nil {
		t.Fatalf("resolution returned backend %q for a home with no backend identity", got)
	}
	if !strings.Contains(err.Error(), "resolved no session backend identity") {
		t.Fatalf("error = %v, want the empty-identity refusal", err)
	}

	// Control: only the backend was missing, so setting it resolves.
	base.Config.Backend = "tmux"
	if err := fleetconfig.StoreFleetBase(home, base); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveGeneralHomeBackend(home); err != nil || got != "tmux" {
		t.Fatalf("ResolveGeneralHomeBackend = %q, %v; want tmux, nil", got, err)
	}
}
