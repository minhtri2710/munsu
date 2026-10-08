package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestReconciledOrphanWorktreeRefusesEmptyProviderPath(t *testing.T) {
	_, err := reconciledOrphanWorktree("/some/worktree", []backend.WorktreeEntry{{Path: ""}})
	if err == nil || !strings.Contains(err.Error(), "provider status contains an empty worktree path; refusing return") {
		t.Fatalf("reconciledOrphanWorktree error = %v, want empty provider path refusal", err)
	}
}

func TestReconciledOrphanWorktreeRefusesDuplicateListing(t *testing.T) {
	path := "/some/worktree"
	_, err := reconciledOrphanWorktree(path, []backend.WorktreeEntry{{Path: path}, {Path: path}})
	if err == nil || !strings.Contains(err.Error(), "lists worktree") || !strings.Contains(err.Error(), "more than once; refusing return") {
		t.Fatalf("reconciledOrphanWorktree error = %v, want duplicate listing refusal", err)
	}
}

func TestReconciledOrphanWorktreeRefusesUnreconciledLeaseHolder(t *testing.T) {
	path := "/some/worktree"
	_, err := reconciledOrphanWorktree(path, []backend.WorktreeEntry{{Path: path, LeaseHolder: "wt-unknown"}})
	if err == nil || !strings.Contains(err.Error(), `lease holder "wt-unknown" is not reconciled; refusing return`) {
		t.Fatalf("reconciledOrphanWorktree error = %v, want unreconciled lease holder refusal", err)
	}
}

func TestVerifyManualWorktreeReleaseRefusesRelativePath(t *testing.T) {
	err := verifyManualWorktreeRelease("relative/worktree")
	if err == nil || !strings.Contains(err.Error(), "worktree path must be absolute for ownership verification") {
		t.Fatalf("verifyManualWorktreeRelease error = %v, want absolute path refusal", err)
	}
}

func TestVerifyManualWorktreeReleaseRefusesDirectoryGitMarker(t *testing.T) {
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := verifyManualWorktreeRelease(worktree)
	if err == nil || !strings.Contains(err.Error(), "worktree .git marker is not a regular file; refusing return") {
		t.Fatalf("verifyManualWorktreeRelease error = %v, want non-regular .git marker refusal", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		t.Fatalf("directory .git marker was disturbed: %v", err)
	}
}

func TestVerifyManualWorktreeReleaseRefusesSoldierLaunchGuardFile(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join(worktree, ".soldier-launch-guard-abc")
	if err := os.WriteFile(guard, []byte("held\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyManualWorktreeRelease(worktree)
	if err == nil || !strings.Contains(err.Error(), `known or unanchored launch artifact ".soldier-launch-guard-abc" is present; refusing return`) {
		t.Fatalf("verifyManualWorktreeRelease error = %v, want launch guard artifact refusal", err)
	}
	if got, err := os.ReadFile(guard); err != nil || string(got) != "held\n" {
		t.Fatalf("launch guard file = %q, err=%v; refused release must not remove it", got, err)
	}
}

func TestActiveWorktreeClaimsRefusesEmptyProviderPath(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
	_, _, err := activeWorktreeClaims(homeDir, []backend.WorktreeEntry{{Path: ""}})
	if err == nil || !strings.Contains(err.Error(), "provider status contains an empty worktree path") {
		t.Fatalf("activeWorktreeClaims error = %v, want empty provider path refusal", err)
	}
}

func TestActiveWorktreeClaimsRefusesLiveReservationWithoutProject(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), `launch reservation "wt-guard-no-project" has no canonical project identity`) {
		t.Fatalf("activeWorktreeClaims error = %v, want missing project identity refusal", err)
	}
}
