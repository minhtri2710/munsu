package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestWorktreeOwnershipGuards(t *testing.T) {
	const path = "/some/worktree"
	artifactWorktree := func(t *testing.T, name string) string {
		worktree := t.TempDir()
		if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		artifact := filepath.Join(worktree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact, []byte("held\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return worktree
	}
	reconciled := func(entries ...backend.WorktreeEntry) func(*testing.T) error {
		return func(*testing.T) error {
			_, err := reconciledOrphanWorktree(path, entries)
			return err
		}
	}
	cases := []struct {
		name    string
		run     func(t *testing.T) error
		refused string
	}{
		{"block: provider lists an empty worktree path", reconciled(backend.WorktreeEntry{Path: ""}), "provider status contains an empty worktree path; refusing return"},
		{"block: provider lists the worktree twice", reconciled(backend.WorktreeEntry{Path: path}, backend.WorktreeEntry{Path: path}), `provider status lists worktree "/some/worktree" more than once; refusing return`},
		{"block: provider does not list the worktree", reconciled(backend.WorktreeEntry{Path: "/some/other"}), `worktree ownership is not established by provider status; refusing return "/some/worktree"`},
		{"block: lease holder not reconciled", reconciled(backend.WorktreeEntry{Path: path, LeaseHolder: "wt-unknown"}), `worktree lease holder "wt-unknown" is not reconciled; refusing return`},
		{"allow: provider lists the worktree once without a lease holder", reconciled(backend.WorktreeEntry{Path: path}), ""},
		{"block: relative worktree path", func(*testing.T) error { return verifyManualWorktreeRelease("relative/worktree") }, `worktree path must be absolute for ownership verification: "relative/worktree"`},
		{"block: .git marker is a directory", func(t *testing.T) error {
			worktree := t.TempDir()
			if err := os.Mkdir(filepath.Join(worktree, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			return verifyManualWorktreeRelease(worktree)
		}, "worktree .git marker is not a regular file; refusing return"},
		{"block: harness launch file under a subdirectory", func(t *testing.T) error {
			return verifyManualWorktreeRelease(artifactWorktree(t, harness.PiProjectSettingsRelPath))
		}, `known or unanchored launch artifact "` + harness.PiProjectSettingsRelPath + `" is present; refusing return`},
		{"block: soldier launch guard file", func(t *testing.T) error {
			return verifyManualWorktreeRelease(artifactWorktree(t, ".soldier-launch-guard-abc"))
		}, `known or unanchored launch artifact ".soldier-launch-guard-abc" is present; refusing return`},
		{"block: untracked content", func(t *testing.T) error {
			_, worktree := gitOrphanWorktree(t)
			if err := os.WriteFile(filepath.Join(worktree, "user-notes.txt"), []byte("protected\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return verifyManualWorktreeRelease(worktree)
		}, "worktree contains dirty, untracked, or ignored content; refusing return"},
		{"allow: clean canonical orphan", func(t *testing.T) error {
			_, worktree := gitOrphanWorktree(t)
			return verifyManualWorktreeRelease(worktree)
		}, ""},
		{"block: provider lists an empty path to the claim scan", func(t *testing.T) error {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			initCLITestHome(t, homeDir)
			_, _, err := activeWorktreeClaims(homeDir, []backend.WorktreeEntry{{Path: ""}})
			return err
		}, "provider status contains an empty worktree path"},
		{"block: live reservation without a project", func(t *testing.T) error {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			initCLITestHome(t, homeDir)
			auth := testAuthorityFor(t, homeDir)
			taskID := mustTaskIDFor(t, "guard-no-project")
			projectID, err := domain.NewProjectID("guard-project")
			if err != nil {
				t.Fatal(err)
			}
			create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "guard reservation", Kind: "ship", Project: projectID, Reason: "guard test"}
			if _, err := auth.Create(mustCanonicalOp(t, "guard-create", create), create); err != nil {
				t.Fatal(err)
			}
			agg, err := auth.Get(taskID)
			if err != nil {
				t.Fatal(err)
			}
			launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("d", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: "", LaunchID: "launch-guard", WindowLabel: "window-guard", WorktreeReservationID: "wt-guard-no-project", WorktreeFenceToken: "fence-guard", EndpointReservationID: "ep-guard", EndpointFenceToken: "ep-fence-guard", SeatPathDirs: []string{"/usr/bin"}, EndpointIncarnation: "ep-inc-guard", Reason: "guard test"}
			launch.SeatEnv = taskauthority.SeatEnv{TempDir: "/tmp", UserHome: "/home/tester"}
			if _, err := auth.BeginSpawn(mustCanonicalOp(t, "guard-launch", launch), launch); err != nil {
				t.Fatal(err)
			}
			_, _, err = activeWorktreeClaims(homeDir, nil)
			return err
		}, `launch reservation "wt-guard-no-project" has no canonical project identity`},
		{"allow: provider lists a worktree no task claims", func(t *testing.T) error {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			initCLITestHome(t, homeDir)
			_, _, err := activeWorktreeClaims(homeDir, []backend.WorktreeEntry{{Path: path}})
			return err
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("error = %v, want allowed", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("error = %v, want %q", err, tc.refused)
			}
		})
	}
}

// activeWorktreeClaims claims the worktree a task's current generation binds
// and releases it once retirement cleanup has completed, and an unreadable
// task document fails closed.
func TestActiveWorktreeClaimsReadCanonicalBindings(t *testing.T) {
	const worktree = "/pool/wt-claimed"
	for _, tc := range []struct {
		name    string
		seed    func(t *testing.T, homeDir string)
		claimed bool
		wantErr string
	}{
		{"blocks a worktree a live binding holds", func(t *testing.T, homeDir string) {
			bindClaimWorktree(t, homeDir, "live", worktree, false)
		}, true, ""},
		{"allows a worktree whose retirement cleanup completed", func(t *testing.T, homeDir string) {
			bindClaimWorktree(t, homeDir, "released", worktree, true)
		}, false, ""},
		{"fails closed on an unreadable task document", func(t *testing.T, homeDir string) {
			bindClaimWorktree(t, homeDir, "live", worktree, false)
			if err := os.WriteFile(filepath.Join(homeDir, "state", "task-authority", "tasks", "live", "current.json"), []byte("{corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false, "listing task authority in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			initCLITestHome(t, homeDir)
			tc.seed(t, homeDir)
			active, _, err := activeWorktreeClaims(homeDir, []backend.WorktreeEntry{{Path: worktree}})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("activeWorktreeClaims = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || active[worktreeClaimKey(worktree)] != tc.claimed {
				t.Fatalf("activeWorktreeClaims = %v, %v, want claimed=%v", active, err, tc.claimed)
			}
		})
	}
}

// bindClaimWorktree binds id's worktree at path under a launch's reservation.
// With release set the task is retired with its retirement cleanup completed.
func bindClaimWorktree(t *testing.T, homeDir, id, path string, release bool) {
	t.Helper()
	const project = "claim-project"
	if _, err := fleet.Find(homeDir, project); err != nil {
		registerProject(t, homeDir, project)
	}
	auth := cliCanonicalForHome(t, homeDir)
	taskID := mustTaskIDFor(t, id)
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "claim", Kind: "ship", Project: mustCLIProjectID(t, project), Reason: "claim test"}
	if _, err := auth.Create(mustCanonicalOp(t, "claim-create-"+id, create), create); err != nil {
		t.Fatal(err)
	}
	precondition := func() domain.Precondition {
		agg, err := auth.Get(taskID)
		if err != nil {
			t.Fatal(err)
		}
		return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	}
	launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, SnapshotDigest: strings.Repeat("d", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: project, LaunchID: "launch-" + id, WindowLabel: "window-" + id, WorktreeReservationID: "wt-" + id, WorktreeFenceToken: "fence-" + id, EndpointReservationID: "ep-" + id, EndpointFenceToken: "ep-fence-" + id, SeatPathDirs: []string{"/usr/bin"}, EndpointIncarnation: "ep-inc-" + id, Reason: "claim test"}
	launch.SeatEnv = taskauthority.SeatEnv{TempDir: "/tmp", UserHome: "/home/tester"}
	launch.Precondition = precondition()
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "claim-launch-"+id, launch), launch); err != nil {
		t.Fatal(err)
	}
	bind := taskauthority.CanonicalBindWorktreeRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: precondition(), Reason: "claim test", Binding: taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo", Path: path, GitDir: path + "/.git", CommonDir: "/repo/.git", BaseHead: strings.Repeat("a", 40), LeaseID: launch.WorktreeReservationID, FenceToken: launch.WorktreeFenceToken, BoundAtUnix: 1,
	}}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "claim-bind-"+id, bind), bind); err != nil {
		t.Fatal(err)
	}
	if !release {
		return
	}
	retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: precondition(), Reason: "claim test"}
	if _, err := auth.Retire(mustCanonicalOp(t, "claim-retire-"+id, retire), retire); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ReconcileRetirementCleanup(taskID, agg.Generation, taskauthority.CleanupCompleted, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
