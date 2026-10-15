//go:build integration

package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// --- test helpers ---

// setupTopologyManifest writes canonical launch artifacts and the manifest
// into the worktree. Returns the manifest digest for use in meta.
// contractedMergeCheckOptions is the Options of a merge check over a task home
// whose task carries a captured github forge step, the state the check reads.
func contractedMergeCheckOptions(t *testing.T, taskID string) Options {
	t.Helper()
	c, homeDir := newFleetCanonical(t)
	seedCanonicalQueuedTask(t, c, taskID, "general")
	seedSourceContract(t, c, taskID, deliveryTestGitHubForge)
	return Options{HomeDir: homeDir, ID: taskID}
}

func setupTopologyManifest(t *testing.T, wt string) string {
	t.Helper()
	taskID, launchID, endpointFence := "topology-test", "launch-topology-test", "endpoint-fence-test"
	charter := DefaultCharter("topology-test", "ship", "direct-PR", "")
	brief := []byte("# Topology test brief\n")
	prompt := "prompt"
	os.WriteFile(filepath.Join(wt, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(wt, BriefName), brief, 0644)
	os.WriteFile(filepath.Join(wt, PromptName), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(wt, EnvelopeName), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(wt, LaunchScriptName), []byte("#!/bin/bash\n"), 0644)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, wt, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, wt, taskID, launchID, "1", endpointFence))
	manifest := BuildManifest(entries)
	data, digest, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshaling manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ManifestName), data, 0644); err != nil {
		t.Fatalf("writing manifest fixture: %v", err)
	}
	return digest
}

// topologyGitEnv returns the GIT_CEILING_DIRECTORIES environment for a worktree.
func topologyGitEnv(wt string) []string {
	return append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)
}

// fixtureIdentity returns the delivery identity topology-aware tests check
// against, the shape a delivery authorization carries.
func fixtureIdentity() *domain.DeliveryIdentity {
	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "fm/feature-branch",
		HeadSHA:    "abc123def456",
		CapturedAt: "2026-07-18T00:00:00Z",
	}
}

// setupTopologyRepo creates a git repo with a remote and returns the worktree path
// and manifest digest.
// Creates a branch with upstream so it's in a "clean with remote" state.
// Sets up a real ordinary merge topology: a feature commit, merged into main,
// and main pushed to origin so the feature head IS an ancestor of origin/main.
func setupTopologyRepo(t *testing.T, tmp string) (wtPath, remotePath, manifestDigest string) {
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

	// Set up launch artifacts and manifest.
	manifestDigest = setupTopologyManifest(t, wtPath)
	return wtPath, remotePath, manifestDigest
}

// mockPRMergeStatus returns a function that replaces QueryDeliveryMergeStatus
// with a mock that returns the given status and error.
func mockPRMergeStatus(status *domain.PRMergeStatus, err error) func(taskauthority.DeliveryStep, *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return func(taskauthority.DeliveryStep, *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		return status, err
	}
}

// applyMockPRStatus sets up a mock for QueryDeliveryMergeStatus and returns a
// cleanup function that restores the original.
func applyMockPRStatus(t *testing.T, status *domain.PRMergeStatus, err error) func() {
	t.Helper()
	saved := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = mockPRMergeStatus(status, err)
	return func() { QueryDeliveryMergeStatus = saved }
}

// --- Cleanliness checks (no identity) ---

func dirtyTreeAllow(t *testing.T) {
	// A clean branch with remote should pass the remote branch check fallback
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	if _, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false); err != nil {
		t.Fatalf("clean branch should pass from canonical path/digest: %v", err)
	}
}

func dirtyTreeBlock(t *testing.T) {
	// A dirty branch should fail even with a remote
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("changes"), 0644)

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("dirty worktree should fail")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("expected uncommitted changes error, got: %v", err)
	}
}

// localOnlyLandingCheck runs the ship safety check of a local-only task after
// merging its branch into main when merged; origin is a bare remote holding the
// pushed branch when pushed, and absent otherwise.
func localOnlyLandingCheck(t *testing.T, merged, pushed bool) error {
	t.Helper()
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	os.MkdirAll(wt, 0o755)
	remote := ""
	if pushed {
		remote = filepath.Join(tmp, "remote.git")
	}
	setupGitRepo(t, wt, remote)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wt
		cmd.Env = topologyGitEnv(wt)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("checkout", "-b", "mu/local")
	git("commit", "--allow-empty", "-m", "work")
	if merged {
		git("checkout", "main")
		git("merge", "--no-ff", "-m", "land", "mu/local")
		git("checkout", "mu/local")
	}
	if pushed {
		git("push", "origin", "mu/local")
	}
	md := setupTopologyManifest(t, wt)
	c, homeDir := newFleetCanonical(t)
	seedCanonicalQueuedTask(t, c, "local", "general")
	seedSourceContract(t, c, "local", taskauthority.DeliveryStep{Baseline: true, ProbeState: "baseline"})
	_, err := shipSafetyCheck(Options{HomeDir: homeDir, ID: "local"}, nil, fakeTeardown{}, c, testWorktreeBinding(wt, md), false)
	return err
}

