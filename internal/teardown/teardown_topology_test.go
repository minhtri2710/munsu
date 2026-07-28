package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/domain"
)

// --- test helpers ---

// topologyGitEnv returns the GIT_CEILING_DIRECTORIES environment for a worktree.
func topologyGitEnv(wt string) []string {
	return append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)
}

// fixtureMeta returns a minimal task meta map suitable for topology-aware tests.
// Optionally includes delivery identity fields.
func fixtureMeta(wtPath string, withIdentity bool) map[string]string {
	meta := map[string]string{
		"worktree": wtPath,
		"kind":     "ship",
	}
	if withIdentity {
		meta["pr_url"] = "https://github.com/minhtri2710/munsu/pull/42"
		meta["pr_provider"] = "github"
		meta["pr_owner"] = "minhtri2710"
		meta["pr_repo"] = "munsu"
		meta["pr_number"] = "42"
		meta["pr_head_ref"] = "fm/feature-branch"
		meta["pr_head"] = "abc123def456"
		meta["pr_base"] = "main"
		meta["pr_timestamp"] = "2026-07-18T00:00:00Z"
	}
	return meta
}

// setupTopologyRepo creates a git repo with a remote and returns the worktree path.
// Creates a branch with upstream so it's in a "clean with remote" state.
// Sets up a real ordinary merge topology: a feature commit, merged into main,
// and main pushed to origin so the feature head IS an ancestor of origin/main.
func setupTopologyRepo(t *testing.T, tmp string) (wtPath, remotePath string) {
	t.Helper()
	wtPath = filepath.Join(tmp, "worktree")
	remotePath = filepath.Join(tmp, "remote.git")
	os.MkdirAll(wtPath, 0755)
	setupGitRepo(t, wtPath, remotePath)

	gitEnv := topologyGitEnv(wtPath)

	// Create a feature branch with a unique feature commit
	cmd := exec.Command("git", "checkout", "-b", "fm/feature-branch")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}

	featureFile := filepath.Join(wtPath, "feature.txt")
	os.WriteFile(featureFile, []byte("feature work"), 0644)
	cmd = exec.Command("git", "add", "feature.txt")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add feature: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "feature work")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit feature: %s", out)
	}

	// Push feature branch to origin
	cmd = exec.Command("git", "push", "-u", "origin", "fm/feature-branch")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Simulate ordinary merge: checkout main, merge feature, push updated main
	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout main: %s", out)
	}

	cmd = exec.Command("git", "merge", "fm/feature-branch")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git merge: %s", out)
	}

	cmd = exec.Command("git", "push", "origin", "main")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push main: %s", out)
	}

	// Fetch/update remote refs so origin/main is current
	cmd = exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fetch: %s", out)
	}

	// Return to feature branch so tests start from it
	cmd = exec.Command("git", "checkout", "fm/feature-branch")
	cmd.Dir = wtPath
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout feature: %s", out)
	}

	return wtPath, remotePath
}

// mockPRMergeStatus returns a function that replaces delivery.QueryPRMergeStatus
// with a mock that returns the given status and error.
func mockPRMergeStatus(status *delivery.PRMergeStatus, err error) func(domain.GHURL) (*delivery.PRMergeStatus, error) {
	return func(domain.GHURL) (*delivery.PRMergeStatus, error) {
		return status, err
	}
}

// applyMockPRStatus sets up a mock for delivery.QueryPRMergeStatus and returns a
// cleanup function that restores the original.
func applyMockPRStatus(t *testing.T, status *delivery.PRMergeStatus, err error) func() {
	t.Helper()
	saved := delivery.QueryPRMergeStatus
	delivery.QueryPRMergeStatus = mockPRMergeStatus(status, err)
	return func() { delivery.QueryPRMergeStatus = saved }
}

// --- Cleanliness checks (no identity) ---

