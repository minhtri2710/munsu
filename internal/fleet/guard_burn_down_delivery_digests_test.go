//go:build integration

package fleet

import (
	"strings"
	"testing"
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
}
