//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestGuardBurnDownValidateDeliverRequestRefusesInvalidPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name string
		pre  []taskauthority.DeliveryPrecondition
		want string
	}{
		{name: "missing preconditions", want: "requires at least one typed precondition"},
		{name: "invalid precondition", pre: []taskauthority.DeliveryPrecondition{"unknown"}, want: "invalid delivery precondition"},
		{name: "duplicate precondition", pre: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRMergeable,
		}, want: "duplicate delivery precondition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := deliverRequest()
			req.Preconditions = tc.pre
			if err := validateDeliverRequest(req, req.Method); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateDeliverRequest error = %v, want %q", err, tc.want)
			}
		})
	}
}
