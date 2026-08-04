package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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

func TestIsHardRequiredByConfig(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		mutate func(string)
		want   bool
	}{
		{name: "non-no-mistakes tool never hard-required by config", tool: "git", want: false},
		{name: "no base document -> false", tool: "no-mistakes", want: false},
		{name: "base requireNoMistakes unset -> false", tool: "no-mistakes", mutate: func(home string) {
			if err := config.StoreFleetBase(home, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}); err != nil {
				t.Fatal(err)
			}
		}, want: false},
		{name: "base requireNoMistakes true -> true (presence semantics)", tool: "no-mistakes", mutate: func(home string) {
			if err := config.StoreFleetBase(home, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.ProjectOverlay{RequireNoMistakes: &[]bool{true}[0]},
			}); err != nil {
				t.Fatal(err)
			}
		}, want: true},
		{name: "base requireNoMistakes false -> false", tool: "no-mistakes", mutate: func(home string) {
			if err := config.StoreFleetBase(home, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.ProjectOverlay{RequireNoMistakes: &[]bool{false}[0]},
			}); err != nil {
				t.Fatal(err)
			}
		}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.mutate != nil {
				tt.mutate(home)
			}
			got, err := IsHardRequiredByConfig(home, tt.tool)
			if err != nil {
				t.Fatalf("IsHardRequiredByConfig(%q) unexpected error: %v", tt.tool, err)
			}
			if got != tt.want {
				t.Errorf("IsHardRequiredByConfig(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}

func TestIsHardRequiredByConfig_ReturnsReadErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(string)
	}{
		{name: "malformed base -> error propagates", setup: func(home string) {
			if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "config", "base.json"), []byte("{not-json"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "base path is a directory (EISDIR) -> error propagates", setup: func(home string) {
			if err := os.MkdirAll(filepath.Join(home, "config", "base.json"), 0755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unreadable base (permission) -> error propagates", setup: func(home string) {
			if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "config", "base.json")
			if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0000); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			tt.setup(home)
			got, err := IsHardRequiredByConfig(home, "no-mistakes")
			if err == nil {
				t.Fatalf("expected error for %s, got (false, nil)", tt.name)
			}
			if got {
				t.Errorf("expected false on error, got true")
			}
		})
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