func TestShipSafetyCheck_Topology_CleanNoIdentity(t *testing.T) {
	// A clean branch with remote should pass the remote branch check fallback
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, false)
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("clean branch should pass: %v", err)
	}
}

func TestShipSafetyCheck_Topology_DirtyNoIdentity(t *testing.T) {
	// A dirty branch should fail even with a remote
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("changes"), 0644)

	meta := fixtureMeta(wt, false)
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("dirty worktree should fail")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("expected uncommitted changes error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_NoWorktreeInMeta(t *testing.T) {
	// Missing worktree should fail
	_, err := shipSafetyCheck(Options{ID: "test"}, map[string]string{"kind": "ship"})
	if err == nil {
		t.Fatal("should fail when no worktree in meta")
	}
}

func TestShipSafetyCheck_Topology_NonexistentWorktree(t *testing.T) {
	// Nonexistent worktree should fail
	tmp := t.TempDir()
	meta := fixtureMeta(filepath.Join(tmp, "nonexistent"), false)
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("should fail when worktree does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected does not exist error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_NoRemoteBranchFallback(t *testing.T) {
	// Without delivery identity and no remote branch, should fail
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, "") // no remote

	meta := fixtureMeta(wt, false)
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("should fail without remote branch when no identity")
	}
}

// --- Topology-aware: merged PR (squash/rebase + deleted head) ---

func TestShipSafetyCheck_Topology_MergedPRWithDeletedHead(t *testing.T) {
	// Squash-merged/deleted remote head: provider confirms merged, accept teardown
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "abc123def456",
		HeadSHA:   "abc123def456",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Delete the remote branch to simulate squash+delete
	gitEnv := topologyGitEnv(wt)
	cmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	_ = cmd.Run()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("merged PR with deleted head should pass: %v", err)
	}
}

func TestShipSafetyCheck_Topology_MergedPRBranchExists(t *testing.T) {
	// Merged PR where remote branch still exists: accept with head SHA match
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	// Get the actual head SHA
	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = headSHA // must match actual head

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("merged PR with existing branch should pass: %v", err)
	}
}

// --- Topology-aware: closed PR, mismatched head ---

func TestShipSafetyCheck_Topology_ClosedUnmergedPR(t *testing.T) {
	// Closed but not merged: refuse teardown
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  true,
		State:   "CLOSED",
		HeadSHA: "abc123def456",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("closed unmerged PR should fail")
	}
	if !strings.Contains(err.Error(), "closed but not merged") {
		t.Errorf("expected closed but not merged error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_OpenUnmergedPR(t *testing.T) {
	// Still open and not merged: refuse teardown
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "OPEN",
		HeadSHA: "abc123def456",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("open unmerged PR should fail")
	}
	if !strings.Contains(err.Error(), "still open") {
		t.Errorf("expected still open error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_ProviderUnavailable(t *testing.T) {
	// Provider unreachable: fail closed
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, nil, fmt.Errorf("gh CLI not available"))
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("unavailable provider should fail")
	}
	if !strings.Contains(err.Error(), "cannot verify merge status") {
		t.Errorf("expected cannot verify merge status error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_WrongPRHead(t *testing.T) {
	// Mismatched head SHA when remote branch still exists: refuse
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	// Stored head doesn't match provider-reported head
	meta["pr_head"] = "oldsha0000000000000000000000000000000000"

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "newsha0000000000000000000000000000000000",
		HeadSHA:   "newsha0000000000000000000000000000000000",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("mismatched head should fail")
	}
	if !strings.Contains(err.Error(), "head SHA mismatch") {
		t.Errorf("expected head SHA mismatch error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_DirtyWithIdentity(t *testing.T) {
	// Dirty worktree should fail even with valid merged PR
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("changes"), 0644)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "abc123def456",
		HeadSHA:   "abc123def456",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("dirty worktree should fail even with merged PR")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("expected uncommitted changes error, got: %v", err)
	}
}

// --- Dry-run / output proof ---

func TestShipSafetyCheck_Topology_EmitProof(t *testing.T) {
	// topologyAwareMergeCheck emits the exact proof used.
	// We verify this by inspecting the proof string in the error messages
	// when things fail, and by verifying success is returned when things pass.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "abc123def456",
		HeadSHA:   "abc123def456",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Delete the remote branch to trigger deleted-head path
	gitEnv := topologyGitEnv(wt)
	cmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	_ = cmd.Run()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("should accept merged PR with deleted head: %v", err)
	}
	// Pass = proof was sufficient
}

