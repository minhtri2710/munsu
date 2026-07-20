package bootstrap

import (
	"testing"
)

func TestIsHardRequired(t *testing.T) {
	tests := []struct {
		tool     string
		required bool
	}{
		{"git", true},
		{"tmux", true},
		{"treehouse", false},
		{"no-mistakes", false},
		{"tasks-axi", false},
		{"gh-axi", false},
		{"gh", false},
		{"herdr", false},
		{"zellij", false},
		{"unknown-tool", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsHardRequired(tt.tool)
		if got != tt.required {
			t.Errorf("IsHardRequired(%q) = %v, want %v", tt.tool, got, tt.required)
		}
	}
}

func TestCheckedToolsRegistry(t *testing.T) {
	// Verify the registry contains all expected tools and has no duplicates.
	seen := make(map[string]bool)
	for _, spec := range checkedTools {
		if seen[spec.Name] {
			t.Errorf("duplicate tool in checkedTools: %s", spec.Name)
		}
		seen[spec.Name] = true
	}

	expectedTools := []string{"git", "tmux", "treehouse", "zellij", "no-mistakes", "tasks-axi", "gh-axi", "gh"}
	for _, name := range expectedTools {
		if !seen[name] {
			t.Errorf("expected tool %q not found in checkedTools", name)
		}
	}
}
