package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObligationsByRole(t *testing.T) {
	tests := []struct {
		role      Role
		want      int
		wantKinds []ObligationKind
	}{
		{
			role:      RoleGeneral,
			want:      1,
			wantKinds: []ObligationKind{Cleanup},
		},
		{
			role:      RoleCaptain,
			want:      1,
			wantKinds: []ObligationKind{Cleanup},
		},
		{
			role:      RoleSoldier,
			want:      1,
			wantKinds: []ObligationKind{Cleanup},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := Obligations(tt.role)
			if len(got) != tt.want {
				t.Errorf("Obligations(%q) = %d items, want %d", tt.role, len(got), tt.want)
			}

			// Verify all expected kinds are present
			for _, wantKind := range tt.wantKinds {
				found := false
				for _, o := range got {
					if o.Kind == wantKind {
						found = true
						if o.State != StateOpen {
							t.Errorf("Obligations(%q) kind=%q has state=%q, want %q",
								tt.role, o.Kind, o.State, StateOpen)
						}
						break
					}
				}
				if !found {
					t.Errorf("Obligations(%q): missing kind %q", tt.role, wantKind)
				}
			}

			// All must be open
			for _, o := range got {
				if o.State != StateOpen {
					t.Errorf("Obligations(%q) kind=%q state=%q, want %q", tt.role, o.Kind, o.State, StateOpen)
				}
			}
		})
	}
}

func TestObligationsUnknownRole(t *testing.T) {
	got := Obligations("unknown")
	if got != nil {
		t.Errorf("Obligations(unknown) = %v, want nil", got)
	}
}

func TestSaveAndLoadObligations(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Initially no persisted state — should return defaults
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("LoadObligations() = %d items, want 1", len(loaded))
	}

	found, err := CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("CompleteObligation() error = %v", err)
	}
	if !found {
		t.Errorf("CompleteObligation(Cleanup) = false, want true")
	}

	// Load again — the obligation persists, closed
	loaded, err = LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Kind != Cleanup || loaded[0].State != StateClosed || loaded[0].ClosedAt == 0 {
		t.Fatalf("LoadObligations() after complete = %+v, want one closed Cleanup with ClosedAt", loaded)
	}
}

func TestCompleteIdempotent(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Complete Cleanup twice — second call should find it already closed
	found, err := CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("first CompleteObligation() error = %v", err)
	}
	if !found {
		t.Errorf("first CompleteObligation(Cleanup) = false, want true")
	}

	found, err = CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("second CompleteObligation() error = %v", err)
	}
	if found {
		t.Errorf("second CompleteObligation(Cleanup) = true, want false (already closed)")
	}
}

func TestClearCompleted(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Complete all obligations
	CompleteObligation(home, role, Cleanup)

	// Clear completed
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() error = %v", err)
	}

	// Load — should have no open obligations
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("ClearCompleted: got %d obligations, want 0", len(loaded))
	}
}

func TestClearCompletedIdempotent(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Clear completed on fresh home — no-op
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() on fresh home error = %v", err)
	}

	// Load should still return defaults
	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("after ClearCompleted on fresh home: got %d, want 1", len(loaded))
	}
}

func TestLoadFromEmptyFile(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Create an empty obligations file
	p := filepath.Join(home, obligationsDir, string(role)+".obligations")
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte{}, 0644); err != nil {
		t.Fatalf("writing empty file: %v", err)
	}

	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("empty file: got %d obligations, want 0", len(loaded))
	}
}