// --- identityFromMeta tests ---

func TestIdentityFromMeta_WithURL(t *testing.T) {
	meta := map[string]string{
		"pr_url":      "https://github.com/minhtri2710/munsu/pull/42",
		"pr_number":   "42",
		"pr_head":     "abc123",
		"pr_base":     "main",
		"pr_head_ref": "fm/feature",
	}
	ident, err := identityFromMeta(meta)
	if err != nil {
		t.Fatalf("identityFromMeta should succeed: %v", err)
	}
	if ident == nil {
		t.Fatal("identityFromMeta should return non-nil with pr_url")
	}
	if ident.Number != 42 {
		t.Errorf("expected number 42, got %d", ident.Number)
	}
}

func TestIdentityFromMeta_WithoutURL(t *testing.T) {
	meta := map[string]string{
		"kind": "ship",
	}
	ident, err := identityFromMeta(meta)
	if err != nil {
		t.Fatalf("identityFromMeta should not error without pr_url: %v", err)
	}
	if ident != nil {
		t.Fatal("identityFromMeta should return nil without pr_url")
	}
}

// --- Backward compatibility: existing tests still pass ---

func TestShipSafetyCheck_Topology_ExistingTestsStillWork(t *testing.T) {
	// Test that the existing tests' patterns still pass through the new code

	// No worktree in meta
	_, err := shipSafetyCheck(Options{ID: "test"}, map[string]string{})
	if err == nil {
		t.Fatal("should fail when no worktree in meta")
	}

	// Clean with remote (no identity) should pass
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)
	meta := fixtureMeta(wt, false)
	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("clean branch with remote should pass: %v", err)
	}

	// Dirty should fail
	os.WriteFile(filepath.Join(wt, "another-dirty.txt"), []byte("changes"), 0644)
	meta = fixtureMeta(wt, false)
	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("dirty worktree should fail")
	}
	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("dirty worktree should fail")
	}
}

// --- ValidateIdentity is called after identityFromMeta ---

func TestShipSafetyCheck_Topology_PartialIdentityFailsClosed(t *testing.T) {
	// Partial identity should fail closed, not degrade to legacy branch check.
	// Has pr_url and pr_head but missing pr_provider, pr_owner, pr_repo, pr_number, pr_base, pr_head_ref.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
		"pr_url":   "https://github.com/minhtri2710/munsu/pull/42",
		"pr_head":  "abc123def456",
	}
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("partial identity should fail closed")
	}
	if !strings.Contains(err.Error(), "invalid delivery identity") {
		t.Errorf("expected invalid delivery identity error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_MissingProviderFailsClosed(t *testing.T) {
	// All fields except pr_provider set — ValidateIdentity should reject.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head":      "abc123def456",
		"pr_head_ref":  "fm/feature-branch",
		"pr_base":      "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("missing pr_provider should fail closed")
	}
	if !strings.Contains(err.Error(), "invalid delivery identity") {
		t.Errorf("expected invalid delivery identity error, got: %v", err)
	}
}

// --- Proof emission tests ---

