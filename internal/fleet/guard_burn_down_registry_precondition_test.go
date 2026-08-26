package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestGuardBurnDownRegistryPreconditionRefusals(t *testing.T) {
	t.Run("unrecognized conflict is rejected", func(t *testing.T) {
		id := mustProjectID(t, "precondition-project")
		err := verifyPrecondition(id, domain.Of(99, 7), 3)
		if err == nil || !errors.Is(err, domain.ErrStalePrecondition) {
			t.Fatalf("verifyPrecondition error = %v, want stale-precondition conflict", err)
		}
		var conflict *domain.Conflict
		if !errors.As(err, &conflict) {
			t.Fatalf("verifyPrecondition error = %v, want domain.Conflict", err)
		}
		if conflict.ExpectedGeneration != 99 || conflict.ExpectedRevision != 7 || conflict.ActualGeneration != registryGeneration || conflict.ActualRevision != 3 {
			t.Fatalf("conflict = %+v, want expected gen=99 rev=7 and actual gen=%d rev=3", conflict, registryGeneration)
		}
	})

	t.Run("project registration requires current generation", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RegisterProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    mustProjectID(t, "wrong-generation-project"),
			Name:         "wrong-generation-project",
			Path:         "/proj/wrong-generation-project",
			Precondition: domain.Of(2, 0),
			Reason:       "guard test",
		}
		if _, err := r.RegisterProject(mustOp(t, "op-project-generation", req), req); err == nil || !strings.Contains(err.Error(), "precondition generation must be 1") {
			t.Fatalf("RegisterProject error = %v, want generation refusal", err)
		}
	})

	t.Run("captain registration requires current generation", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RegisterCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    mustCaptainID(t, "wrong-generation-captain"),
			Home:         "/captains/wrong-generation-captain",
			Precondition: domain.Of(2, 0),
			Reason:       "guard test",
		}
		if _, err := r.RegisterCaptain(mustOp(t, "op-captain-generation", req), req); err == nil || !strings.Contains(err.Error(), "precondition generation must be 1") {
			t.Fatalf("RegisterCaptain error = %v, want generation refusal", err)
		}
	})
}
