//go:build integration

package fleet

import "testing"

// TestRetirementRules is the one table for the teardown refusals: each rule
// has exactly one row that blocks the refused case and one that allows the
// nearest valid case. The row functions live beside the fixtures they use.
// The remote-branch pushed-head proof (TestCheckRemoteBranchPushedHeadProof)
// and the worktree-holder reap (TestReapWorktreeHolders) keep their own tables,
// and the default-lane registry rule keeps TestRegistryRetireProject.
func TestRetirementRules(t *testing.T) {
	for _, rule := range []struct {
		name         string
		block, allow func(*testing.T)
	}{
		{"captain with in-flight soldiers, force overrides", captainInFlightBlock, captainInFlightForceAllow},
		{"scout unresolved decision holds", scoutHoldsBlock, scoutHoldsAllow},
		{"completed delivery outcome required", completedOutcomeBlock, completedOutcomeAllow},
		{"review names no worktree, even with force", reviewWorktreeBlock, reviewWorktreeAllow},
		{"review names no branch, force overrides", reviewBranchBlock, reviewBranchForceAllow},
		{"abort needs the endpoint proven absent", abortLiveEndpointBlock, abortAbsentEndpointAllow},
		{"stale teardown never retires a reopened generation", staleRetryBlock, explicitGenerationAllow},
		{"terminal cleanup claim is never resumed", terminalCleanupClaimBlock, activeCleanupClaimAllow},
		{"ship worktree is clean", dirtyTreeBlock, dirtyTreeAllow},
		{"ship PR is not closed unmerged", closedUnmergedBlock, mergedMatchingHeadAllow},
		{"ship PR is not open unmerged", openUnmergedBlock, mergedMatchingHeadAllow},
		{"ship merged PR head matches the identity", wrongPRHeadBlock, mergedMatchingHeadAllow},
		{"ship canonical delivery outcome is completed", outcomeNotCompletedBlock, outcomeCompletedAllow},
	} {
		t.Run(rule.name+"/blocks", rule.block)
		t.Run(rule.name+"/allows", rule.allow)
	}
}
