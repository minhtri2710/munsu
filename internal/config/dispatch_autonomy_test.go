package config

import (
	"path/filepath"
	"testing"
)

func TestResolveProjectCarriesDispatchAutonomyThroughOverlay(t *testing.T) {
	home := t.TempDir()
	base := FleetBaseDocument{SchemaVersion: FleetBaseSchemaVersion, Config: ProjectOverlay{DispatchAutonomy: "manual"}}
	captains := CaptainRegistryDocument{SchemaVersion: CaptainRegistrySchemaVersion, Captains: []CaptainRecord{{ID: "captain", Home: filepath.Join(home, "captain"), Project: "project"}}}
	projects := ProjectRegistryDocument{SchemaVersion: ProjectRegistrySchemaVersion, Projects: []ProjectRecord{{Name: "project", Path: filepath.Join(home, "project"), Config: ProjectOverlay{DispatchAutonomy: "safe-reinterpretation"}}}}
	resolved, err := ResolveProject(base, captains, projects, "project", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DispatchAutonomy != "safe-reinterpretation" {
		t.Fatalf("DispatchAutonomy = %q, want safe-reinterpretation", resolved.DispatchAutonomy)
	}
}
