package decisionhold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHoldID(t *testing.T) {
	tests := []struct {
		origin, key, want string
	}{
		{"task-1", "approach", "task-1-decision-approach"},
		{"scout-r2", "ui-pattern", "scout-r2-decision-ui-pattern"},
		{"a", "b", "a-decision-b"},
	}
	for _, tt := range tests {
		got := HoldID(tt.origin, tt.key)
		if got != tt.want {
			t.Errorf("HoldID(%q, %q) = %q, want %q", tt.origin, tt.key, got, tt.want)
		}
	}
}

func TestCreate_Hold(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Create(homeDir, "task-1", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatal("expected Created=true for first call")
	}
	if result.HoldID != "task-1-decision-approach" {
		t.Errorf("HoldID = %q, want %q", result.HoldID, "task-1-decision-approach")
	}

	// Verify hold file exists.
	holdFile := filepath.Join(homeDir, "holds", "task-1-decision-approach.hold")
	if _, err := os.Stat(holdFile); err != nil {
		t.Errorf("hold file should exist: %v", err)
	}

	// Verify status was appended.
	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "task-1.status"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(statuses)
	if !contains(content, "needs-decision:") {
		t.Errorf("status missing needs-decision line: %s", content)
	}
	if !contains(content, "[key=approach]") {
		t.Errorf("status missing key=approach: %s", content)
	}
}

