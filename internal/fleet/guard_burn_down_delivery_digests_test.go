//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownDeliveryAuthorizationAndRevocationDigestRefusals(t *testing.T) {
	t.Run("authorization digest mismatch", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		journal, err := buildDeliveryJournal("/home", c, agg, req, req.Method)
		if err != nil {
			t.Fatal(err)
		}
		journal.AuthorizeDigest = "wrong-digest"
		err = issueDeliveryAuthorization(c, journal)
		if err == nil || !strings.Contains(err.Error(), "authorization digest mismatch") {
			t.Fatalf("issueDeliveryAuthorization error = %v, want digest refusal", err)
		}
	})

	t.Run("revocation digest mismatch", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		journal, err := buildDeliveryJournal("/home", c, agg, req, req.Method)
		if err != nil {
			t.Fatal(err)
		}
		journal.RevokeFailClosedDigest = "wrong-digest"
		err = releaseDeliveryAuthorization(nil, nil, c, journal, "delivery currency invalid", journal.Revision+1)
		if err == nil || !strings.Contains(err.Error(), "revoke digest mismatch") {
			t.Fatalf("releaseDeliveryAuthorization error = %v, want digest refusal", err)
		}
	})
}

func TestGuardBurnDownDeliveryOutcomeDigestRefusals(t *testing.T) {
	t.Run("missing outcome status", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		journal := &deliveryJournal{
			ID: "journal-missing-status", TaskID: "t1", Generation: 1, Revision: 3,
			Kind: deliverRequest().Kind, AuthorizeOpID: "authorize-missing-status",
			OutcomeOpID: "outcome-missing-status",
		}
		_, err := commitPinnedOutcome(nil, nil, c, journal)
		if err == nil || !strings.Contains(err.Error(), "has no pinned outcome") {
			t.Fatalf("commitPinnedOutcome error = %v, want missing-status refusal", err)
		}
	})

	t.Run("outcome digest mismatch", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		journal, err := buildDeliveryJournal("/home", c, agg, req, req.Method)
		if err != nil {
			t.Fatal(err)
		}
		journal.OutcomeStatus = taskauthority.DeliveryOutcomeCompleted
		journal.OutcomeDetail = "merged"
		journal.OutcomeHeadSHA = req.Identity.HeadSHA
		journal.OutcomeMergedSHA = "590d6f6c114867fe47123fd940b920fdabcd1234"
		journal.OutcomeOpID = "outcome-digest-mismatch"
		journal.OutcomeDigest = "wrong-digest"
		_, err = commitPinnedOutcome(nil, nil, c, journal)
		if err == nil || !strings.Contains(err.Error(), "outcome digest mismatch") {
			t.Fatalf("commitPinnedOutcome error = %v, want digest refusal", err)
		}
	})
}
