//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownVerifyDeliveryCurrencyRefusesAuthorizationIdentity(t *testing.T) {
	t.Run("missing authorization", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		journal := &deliveryJournal{
			TaskID: taskID, Generation: 1, Revision: 3, Kind: req.Kind,
			Identity: req.Identity, Preconditions: append([]taskauthority.DeliveryPrecondition(nil), req.Preconditions...),
			AuthorizeOpID: "missing-authorization",
		}
		err := verifyDeliveryCurrency(c, journal)
		if err == nil || !strings.Contains(err.Error(), "has no delivery authorization") {
			t.Fatalf("verifyDeliveryCurrency error = %v, want missing-authorization refusal", err)
		}
	})

	t.Run("authorization operation identity", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
			HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
			Precondition: domain.Of(1, 3), Kind: req.Kind, Identity: req.Identity,
			Preconditions: req.Preconditions,
		}
		if _, err := c.AuthorizeDelivery(mustFleetOperation(t, "op-currency-auth", authReq), authReq); err != nil {
			t.Fatal(err)
		}
		journal := &deliveryJournal{
			TaskID: taskID, Generation: 1, Revision: 3, Kind: req.Kind,
			Identity: req.Identity, Preconditions: append([]taskauthority.DeliveryPrecondition(nil), req.Preconditions...),
			AuthorizeOpID: "other-authorization",
		}
		err := verifyDeliveryCurrency(c, journal)
		if err == nil || !strings.Contains(err.Error(), "authorization identity mismatch") {
			t.Fatalf("verifyDeliveryCurrency error = %v, want operation-identity refusal", err)
		}
	})
}
