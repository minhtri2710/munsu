package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
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
			launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("d", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: "", LaunchID: "launch-guard", WindowLabel: "window-guard", WorktreeReservationID: "wt-guard-no-project", WorktreeFenceToken: "fence-guard", EndpointReservationID: "ep-guard", EndpointFenceToken: "ep-fence-guard", EndpointIncarnation: "ep-inc-guard", Reason: "guard test"}
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
