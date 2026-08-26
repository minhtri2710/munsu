//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestDeliverProviderIdentityDriftFailsClosedBeforeMutation proves the
// last fail-closed fence before the irreversible provider merge rejects an
// observation whose head or base ref drifted from the pinned delivery
// identity: no merge is executed and no terminal outcome is committed.
//
// The base ref case is the one the provider merge cannot recover from: the
// merge lands in the PR's CURRENT base, so a base changed inside the
// capture-to-merge window would land the mutation on a branch that was never
// authorized.
func TestDeliverProviderIdentityDriftFailsClosedBeforeMutation(t *testing.T) {
	pinned := deliveryTestIdentity()
	cases := []struct {
		name    string
		obs     DeliveryProviderObservation
		wantErr string
	}{
		{
			name:    "head-drift",
			obs:     DeliveryProviderObservation{State: "OPEN", HeadSHA: "9999888877776666555544443333222211110000", BaseRef: pinned.BaseRef, Mergeability: DeliveryMergeabilityAllowed},
			wantErr: "provider head changed since capture",
		},
		{
			name:    "base-ref-drift",
			obs:     DeliveryProviderObservation{State: "OPEN", HeadSHA: pinned.HeadSHA, BaseRef: "release/1.0", Mergeability: DeliveryMergeabilityAllowed},
			wantErr: "provider base ref changed since capture",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			provider := newFakeDeliveryProvider().script(tc.obs)
			installDeliveryProviderFor(t, provider)

			_, err := Deliver(homeDir, taskID, deliverRequest())
			var failClosed *DeliveryFailClosedError
			if !errors.As(err, &failClosed) {
				t.Fatalf("Deliver err = %T %v, want *DeliveryFailClosedError", err, err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Deliver err = %v, want it to carry %q", err, tc.wantErr)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0 (the irreversible mutation must not run)", provider.merges)
			}
			if out, oerr := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); oerr == nil {
				t.Fatalf("fail-closed path committed a terminal outcome: %+v", out)
			}
		})
	}
}

// TestDeliverProviderBaseRefMatchDeliversNormally proves the base ref fence
// only rejects drift: an observation whose base ref equals the pinned one
// still executes the merge exactly once and commits the truthful outcome.
// This check runs on EVERY delivery, so a false rejection here would block
// the whole delivery path.
func TestDeliverProviderBaseRefMatchDeliversNormally(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	pinned := deliveryTestIdentity()
	provider := newFakeDeliveryProvider().script(
		DeliveryProviderObservation{State: "OPEN", HeadSHA: pinned.HeadSHA, BaseRef: pinned.BaseRef, Mergeability: DeliveryMergeabilityAllowed},
		DeliveryProviderObservation{State: "MERGED", HeadSHA: pinned.HeadSHA, BaseRef: pinned.BaseRef, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
	)
	installDeliveryProviderFor(t, provider)

	result, err := Deliver(homeDir, taskID, deliverRequest())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("status = %q, want completed (a matching base ref must not be blocked)", result.Status)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want exactly 1", provider.merges)
	}
}

// TestDeliverProviderFenceAcceptsMatchingObservations proves the provider
// identity fence accepts matching head and base evidence.
func TestVerifyProviderHeadRejectsInvalidMergedSHA(t *testing.T) {
	journal := &deliveryJournal{Identity: deliveryTestIdentity()}
	for _, sha := range []string{"not-a-git-object-id", "0000000000000000000000000000000000000000"} {
		obs := DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, MergedSHA: sha}
		if err := verifyProviderHead(journal, obs); err == nil {
			t.Fatalf("verifyProviderHead accepted invalid merged SHA %q", sha)
		}
	}
}

func TestCommitPinnedOutcomeRejectsInvalidMergedSHA(t *testing.T) {
	journal := &deliveryJournal{
		ID: "journal-1", TaskID: "t1", AuthorizeOpID: "authorize-1",
		OutcomeStatus:  taskauthority.DeliveryOutcomeCompleted,
		OutcomeHeadSHA: deliveryTestHead, OutcomeMergedSHA: "not-a-git-object-id",
	}
	if _, err := commitPinnedOutcome(nil, nil, nil, journal); err == nil {
		t.Fatal("commitPinnedOutcome accepted invalid merged SHA")
	}
}

func TestDeliverMergedInvalidSHAFailsClosedBeforeOutcome(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	provider := newFakeDeliveryProvider().script(DeliveryProviderObservation{
		State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, MergedSHA: "not-a-git-object-id",
	})
	installDeliveryProviderFor(t, provider)

	result, err := Deliver(homeDir, taskID, deliverRequest())
	var failClosed *DeliveryFailClosedError
	if !errors.As(err, &failClosed) || result != nil {
		t.Fatalf("result=%+v err=%T %v, want fail-closed refusal", result, err, err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
	if out, outcomeErr := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); outcomeErr == nil {
		t.Fatalf("invalid merged SHA committed outcome: %+v", out)
	}
}

func TestDeliverProviderFenceAcceptsAndRejectsObservations(t *testing.T) {
	journal := &deliveryJournal{Identity: deliveryTestIdentity()}
	cases := []struct {
		name string
		obs  DeliveryProviderObservation
	}{
		{"explicit-mergeability", DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, Mergeability: DeliveryMergeabilityAllowed}},
		{"merged", DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, MergedSHA: "0123456789abcdef0123456789abcdef01234567"}},
		{"merged-missing-head", DeliveryProviderObservation{State: "MERGED", BaseRef: deliveryTestBase}},
		{"merged-drifted-head", DeliveryProviderObservation{State: "MERGED", HeadSHA: "9999888877776666555544443333222211110000", BaseRef: deliveryTestBase}},
		{"merged-missing-base", DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead}},
		{"merged-drifted-base", DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: "release/1.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyProviderHead(journal, tc.obs)
			if tc.name == "explicit-mergeability" || tc.name == "merged" {
				if err != nil {
					t.Fatalf("verifyProviderHead = %v, want accepted", err)
				}
			} else if err == nil {
				t.Fatal("verifyProviderHead unexpectedly accepted invalid merged evidence")
			}
		})
	}
}

// TestDeliverPrevalidateRejectsHeadNotMatchingBoundWorktree is the canary of
// the prevalidation mirror of the canonical head fence: an identity head that
// does not match the bound worktree head never writes a journal intent and
// never issues an authorization.
func TestDeliverPrevalidateRejectsHeadNotMatchingBoundWorktree(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	provider := installScriptedProviderFor(t, "open-then-merged")

	req := deliverRequest()
	req.Identity.HeadSHA = "9999888877776666555544443333222211110000"

	_, err := Deliver(homeDir, taskID, req)
	if err == nil || !strings.Contains(err.Error(), "does not match the bound worktree head") {
		t.Fatalf("Deliver err = %v, want the identity/head prevalidation to fail closed", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none (no intent for an unauthorizable task)", active)
	}
	if files := listDeliveryJournalFiles(t, homeDir); len(files) != 0 {
		t.Fatalf("journal records = %v, want none", files)
	}
	if _, err := c.DeliveryAuthorization(mustFleetTaskID(t, taskID)); err == nil {
		t.Fatal("authorization issued for a head that does not match the bound worktree")
	}
}
