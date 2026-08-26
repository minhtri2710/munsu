package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestGuardBurnDownRegistryLifecycleRefusals(t *testing.T) {
	t.Run("binding requires current generation", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		mustRegisterProject(t, r, "bind-generation-project")
		mustRegisterCaptain(t, r, "bind-generation-captain")
		req := BindCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    mustCaptainID(t, "bind-generation-captain"),
			ProjectID:    mustProjectID(t, "bind-generation-project"),
			Precondition: domain.Of(2, 0),
			Reason:       "guard test",
		}
		if _, err := r.BindCaptain(mustOp(t, "op-bind-generation", req), req); err == nil || !strings.Contains(err.Error(), "precondition generation must be 1") {
			t.Fatalf("BindCaptain error = %v, want generation refusal", err)
		}
	})

	t.Run("retiring an unknown captain refuses", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RetireCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    mustCaptainID(t, "missing-captain"),
			Precondition: preconditionOf(0),
			Reason:       "guard test",
		}
		if _, err := r.RetireCaptain(mustOp(t, "op-retire-missing-captain", req), req); err == nil || !strings.Contains(err.Error(), "captain missing-captain not found") {
			t.Fatalf("RetireCaptain error = %v, want missing-captain refusal", err)
		}
	})

	t.Run("retiring a captain requires current generation", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RetireCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    mustCaptainID(t, "retire-generation-captain"),
			Precondition: domain.Of(2, 0),
			Reason:       "guard test",
		}
		if _, err := r.RetireCaptain(mustOp(t, "op-retire-generation-captain", req), req); err == nil || !strings.Contains(err.Error(), "precondition generation must be 1") {
			t.Fatalf("RetireCaptain error = %v, want generation refusal", err)
		}
	})
}
