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

func TestGuardBurnDownVerifyDeliveryCurrencyRefusesKindHeadAndPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*deliveryJournal)
		want   string
	}{
		{name: "authorization kind", mutate: func(j *deliveryJournal) {
			j.Kind = taskauthority.DeliveryAuthorizationKind("not-a-kind")
		}, want: "authorization kind mismatch"},
		{name: "authorization head", mutate: func(j *deliveryJournal) {
			j.Identity.HeadSHA = "9999888877776666555544443333222211110000"
		}, want: "authorization head mismatch"},
		{name: "authorization preconditions", mutate: func(j *deliveryJournal) {
			j.Preconditions = []taskauthority.DeliveryPrecondition{taskauthority.DeliveryPreconditionWorktreeClean}
		}, want: "precondition set differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				AuthorizeOpID: "op-currency-auth",
			}
			tc.mutate(journal)
			err := verifyDeliveryCurrency(c, journal)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verifyDeliveryCurrency error = %v, want %q", err, tc.want)
			}
		})
	}
}
