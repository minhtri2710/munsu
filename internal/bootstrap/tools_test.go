package bootstrap

import (
	"testing"
)

func TestCheckedToolsRegistry(t *testing.T) {
	// Verify the registry contains all expected tools and has no duplicates.
	seen := make(map[string]bool)
	for _, spec := range checkedTools {
		if seen[spec.Name] {
			t.Errorf("duplicate tool in checkedTools: %s", spec.Name)
		}
		seen[spec.Name] = true
	}

	expectedTools := []string{"git", "gh-axi", "gh", "fuser"}
	for _, name := range expectedTools {
		if !seen[name] {
			t.Errorf("expected tool %q not found in checkedTools", name)
		}
	}
}
