//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownPrevalidateDeliveryTaskRefusesOwnerOrActiveAuthorization(t *testing.T) {
	t.Run("missing owner", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		if err := rewriteDeliveryAggregate(t, homeDir, taskID, func(cur taskauthority.Aggregate) taskauthority.Aggregate {
			cur.Definition.Owner = "   "
			return cur
		}); err != nil {
			t.Fatal(err)
		}
		agg, err := readDeliveryAggregate(t, homeDir, taskID)
		if err != nil {
			t.Fatal(err)
		}
		err = prevalidateDeliveryTask(c, agg, deliverRequest())
		if err == nil || !strings.Contains(err.Error(), "requires an owner") {
			t.Fatalf("prevalidateDeliveryTask error = %v, want missing-owner refusal", err)
		}
	})

	t.Run("active authorization", func(t *testing.T) {
		c, _ := newFleetCanonical(t)
		taskID := "t1"
		mustWorkingDeliveryTask(t, c, taskID)
		req := deliverRequest()
		authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
			HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
			Precondition: domain.Of(1, 3), Kind: req.Kind, Identity: req.Identity,
			Preconditions: req.Preconditions,
		}
		if _, err := c.AuthorizeDelivery(mustFleetOperation(t, "op-active-auth", authReq), authReq); err != nil {
			t.Fatal(err)
		}
		agg, err := c.Get(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		err = prevalidateDeliveryTask(c, agg, req)
		if err == nil || !strings.Contains(err.Error(), "already has an active delivery authorization") {
			t.Fatalf("prevalidateDeliveryTask error = %v, want active-authorization refusal", err)
		}
	})
}
