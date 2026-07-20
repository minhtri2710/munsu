package turnend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObligationsByRole(t *testing.T) {
	tests := []struct {
		role     Role
		want     int
		wantKinds []ObligationKind
	}{
		{
			role:     RoleGeneral,
			want:     1,
			wantKinds: []ObligationKind{Cleanup},
		},
		{
			role:     RoleCaptain,
			want:     2,
			wantKinds: []ObligationKind{ReportRelay, Cleanup},
		},
		{
			role:     RoleSoldier,
			want:     2,
			wantKinds: []ObligationKind{ReportRelay, Cleanup},
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
	if len(loaded) != 2 {
		t.Fatalf("LoadObligations() = %d items, want 2", len(loaded))
	}

	// Complete one obligation
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation() error = %v", err)
	}
	if !found {
		t.Errorf("CompleteObligation(ReportRelay) = false, want true")
	}

	// Load again — should see one closed, one open
	loaded, err = LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("LoadObligations() = %d items, want 2 after complete", len(loaded))
	}

	var reportRelayFound, cleanupFound bool
	for _, o := range loaded {
		switch o.Kind {
		case ReportRelay:
			reportRelayFound = true
			if o.State != StateClosed {
				t.Errorf("ReportRelay state = %q, want %q", o.State, StateClosed)
			}
			if o.ClosedAt == 0 {
				t.Errorf("ReportRelay ClosedAt = 0, want non-zero")
			}
		case Cleanup:
			cleanupFound = true
			if o.State != StateOpen {
				t.Errorf("Cleanup state = %q, want %q", o.State, StateOpen)
			}
		}
	}
	if !reportRelayFound || !cleanupFound {
		t.Errorf("missing obligations: reportRelay=%v cleanup=%v", reportRelayFound, cleanupFound)
	}
}

func TestCompleteNotFound(t *testing.T) {
	home := t.TempDir()
	role := RoleGeneral

	// General has only Cleanup, completing ReportRelay should return false
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation() error = %v", err)
	}
	if found {
		t.Errorf("CompleteObligation(ReportRelay) for General = true, want false")
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
	CompleteObligation(home, role, ReportRelay)
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
	if len(loaded) != 2 {
		t.Errorf("after ClearCompleted on fresh home: got %d, want 2", len(loaded))
	}
}

func TestClearAll(t *testing.T) {
	home := t.TempDir()
	role := RoleCaptain

	// Save some obligations
	obligations := Obligations(role)
	if err := SaveObligations(home, role, obligations); err != nil {
		t.Fatalf("SaveObligations() error = %v", err)
	}

	// Verify file exists
	p := filepath.Join(home, obligationsDir, string(role)+".obligations")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("obligations file should exist: %v", err)
	}

	// Clear all
	if err := ClearAll(home, role); err != nil {
		t.Fatalf("ClearAll() error = %v", err)
	}

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("obligations file should be removed, but stat err = %v", err)
	}

	// ClearAll on already-cleared home is a no-op
	if err := ClearAll(home, role); err != nil {
		t.Errorf("ClearAll() on cleared home error = %v", err)
	}
}

func TestMaterialStates(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"done", true},
		{"failed", true},
		{"needs-decision", true},
		{"blocked", true},
		{"working", false},
		{"paused", false},
		{"resolved", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		got := MaterialStates(tt.state)
		if got != tt.want {
			t.Errorf("MaterialStates(%q) = %v, want %v", tt.state, got, tt.want)
		}
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

func TestSoldierObligations(t *testing.T) {
	home := t.TempDir()
	role := RoleSoldier

	loaded, err := LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("soldier: got %d obligations, want 2", len(loaded))
	}

	// Complete both
	found, err := CompleteObligation(home, role, ReportRelay)
	if err != nil {
		t.Fatalf("CompleteObligation(ReportRelay) error = %v", err)
	}
	if !found {
		t.Errorf("soldier CompleteObligation(ReportRelay) = false, want true")
	}

	found, err = CompleteObligation(home, role, Cleanup)
	if err != nil {
		t.Fatalf("CompleteObligation(Cleanup) error = %v", err)
	}
	if !found {
		t.Errorf("soldier CompleteObligation(Cleanup) = false, want true")
	}

	// Clear completed
	if err := ClearCompleted(home, role); err != nil {
		t.Fatalf("ClearCompleted() error = %v", err)
	}

	loaded, err = LoadObligations(home, role)
	if err != nil {
		t.Fatalf("LoadObligations() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("soldier after clear: got %d obligations, want 0", len(loaded))
	}
}
