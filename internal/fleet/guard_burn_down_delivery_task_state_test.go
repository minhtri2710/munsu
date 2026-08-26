//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownPrevalidateDeliveryTaskRefusesHeldOrTerminalTask(t *testing.T) {
	t.Run("matching delivery hold", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		hold := taskauthority.CanonicalAddHoldRequest{
			HomeID: c.HomeID(), HoldID: "delivery-hold",
			Scope:   taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
			Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery},
			Reason:  "freeze delivery",
		}
		if _, err := c.AddHold(mustFleetOperation(t, "op-add-delivery-hold", hold), hold); err != nil {
			t.Fatal(err)
		}
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		err = prevalidateDeliveryTask(c, agg, deliverRequest())
		if err == nil || !strings.Contains(err.Error(), "delivery is held") {
			t.Fatalf("prevalidateDeliveryTask error = %v, want delivery-hold refusal", err)
		}
	})

	t.Run("terminal outcome", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
			HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
			Precondition: domain.Of(1, 3),
			Kind:         deliverRequest().Kind, Identity: deliveryTestIdentity(),
			Preconditions: deliverRequest().Preconditions,
		}
		if _, err := c.AuthorizeDelivery(mustFleetOperation(t, "op-terminal-auth", authReq), authReq); err != nil {
			t.Fatal(err)
		}
		outReq := taskauthority.CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
			Precondition: domain.Of(1, 4), AuthorizationOperationID: "op-terminal-auth",
			Status: taskauthority.DeliveryOutcomeCompleted, Detail: "already merged",
			HeadSHA: deliveryTestHead, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		if _, err := c.CommitDeliveryOutcome(mustFleetOperation(t, "op-terminal-outcome", outReq), outReq); err != nil {
			t.Fatal(err)
		}
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		err = prevalidateDeliveryTask(c, agg, deliverRequest())
		if err == nil || !strings.Contains(err.Error(), "already committed terminal delivery outcome") {
			t.Fatalf("prevalidateDeliveryTask error = %v, want terminal-outcome refusal", err)
		}
	})
}
