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
		{"unanswered delivery authorization, force overrides", deliveryReauthorizedBlock, deliveryReauthorizedForceAllow},
		{"this task's delivery holds the delivery lock, force overrides", deliveryLockHeldOwnBlock, deliveryLockHeldForceAllow},
		{"crashed delivery is recovered before the check", crashedDeliveryHeldBlock, crashedDeliveryHealsAllow},
		{"reopen while waiting for the delivery lock refuses a pinned teardown", reopenDuringLockWaitPinnedBlock, crashedDeliveryHealsAllow},
		{"reopen while waiting for the delivery lock refuses an unpinned teardown", reopenDuringLockWaitUnpinnedBlock, crashedDeliveryHealsAllow},
		{"task change while waiting for the delivery lock refuses", changeDuringLockWaitBlock, crashedDeliveryHealsAllow},
		{"released delivery authorization does not block", deliveryReauthorizedBlock, deliveryReleasedAllow},
		{"outcome of its own authorization does not block", deliveryReauthorizedBlock, deliveryOwnOutcomeAllow},
		{"another task's delivery lock does not hold an unrelated task", deliveryLockHeldOwnBlock, unrelatedTaskNoWaitAllow},
		{"review names no worktree, even with force", reviewWorktreeBlock, reviewWorktreeAllow},
		{"abort needs the endpoint proven absent", abortLiveEndpointBlock, abortAbsentEndpointAllow},
		{"stale teardown never retires a reopened generation", staleRetryBlock, explicitGenerationAllow},
		{"terminal cleanup claim is never resumed", terminalCleanupClaimBlock, activeCleanupClaimAllow},
		{"ship worktree is clean", dirtyTreeBlock, dirtyTreeAllow},
		{"local-only ship branch is merged locally, no origin needed", localOnlyUnlandedBlock, localOnlyLandedAllow},
		{"local-only ship branch pushed to origin passes unmerged", localOnlyUnlandedBlock, localOnlyPushedUnmergedAllow},
		{"ship PR is not closed unmerged", closedUnmergedBlock, mergedMatchingHeadAllow},
		{"ship PR is not open unmerged", openUnmergedBlock, mergedMatchingHeadAllow},
		{"ship merged PR head matches the identity", wrongPRHeadBlock, mergedMatchingHeadAllow},
		{"ship canonical delivery outcome is completed", outcomeNotCompletedBlock, outcomeCompletedAllow},
		{"ship delivery identity is the authorization's, not .meta", authorizedIdentityBlock, unauthorizedIdentityAllow},
		{"an earlier generation's authorization is not the delivery identity", authorizedIdentityBlock, earlierGenerationIdentityAllow},
	} {
		t.Run(rule.name+"/blocks", rule.block)
		t.Run(rule.name+"/allows", rule.allow)
	}
}