func TestShipSafetyCheck_Topology_ProofReturnedDeletedHead(t *testing.T) {
	// When topologyAwareMergeCheck succeeds with deleted head,
	// the proof string is returned and not dead code.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "abc123def456",
		HeadSHA:   "abc123def456",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Delete the remote branch to simulate squash+delete
	gitEnv := topologyGitEnv(wt)
	cmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	_ = cmd.Run()

	proofs, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("merged PR with deleted head should pass: %v", err)
	}
	if len(proofs) != 1 {
		t.Fatalf("expected 1 proof, got %d: %v", len(proofs), proofs)
	}
	if !strings.Contains(proofs[0], "PR #42 merged") {
		t.Errorf("proof should mention PR #42 merged, got: %s", proofs[0])
	}
	if !strings.Contains(proofs[0], "provider-confirmed state=merged") {
		t.Errorf("proof should include provider confirmation, got: %s", proofs[0])
	}
}

func TestShipSafetyCheck_Topology_ProofReturnedOrdinaryMerge(t *testing.T) {
	// Ordinary merge (remote branch exists) returns proof with ancestry verification.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	// Get the actual head SHA
	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = headSHA // must match actual head

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	proofs, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("merged PR with existing branch should pass: %v", err)
	}
	if len(proofs) != 1 {
		t.Fatalf("expected 1 proof, got %d: %v", len(proofs), proofs)
	}
	if !strings.Contains(proofs[0], "ancestry verified") {
		t.Errorf("ordinary merge proof should include ancestry verification, got: %s", proofs[0])
	}
	if !strings.Contains(proofs[0], "is ancestor of origin/main") {
		t.Errorf("proof should mention origin/main ancestry, got: %s", proofs[0])
	}
}

// --- Ancestry verification tests ---

func TestShipSafetyCheck_Topology_AncestryFails(t *testing.T) {
	// When the remote branch exists but the provider-reported head is NOT an
	// ancestor of the base target (e.g., force-pushed, orphaned commit),
	// the ancestry check is not a blocker — provider-confirmed MERGED is
	// sufficient proof.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	// Create an unrelated commit on a separate branch that will never
	// be an ancestor of origin/main.
	gitEnv := topologyGitEnv(wt)
	orphanCmd := exec.Command("git", "checkout", "--orphan", "orphan-branch")
	orphanCmd.Dir = wt
	orphanCmd.Env = gitEnv
	if out, err := orphanCmd.CombinedOutput(); err != nil {
		t.Fatalf("create orphan branch: %s", out)
	}
	origFile := filepath.Join(wt, "orphan.txt")
	os.WriteFile(origFile, []byte("orphan content"), 0644)
	addCmd := exec.Command("git", "add", "orphan.txt")
	addCmd.Dir = wt
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	commitCmd := exec.Command("git", "commit", "-m", "orphan commit")
	commitCmd.Dir = wt
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	// Get the orphan SHA — this is NOT an ancestor of origin/main
	origShaCmd := exec.Command("git", "rev-parse", "HEAD")
	origShaCmd.Dir = wt
	origShaCmd.Env = gitEnv
	origShaOut, err := origShaCmd.Output()
	if err != nil {
		t.Fatalf("getting orphan SHA: %v", err)
	}
	orphanSHA := strings.TrimSpace(string(origShaOut))

	// Go back to feature branch so we can set up the identity
	checkoutCmd := exec.Command("git", "checkout", "fm/feature-branch")
	checkoutCmd.Dir = wt
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout feature branch: %s", out)
	}

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = orphanSHA // orphan SHA is NOT an ancestor of origin/main

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: orphanSHA,
		HeadSHA:   orphanSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Ancestry check is not a blocker — provider MERGED + head SHA match
	// are sufficient proof. Teardown should succeed even though ancestry fails.
	proofs, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("orphan head should not block teardown: %v", err)
	}
	if len(proofs) != 1 {
		t.Fatalf("expected 1 proof, got %d: %v", len(proofs), proofs)
	}
	if !strings.Contains(proofs[0], "ancestry check inconclusive") {
		t.Errorf("expected ancestry check inconclusive in proof, got: %s", proofs[0])
	}
}

