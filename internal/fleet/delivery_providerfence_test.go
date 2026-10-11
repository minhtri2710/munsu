//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

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
	mustWorkingDeliveryTask(t, c, taskID, deliveryTestGitHubForge)
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

func TestVerifyProviderHeadRequiresHeadAndBaseEvidence(t *testing.T) {
	journal := &deliveryJournal{Identity: deliveryTestIdentity()}

	// A non-merged observation with no head evidence must reach the head
	// refusal: the merged-SHA precondition does not shadow it here.
	noHead := DeliveryProviderObservation{State: "OPEN", BaseRef: deliveryTestBase}
	err := verifyProviderHead(journal, noHead)
	if err == nil || !strings.Contains(err.Error(), "missing head evidence") {
		t.Fatalf("verifyProviderHead = %v, want missing-head-evidence refusal", err)
	}

	// Head evidence present but base ref missing must reach the base
	// refusal before any drift comparison can run.
	noBase := DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead}
	err = verifyProviderHead(journal, noBase)
	if err == nil || !strings.Contains(err.Error(), "missing base ref evidence") {
		t.Fatalf("verifyProviderHead = %v, want missing-base-ref-evidence refusal", err)
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