func localOnlyUnlandedBlock(t *testing.T) {
	if err := localOnlyLandingCheck(t, false, false); err == nil || !strings.Contains(err.Error(), "worktree head is not merged into main") {
		t.Fatalf("unlanded local-only task: err = %v, want not-merged refusal", err)
	}
}

func localOnlyLandedAllow(t *testing.T) {
	if err := localOnlyLandingCheck(t, true, false); err != nil {
		t.Fatalf("landed local-only task without origin: %v", err)
	}
}

func localOnlyPushedUnmergedAllow(t *testing.T) {
	if err := localOnlyLandingCheck(t, false, true); err != nil {
		t.Fatalf("local-only task pushed to origin but not merged: %v", err)
	}
}

func TestShipSafetyCheck_Topology_NoWorktreeInMeta(t *testing.T) {
	// Missing canonical worktree binding should fail.
	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), nil, fakeTeardown{}, nil, nil, false)
	if err == nil {
		t.Fatal("should fail when no canonical worktree binding exists")
	}
}

func TestShipSafetyCheck_Topology_NonexistentWorktree(t *testing.T) {
	// Nonexistent worktree should fail
	tmp := t.TempDir()
	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), nil, fakeTeardown{}, nil, testWorktreeBinding(filepath.Join(tmp, "nonexistent"), strings.Repeat("0", 64)), false)
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

	md := setupTopologyManifest(t, wt)
	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("should fail without remote branch when no identity")
	}
}

// --- Topology-aware: merged PR (squash/rebase + deleted head) ---

func TestShipSafetyCheck_Topology_MergedPRWithDeletedHead(t *testing.T) {
	// Squash-merged/deleted remote head: provider confirms merged, accept teardown
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: "0123456789abcdef0123456789abcdef01234567",
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

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("merged PR with deleted head should pass: %v", err)
	}
}

func mergedMatchingHeadAllow(t *testing.T) {
	// Merged PR where remote branch still exists: accept with head SHA match
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA // must match actual head

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("merged PR with existing branch should pass: %v", err)
	}
}

// --- Topology-aware: closed PR, mismatched head ---

func closedUnmergedBlock(t *testing.T) {
	// Closed but not merged: refuse teardown
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:  false,
		Closed:  true,
		State:   "CLOSED",
		HeadSHA: "abc123def456",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("closed unmerged PR should fail")
	}
	if !strings.Contains(err.Error(), "closed but not merged") {
		t.Errorf("expected closed but not merged error, got: %v", err)
	}
}

func openUnmergedBlock(t *testing.T) {
	// Still open and not merged: refuse teardown
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "OPEN",
		HeadSHA: "abc123def456",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, nil, fmt.Errorf("gh CLI not available"))
	defer cleanup()

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("unavailable provider should fail")
	}
	if !strings.Contains(err.Error(), "cannot verify merge status") {
		t.Errorf("expected cannot verify merge status error, got: %v", err)
	}
}

func wrongPRHeadBlock(t *testing.T) {
	// Mismatched head SHA when remote branch still exists: refuse
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	// Stored head doesn't match provider-reported head
	ident.HeadSHA = "oldsha0000000000000000000000000000000000"

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: "0123456789abcdef0123456789abcdef01234567",
		HeadSHA:   "newsha0000000000000000000000000000000000",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("mismatched head should fail")
	}
	if !strings.Contains(err.Error(), "head SHA mismatch") {
		t.Errorf("expected head SHA mismatch error, got: %v", err)
	}
}

// --- Dry-run / output proof ---

func TestShipSafetyCheck_Topology_EmitProof(t *testing.T) {
	// topologyAwareMergeCheck emits the exact proof used.
	// We verify this by inspecting the proof string in the error messages
	// when things fail, and by verifying success is returned when things pass.
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: "0123456789abcdef0123456789abcdef01234567",
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

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("should accept merged PR with deleted head: %v", err)
	}
	// Pass = proof was sufficient
}

// --- Proof emission tests ---

func TestShipSafetyCheck_Topology_ProofReturnedDeletedHead(t *testing.T) {
	// When topologyAwareMergeCheck succeeds with deleted head,
	// the proof string is returned and not dead code.
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: "0123456789abcdef0123456789abcdef01234567",
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

	proofs, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA // must match actual head

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	proofs, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = orphanSHA // orphan SHA is NOT an ancestor of origin/main

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: orphanSHA,
		HeadSHA:   orphanSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Ancestry check is not a blocker — provider MERGED + head SHA match
	// are sufficient proof. Teardown should succeed even though ancestry fails.
	proofs, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: squashSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	// Provider MERGED + head SHA match + MergedSHA ancestry verified
	proofs, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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

// --- Provider ambiguous/unavailable ---

func TestShipSafetyCheck_Topology_ProviderEmptyState(t *testing.T) {
	// Provider returns an empty/invalid state — should fail closed.
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

	ident := fixtureIdentity()
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:  false,
		Closed:  false,
		State:   "", // empty state is not valid
		HeadSHA: "",
	}, nil)
	defer cleanup()

	_, err := shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("empty provider state should fail")
	}
	if !strings.Contains(err.Error(), "unexpected state") {
		t.Errorf("expected unexpected state error, got: %v", err)
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
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA

	// Mock provider: merged with matching HeadSHA
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
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
	_, err = shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("regression: no upstream + deleted head + complete identity should pass: %v", err)
	}
}