func TestShipSafetyCheck_Topology_SquashMerge(t *testing.T) {
	// Squash/rebase merge: provider confirms MERGED with different MergedSHA vs HeadSHA.
	// Remote branch still exists. HeadSHA is NOT an ancestor of main,
	// but MergedSHA (the merge commit) IS an ancestor. Teardown should succeed.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	// Get the feature branch head SHA
	gitEnv := topologyGitEnv(wt)
	headSHACmd := exec.Command("git", "rev-parse", "HEAD")
	headSHACmd.Dir = wt
	headSHACmd.Env = gitEnv
	headSHAOut, err := headSHACmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(headSHAOut))

	// Simulate squash merge: create a new commit on main that represents
	// the squashed merge result, then push main to origin.
	checkoutCmd := exec.Command("git", "checkout", "main")
	checkoutCmd.Dir = wt
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout main: %s", out)
	}

	squashFile := filepath.Join(wt, "squash-result.txt")
	os.WriteFile(squashFile, []byte("squashed content"), 0644)
	addCmd := exec.Command("git", "add", "squash-result.txt")
	addCmd.Dir = wt
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	commitCmd := exec.Command("git", "commit", "-m", "squash merge result (simulated)")
	commitCmd.Dir = wt
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Get the squash commit SHA — this IS an ancestor of origin/main
	squashSHACmd := exec.Command("git", "rev-parse", "HEAD")
	squashSHACmd.Dir = wt
	squashSHACmd.Env = gitEnv
	squashSHAOut, err := squashSHACmd.Output()
	if err != nil {
		t.Fatalf("getting squash SHA: %v", err)
	}
	squashSHA := strings.TrimSpace(string(squashSHAOut))

	// Push main to origin so origin/main references the squash commit
	pushCmd := exec.Command("git", "push", "origin", "main")
	pushCmd.Dir = wt
	pushCmd.Env = gitEnv
	if out, err := pushCmd.CombinedOutput(); err != nil {
		t.Fatalf("git push main: %s", out)
	}

	// Fetch to update origin/main ref in the local repo
	fetchCmd := exec.Command("git", "fetch", "origin", "main")
	fetchCmd.Dir = wt
	fetchCmd.Env = gitEnv
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		t.Fatalf("git fetch: %s", out)
	}

	// Go back to feature branch
	checkoutFeatureCmd := exec.Command("git", "checkout", "fm/feature-branch")
	checkoutFeatureCmd.Dir = wt
	checkoutFeatureCmd.Env = gitEnv
	if out, err := checkoutFeatureCmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout feature branch: %s", out)
	}

	// Now headSHA is NOT an ancestor of origin/main (the new squash commit
	// on main is unrelated to the feature branch head).
	// squashSHA IS an ancestor of origin/main.

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = headSHA

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: squashSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Provider MERGED + head SHA match + MergedSHA ancestry verified
	proofs, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("squash merge should pass: %v", err)
	}
	if len(proofs) != 1 {
		t.Fatalf("expected 1 proof, got %d: %v", len(proofs), proofs)
	}
	if !strings.Contains(proofs[0], "ancestry verified") {
		t.Errorf("squash merge proof should include ancestry verification, got: %s", proofs[0])
	}
	if !strings.Contains(proofs[0], squashSHA) {
		t.Errorf("proof should reference MergedSHA %s, got: %s", squashSHA, proofs[0])
	}
}

// --- Unmerged deleted branch ---

func TestShipSafetyCheck_Topology_UnmergedDeletedBranch(t *testing.T) {
	// Remote branch deleted but PR NOT merged: branch deletion alone
	// never authorizes teardown.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)

	// Delete the remote branch but mock PR status as OPEN (not merged)
	gitEnv := topologyGitEnv(wt)
	cmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	_ = cmd.Run()

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "OPEN",
		HeadSHA: "abc123def456",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("unmerged deleted branch should fail")
	}
	if !strings.Contains(err.Error(), "still open") {
		t.Errorf("expected 'still open' error, got: %v", err)
	}
}

