package config

import (
	"testing"
)

func TestResolveProjectCarriesDispatchAutonomyThroughOverlay(t *testing.T) {
	base := FleetBaseDocument{
		SchemaVersion: FleetBaseSchemaVersion,
		Config:        ProjectOverlay{DispatchAutonomy: "manual", Backend: "tmux"},
	}
	facts := ProjectFacts{
		Name:    "project",
		Path:    "/home/project",
		Overlay: ProjectOverlay{DispatchAutonomy: "safe-reinterpretation"},
	}
	resolved, err := ResolveProject(base, facts, BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DispatchAutonomy != "safe-reinterpretation" {
		t.Fatalf("DispatchAutonomy = %q, want safe-reinterpretation", resolved.DispatchAutonomy)
	}
}