func TestShipSafetyCheck_Regression_NoUpstreamDeletedHead_ProviderEmptyHeadSHA(t *testing.T) {
	tmp := t.TempDir()
	wt, _, md := setupTopologyRepo(t, tmp)

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

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA

	// Provider returns empty HeadSHA — fail closed
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: "",
		HeadSHA:   "",
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("empty provider HeadSHA should fail closed")
	}
	if !strings.Contains(err.Error(), "empty head SHA") {
		t.Errorf("expected 'empty head SHA' error, got: %v", err)
	}
}

// --- DeliveryState merged acceptance tests ---

func outcomeCompletedAllow(t *testing.T) {
	// A committed canonical completed delivery outcome plus a provider
	// merged confirmation accepts without --force.
	tmp := t.TempDir()
	if _, err := mhome.Init(tmp); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	wt, _, md := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA

	auth := seedCanonicalOutcome(t, tmp, "test", taskauthority.DeliveryOutcomeCompleted)

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, auth, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("merged with canonical completed outcome should pass: %v", err)
	}
}

func outcomeNotCompletedBlock(t *testing.T) {
	// A committed canonical non-completed delivery outcome (retryable)
	// rejects teardown without --force even when the provider says merged:
	// canonical truth decides.
	tmp := t.TempDir()
	if _, err := mhome.Init(tmp); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	wt, _, md := setupTopologyRepo(t, tmp)

	gitEnv := topologyGitEnv(wt)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = wt
	shaCmd.Env = gitEnv
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("getting head SHA: %v", err)
	}
	headSHA := strings.TrimSpace(string(shaOut))

	ident := fixtureIdentity()
	ident.HeadSHA = headSHA

	auth := seedCanonicalOutcome(t, tmp, "test", taskauthority.DeliveryOutcomeRetryable)

	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{
		Merged:    true,
		MergedSHA: headSHA,
		HeadSHA:   headSHA,
		State:     "MERGED",
	}, nil)
	defer cleanup()

	_, err = shipSafetyCheck(contractedMergeCheckOptions(t, "test"), ident, fakeTeardown{}, auth, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("expected error for non-completed canonical delivery outcome")
	}
	if !strings.Contains(err.Error(), "canonical delivery outcome") {
		t.Fatalf("expected canonical delivery outcome error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "retryable") {
		t.Fatalf("expected error mentioning retryable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected error mentioning --force, got: %v", err)
	}
}

// canonicalIdentitySafetyCheck runs the ship safety check over t1, whose .meta
// carries no pr_* keys, while the provider reports its PR open. authorized
// leaves a delivery authorization in flight; reopened moves t1 to a later
// generation than the one that authorized it.
func canonicalIdentitySafetyCheck(t *testing.T, authorized, reopened bool) error {
	t.Helper()
	wt, _, md := setupTopologyRepo(t, t.TempDir())
	var homeDir string
	if authorized {
		homeDir = inFlightGitHubDelivery(t)
	} else {
		c, dir := newFleetCanonical(t)
		mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
		homeDir = dir
	}
	if reopened {
		tamperTaskAggregate(t, homeDir, "t1", func(agg *taskauthority.Aggregate) { agg.Generation++ })
	}
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := applyMockPRStatus(t, &domain.PRMergeStatus{State: "OPEN", HeadSHA: deliveryTestHead}, nil)
	defer cleanup()
	_, err = safetyCheck(Options{HomeDir: homeDir, ID: "t1"}, nil, taskauthority.KindShip, fakeTeardown{}, c, 0, testWorktreeBinding(wt, md), false)
	return err
}

func authorizedIdentityBlock(t *testing.T) {
	if err := canonicalIdentitySafetyCheck(t, true, false); err == nil || !strings.Contains(err.Error(), "PR #7 is still open") {
		t.Fatalf("safetyCheck = %v, want the in-flight delivery's open PR refused", err)
	}
}

func unauthorizedIdentityAllow(t *testing.T) {
	if err := canonicalIdentitySafetyCheck(t, false, false); err != nil {
		t.Fatalf("safetyCheck = %v, want the pushed-branch proof to pass", err)
	}
}

func earlierGenerationIdentityAllow(t *testing.T) {
	if err := canonicalIdentitySafetyCheck(t, true, true); err != nil {
		t.Fatalf("safetyCheck = %v, want an earlier generation's authorization ignored", err)
	}
}