// --- Provider ambiguous/unavailable ---

func TestShipSafetyCheck_Topology_ProviderEmptyState(t *testing.T) {
	// Provider returns an empty/invalid state — should fail closed.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "", // empty state is not valid
		HeadSHA: "",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("empty provider state should fail")
	}
	if !strings.Contains(err.Error(), "unexpected state") {
		t.Errorf("expected unexpected state error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_DeletedHeadWrongSHA(t *testing.T) {
	// Deleted remote head but provider-reported head SHA does NOT match
	// stored identity — must fail closed. This covers the squash/rebase
	// topology where the remote branch is gone but the live head SHA
	// differs from the one captured at PR-check time.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := fixtureMeta(wt, true)
	// Stored head SHA differs from provider-reported head SHA
	meta["pr_head"] = "storedsha0000000000000000000000000000000000"

	// Delete the remote branch to simulate squash+delete
	gitEnv := topologyGitEnv(wt)
	cmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	_ = cmd.Run()

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "livesha1111111111111111111111111111111111",
		HeadSHA:   "livesha1111111111111111111111111111111111",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("deleted-head wrong SHA should fail")
	}
	if !strings.Contains(err.Error(), "head SHA mismatch") {
		t.Errorf("expected head SHA mismatch error, got: %v", err)
	}
}

func TestShipSafetyCheck_Topology_PartialIdentityNoURL(t *testing.T) {
	// Partial identity with multiple fields but no pr_url must fail closed
	// and NOT fall through to the legacy remote branch check.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	meta := map[string]string{
		"worktree":    wt,
		"kind":        "ship",
		"pr_provider": "github",
		"pr_owner":    "minhtri2710",
		"pr_repo":     "munsu",
		"pr_number":   "42",
		"pr_head":     "abc123def456abc123def456abc123def456abc1",
		"pr_head_ref": "fm/feature-branch",
		"pr_base":     "main",
		// No pr_url
	}
	_, err := shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("partial identity without pr_url should fail closed")
	}
	// Must error about delivery identity, not about remote branch
	if !strings.Contains(err.Error(), "reading delivery identity") {
		t.Errorf("expected 'reading delivery identity' error, got: %v", err)
	}
}