func TestCreate_Idempotent(t *testing.T) {
	homeDir := t.TempDir()

	result1, err := Create(homeDir, "task-1", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Created {
		t.Fatal("expected Created=true for first call")
	}

	result2, err := Create(homeDir, "task-1", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}
	if result2.Created {
		t.Fatal("expected Created=false for duplicate call")
	}
	if result2.HoldID != result1.HoldID {
		t.Errorf("HoldID mismatch: %q vs %q", result1.HoldID, result2.HoldID)
	}
}

func TestCreate_Validation(t *testing.T) {
	homeDir := t.TempDir()

	tests := []struct {
		name       string
		origin, kw, reason string
	}{
		{"empty origin", "", "k", "reason"},
		{"empty key", "origin", "", "reason"},
		{"empty reason", "origin", "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Create(homeDir, tt.origin, tt.kw, tt.reason)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestListUnresolved(t *testing.T) {
	homeDir := t.TempDir()

	// No holds yet.
	holds, err := ListUnresolved(homeDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 0 {
		t.Errorf("expected 0 holds, got %d", len(holds))
	}

	// Create holds.
	Create(homeDir, "task-1", "approach", "Pick the UI framework")
	Create(homeDir, "task-1", "db-schema", "Choose DB schema")

	holds, err = ListUnresolved(homeDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 2 {
		t.Fatalf("expected 2 holds, got %d", len(holds))
	}

	// Resolve one.
	if err := Resolve(homeDir, "task-1", "approach", "Choose React", nil); err != nil {
		t.Fatal(err)
	}

	holds, err = ListUnresolved(homeDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 {
		t.Fatalf("expected 1 unresolved hold, got %d", len(holds))
	}
	if holds[0].DecisionKey != "db-schema" {
		t.Errorf("expected db-schema, got %s", holds[0].DecisionKey)
	}
}

func TestListUnresolved_OtherOriginIgnored(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")
	Create(homeDir, "task-2", "db", "Choose DB")

	holds, err := ListUnresolved(homeDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 {
		t.Fatalf("expected 1 hold for task-1, got %d", len(holds))
	}
}

func TestComplete(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick the UI framework")
	Create(homeDir, "task-1", "db-schema", "Choose DB schema")

	if err := Complete(homeDir, "task-1", []string{"approach", "db-schema"}); err != nil {
		t.Fatal(err)
	}

	// Both should now be resolved.
	_, resolved, err := ReadResolution(homeDir, "task-1", "approach")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved {
		t.Error("approach should be resolved after Complete")
	}

	_, resolved2, err := ReadResolution(homeDir, "task-1", "db-schema")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved2 {
		t.Error("db-schema should be resolved after Complete")
	}
}

func TestComplete_None(t *testing.T) {
	homeDir := t.TempDir()

	if err := Complete(homeDir, "task-1", []string{"--none"}); err != nil {
		t.Fatal(err)
	}

	// Verify complete marker exists.
	completeFile := filepath.Join(homeDir, "holds", "task-1.complete")
	if _, err := os.Stat(completeFile); err != nil {
		t.Errorf("complete file should exist: %v", err)
	}
}

func TestComplete_Validation(t *testing.T) {
	homeDir := t.TempDir()

	if err := Complete(homeDir, "", []string{"k"}); err == nil {
		t.Error("expected error for empty origin-id")
	}
	if err := Complete(homeDir, "task-1", []string{}); err == nil {
		t.Error("expected error for empty keys")
	}
}

func TestVerify_Clean(t *testing.T) {
	homeDir := t.TempDir()

	unresolved, err := Verify(homeDir, "task-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected no unresolved, got %v", unresolved)
	}
}

func TestVerify_WithUnresolved(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")

	unresolved, err := Verify(homeDir, "task-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved, got %d: %v", len(unresolved), unresolved)
	}
	if unresolved[0] != "approach" {
		t.Errorf("expected approach, got %s", unresolved[0])
	}
}

func TestVerify_AfterResolve(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")
	Resolve(homeDir, "task-1", "approach", "Choose React", nil)

	unresolved, err := Verify(homeDir, "task-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected no unresolved after resolve, got %v", unresolved)
	}
}

func TestResolve(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")

	if err := Resolve(homeDir, "task-1", "approach", "Choose React", nil); err != nil {
		t.Fatal(err)
	}

	answer, resolved, err := ReadResolution(homeDir, "task-1", "approach")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved {
		t.Error("expected resolved=true")
	}
	if answer != "Choose React" {
		t.Errorf("answer = %q, want %q", answer, "Choose React")
	}

	// Verify status was appended.
	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "task-1.status"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(statuses)
	if !contains(content, "resolved:") {
		t.Errorf("status missing resolved line: %s", content)
	}
	if !contains(content, "[key=approach]") {
		t.Errorf("status missing key=approach: %s", content)
	}
}

func TestResolve_WithUnblock(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")

	if err := Resolve(homeDir, "task-1", "approach", "Choose React", []string{"dep-task-1"}); err != nil {
		t.Fatal(err)
	}

	// Verify dependent was unblocked.
	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "dep-task-1.status"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(statuses)
	if !contains(content, "unblocked:") {
		t.Errorf("dependent status missing unblocked line: %s", content)
	}
}

func TestResolve_Validation(t *testing.T) {
	homeDir := t.TempDir()

	tests := []struct {
		name              string
		origin, kw, answer string
	}{
		{"empty origin", "", "k", "answer"},
		{"empty key", "origin", "", "answer"},
		{"empty answer", "origin", "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Resolve(homeDir, tt.origin, tt.kw, tt.answer, nil)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestReadResolution_NotFound(t *testing.T) {
	homeDir := t.TempDir()

	answer, resolved, err := ReadResolution(homeDir, "task-1", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resolved {
		t.Error("expected resolved=false for nonexistent hold")
	}
	if answer != "" {
		t.Errorf("expected empty answer, got %q", answer)
	}
}

func TestReadResolution_Unresolved(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")

	answer, resolved, err := ReadResolution(homeDir, "task-1", "approach")
	if err != nil {
		t.Fatal(err)
	}
	if resolved {
		t.Error("expected resolved=false for unresolved hold")
	}
	if answer != "" {
		t.Errorf("expected empty answer, got %q", answer)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

// searchString is a simple substring search to avoid importing strings in test helpers.
func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestVerify_AfterComplete(t *testing.T) {
	homeDir := t.TempDir()

	// Create a hold (appends needs-decision status line).
	Create(homeDir, "task-1", "approach", "Pick framework")

	// Complete it — must append resolved status line so Verify sees it.
	if err := Complete(homeDir, "task-1", []string{"approach"}); err != nil {
		t.Fatal(err)
	}

	// Verify should now pass.
	unresolved, err := Verify(homeDir, "task-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected no unresolved after complete, got %v", unresolved)
	}
}

func TestResolve_WrongOriginID_Fails(t *testing.T) {
	homeDir := t.TempDir()

	Create(homeDir, "task-1", "approach", "Pick framework")

	// Resolving with a different originID than the hold's owner must fail.
	err := Resolve(homeDir, "task-2", "approach", "Choose React", nil)
	if err == nil {
		t.Fatal("expected error for wrong originID, got nil")
	}
}
