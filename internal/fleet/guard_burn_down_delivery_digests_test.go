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