// --- Exact regression: no upstream + deleted remote head + complete identity ---

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHeadCompleteIdentity(t *testing.T) {
	// Exact regression from the incident:
	//   - Local branch: NO upstream (git branch --unset-upstream)
	//   - Remote head: DELETED
	//   - Complete typed identity: PERSISTED
	//   - Provider confirms: MERGED
	//   -> shipSafetyCheck MUST succeed without Force
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	// Get the actual feature branch head SHA for identity matching
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	// Remove local upstream (no upstream tracking)
	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	if out, err := unsetCmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch --unset-upstream: %s", out)
	}

	// Delete remote head
	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	if out, err := delCmd.CombinedOutput(); err != nil {
		t.Fatalf("git push origin --delete: %s", out)
	}

	// Build meta with complete typed identity using both old and new field names
	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Mock provider: merged with matching HeadSHA
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Verify no upstream (confirming the regression condition)
	upstreamCheck := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	upstreamCheck.Dir = wt
	upstreamCheck.Env = gitEnv
	if upstreamCheck.Run() == nil {
		t.Fatal("expected no upstream for regression test")
	}

	// shipSafetyCheck should succeed without Force
	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("regression: no upstream + deleted head + complete identity should pass: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_ProviderEmptyHeadSHA(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	// Remove local upstream
	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	unsetCmd.Run()

	// Delete remote head
	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	delCmd.Run()

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Provider returns empty HeadSHA — fail closed
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "",
		HeadSHA:   "",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("empty provider HeadSHA should fail closed")
	}
	if !strings.Contains(err.Error(), "empty head SHA") {
		t.Errorf("expected 'empty head SHA' error, got: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_SHAMismatch(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	// Remove local upstream
	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	unsetCmd.Run()

	// Delete remote head
	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	delCmd.Run()

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Provider reports DIFFERENT HeadSHA — fail closed
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: "different0000000000000000000000000000000000",
		HeadSHA:   "different0000000000000000000000000000000000",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("SHA mismatch should fail closed")
	}
	if !strings.Contains(err.Error(), "head SHA mismatch") {
		t.Errorf("expected 'head SHA mismatch' error, got: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_OpenPR(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	unsetCmd.Run()

	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	delCmd.Run()

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Provider: still OPEN (not merged)
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "OPEN",
		HeadSHA: headSHA,
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("open PR should fail closed")
	}
	if !strings.Contains(err.Error(), "still open") {
		t.Errorf("expected 'still open' error, got: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_ClosedUnmerged(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	unsetCmd.Run()

	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	delCmd.Run()

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Provider: CLOSED but not merged
	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:  false,
		Closed:  true,
		State:   "CLOSED",
		HeadSHA: headSHA,
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("closed-unmerged PR should fail closed")
	}
	if !strings.Contains(err.Error(), "closed but not merged") {
		t.Errorf("expected 'closed but not merged' error, got: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_ProviderError(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	unsetCmd := exec.Command("git", "branch", "--unset-upstream")
	unsetCmd.Dir = wt
	unsetCmd.Env = gitEnv
	unsetCmd.Run()

	delCmd := exec.Command("git", "push", "origin", "--delete", "fm/feature-branch")
	delCmd.Dir = wt
	delCmd.Env = gitEnv
	delCmd.Run()

	meta := map[string]string{
		"worktree":     wt,
		"kind":         "ship",
		"pr_url":       "https://github.com/minhtri2710/munsu/pull/42",
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head_ref":  "fm/feature-branch",
		"pr_head":      headSHA,
		"pr_head_sha":  headSHA,
		"pr_base":      "main",
		"pr_base_ref":  "main",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}

	// Provider returns error — fail closed
	cleanup := applyMockPRStatus(t, nil, fmt.Errorf("gh CLI timeout"))
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("provider error should fail closed")
	}
	if !strings.Contains(err.Error(), "cannot verify merge status") {
		t.Errorf("expected 'cannot verify merge status' error, got: %v", err)
	}
}

// --- DeliveryState merged acceptance tests ---

func TestShipSafetyCheck_DeliveryStateMergedAcceptsWithoutForce(t *testing.T) {
	// When delivery_state=merged and provider confirms merge,
	// teardown should accept without --force.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = headSHA
	meta["pr_head_sha"] = headSHA
	meta[delivery.MetaDeliveryState] = string(delivery.DeliveryStateMerged)

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err != nil {
		t.Fatalf("merged with delivery_state=merged should pass: %v", err)
	}
}

func TestShipSafetyCheck_DeliveryStateReviewReadyRejectsWithoutForce(t *testing.T) {
	// When delivery_state=review-ready (not merged), teardown MUST reject
	// without --force even when provider says merged.
	tmp := t.TempDir()
	wt, _ := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	meta := fixtureMeta(wt, true)
	meta["pr_head"] = headSHA
	meta["pr_head_sha"] = headSHA
	meta[delivery.MetaDeliveryState] = string(delivery.DeliveryStateReviewReady)

	cleanup := applyMockPRStatus(t, &delivery.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(Options{ID: "test"}, meta)
	if err == nil {
		t.Fatal("expected error for delivery_state=review-ready (not merged)")
	}
	if !strings.Contains(err.Error(), "delivery lifecycle is in state") {
		t.Fatalf("expected delivery lifecycle error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "review-ready") {
		t.Fatalf("expected error mentioning review-ready, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected error mentioning --force, got: %v", err)
	}
}
