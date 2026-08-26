package fleet

import (
	"strings"
	"testing"
)

func TestGuardBurnDownRegistryValidationRefusals(t *testing.T) {
	t.Run("new registry requires a home", func(t *testing.T) {
		if _, err := NewRegistry(nil); err == nil || !strings.Contains(err.Error(), "nil home") {
			t.Fatalf("NewRegistry(nil) error = %v, want nil-home refusal", err)
		}
	})

	t.Run("operation digest must match typed intent", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RegisterProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    mustProjectID(t, "digest-project"),
			Name:         "digest-project",
			Path:         "/proj/digest-project",
			Precondition: preconditionOf(0),
			Reason:       "guard test",
		}
		op := mustOp(t, "op-bad-digest", req)
		op.Digest = strings.Repeat("0", 64)
		if _, err := r.RegisterProject(op, req); err == nil || !strings.Contains(err.Error(), "digest does not match") {
			t.Fatalf("RegisterProject bad digest error = %v, want digest refusal", err)
		}
	})

	t.Run("required project fields cannot be blank", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		req := RegisterProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    mustProjectID(t, "blank-name"),
			Name:         "  \t",
			Path:         "/proj/blank-name",
			Precondition: preconditionOf(0),
			Reason:       "guard test",
		}
		if _, err := r.RegisterProject(mustOp(t, "op-blank-name", req), req); err == nil || !strings.Contains(err.Error(), "project name is required") {
			t.Fatalf("RegisterProject blank name error = %v, want required-field refusal", err)
		}
	})
}
