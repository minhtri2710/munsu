package config

import (
	"strings"
	"testing"
)

func guardTestBase() FleetBaseDocument {
	return FleetBaseDocument{
		SchemaVersion: FleetBaseSchemaVersion,
		Config:        ProjectOverlay{Backend: "tmux"},
	}
}

func TestGuardBurnDownFinalResolvedOverlayRejectsEmptyProjectFacts(t *testing.T) {
	_, err := ResolveProject(guardTestBase(), ProjectFacts{Path: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "project name is required") {
		t.Fatalf("ResolveProject error = %v, want empty-project-name refusal", err)
	}
}

func TestGuardBurnDownFinalResolvedOverlayRejectsEmptyProjectPath(t *testing.T) {
	_, err := ResolveProject(guardTestBase(), ProjectFacts{Name: "project"})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("ResolveProject error = %v, want empty-project-path refusal", err)
	}
}

func TestGuardBurnDownPublishedSnapshotValidateRefusals(t *testing.T) {
	base := PublishedSnapshotDocument{
		SchemaVersion: PublishedSnapshotSchemaVersion,
		Config: ResolvedProjectConfig{
			Project:     "project",
			ProjectPath: "/tmp/project",
			Digest:      "digest",
			Backend:     "tmux",
		},
	}
	cases := []struct {
		name string
		edit func(*PublishedSnapshotDocument)
		want string
	}{
		{"project", func(d *PublishedSnapshotDocument) { d.Config.Project = "" }, "project is required"},
		{"project path", func(d *PublishedSnapshotDocument) { d.Config.ProjectPath = "" }, "project path is required"},
		{"digest", func(d *PublishedSnapshotDocument) { d.Config.Digest = "" }, "digest is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := base
			tc.edit(&document)
			err := document.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}
}
