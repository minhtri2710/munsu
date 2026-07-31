package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateDocumentsRejectsIndependentSchemaVersions(t *testing.T) {
	base, captains, projects := validDocuments()
	for _, tc := range []struct {
		name     string
		edit     func()
		validate func() error
	}{
		{name: "base", edit: func() { base.SchemaVersion = "future" }, validate: func() error { return base.Validate() }},
		{name: "captains", edit: func() { captains.SchemaVersion = "future" }, validate: func() error { return captains.Validate() }},
		{name: "projects", edit: func() { projects.SchemaVersion = "future" }, validate: func() error { return projects.Validate() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, captains, projects = validDocuments()
			tc.edit()
			if err := tc.validate(); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
				t.Fatalf("Validate() error = %v, want schemaVersion refusal", err)
			}
		})
	}
}

func TestValidateFleetBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CaptainRegistryDocument, *ProjectRegistryDocument)
		want   string
	}{
		{name: "captain missing project", mutate: func(c *CaptainRegistryDocument, _ *ProjectRegistryDocument) { c.Captains[0].Project = "" }, want: "exactly one project"},
		{name: "unknown project", mutate: func(c *CaptainRegistryDocument, _ *ProjectRegistryDocument) { c.Captains[0].Project = "missing" }, want: "unknown project"},
		{name: "duplicate owner", mutate: func(c *CaptainRegistryDocument, _ *ProjectRegistryDocument) {
			c.Captains = append(c.Captains, CaptainRecord{ID: "c2", Home: "/c2", Project: "alpha"})
		}, want: "already owned"},
		{name: "empty Captain home", mutate: func(c *CaptainRegistryDocument, _ *ProjectRegistryDocument) { c.Captains[0].Home = "" }, want: "home is required"},
		{name: "empty project path", mutate: func(_ *CaptainRegistryDocument, p *ProjectRegistryDocument) { p.Projects[0].Path = "" }, want: "path is required"},
		{name: "duplicate Captain id", mutate: func(c *CaptainRegistryDocument, _ *ProjectRegistryDocument) {
			c.Captains = append(c.Captains, CaptainRecord{ID: "c1", Home: "/c2", Project: "beta"})
		}, want: "duplicate Captain"},
		{name: "duplicate project name", mutate: func(_ *CaptainRegistryDocument, p *ProjectRegistryDocument) {
			p.Projects = append(p.Projects, ProjectRecord{Name: "alpha", Path: "/other"})
		}, want: "duplicate project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, c, p := validDocuments()
			tc.mutate(&c, &p)
			if err := ValidateFleetBindings(c, p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateFleetBindings() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveProjectConfigDistinctProjectsAndCaptainFallback(t *testing.T) {
	base, captains, projects := validDocuments()
	alpha, err := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ResolveProject(base, captains, projects, "beta", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if alpha.SoldierHarness != "claude" || beta.SoldierHarness != "codex" {
		t.Fatalf("resolved harnesses = %q/%q", alpha.SoldierHarness, beta.SoldierHarness)
	}
	if alpha.DefaultMode != "direct-pr" || beta.DefaultMode != "direct-pr" {
		t.Fatalf("project mode alias not resolved: %q/%q", alpha.DefaultMode, beta.DefaultMode)
	}
	if alpha.CaptainProfile.Harness != "pi" || alpha.CaptainProfile.Model != "base-model" {
		t.Fatalf("captain fallback = %+v", alpha.CaptainProfile)
	}
	if len(alpha.DispatchProfiles) != 1 || alpha.DispatchProfiles[0].Name != "alpha" || len(beta.DispatchProfiles) != 1 || beta.DispatchProfiles[0].Name != "base" {
		t.Fatalf("dispatch profiles alpha=%+v beta=%+v", alpha.DispatchProfiles, beta.DispatchProfiles)
	}
}

func TestResolveProjectOverlayDefaultModeOverridesProjectModeAlias(t *testing.T) {
	base, captains, projects := validDocuments()
	projects.Projects[0].Config.DefaultMode = "no-mistakes"
	resolved, err := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DefaultMode != "no-mistakes" {
		t.Fatalf("DefaultMode = %q, want overlay value", resolved.DefaultMode)
	}
}

func TestResolveProjectConfigBoundaryOverridesAndImmutability(t *testing.T) {
	base, captains, projects := validDocuments()
	before := projects.Projects[0].Config.DispatchProfiles[0].Harness
	resolved, err := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{Model: "env-model", DefaultMode: "direct-pr"})
	if err != nil {
		t.Fatal(err)
	}
	resolved.DispatchProfiles[0].Harness = "changed"
	if projects.Projects[0].Config.DispatchProfiles[0].Harness != before {
		t.Fatal("resolver mutated or shared dispatch profile storage")
	}
	if resolved.Model != "env-model" || resolved.DefaultMode != "direct-pr" {
		t.Fatalf("boundary overrides not applied: %+v", resolved)
	}
}

func TestProjectDigestIsDeterministicAndTargeted(t *testing.T) {
	base, captains, projects := validDocuments()
	alpha := projects.Projects[0]
	beta := projects.Projects[1]
	a1, _ := ProjectDigest(base, alpha)
	a2, _ := ProjectDigest(base, alpha)
	b1, _ := ProjectDigest(base, beta)
	if a1 != a2 {
		t.Fatalf("digest is not deterministic: %s != %s", a1, a2)
	}
	alpha.Config.Model = "changed"
	a3, _ := ProjectDigest(base, alpha)
	b2, _ := ProjectDigest(base, beta)
	if a1 == a3 {
		t.Fatal("alpha digest did not change")
	}
	if b1 != b2 {
		t.Fatal("beta digest changed for alpha-only overlay")
	}
	alpha.Config.Model = ""
	base.Config.Model = "new-base"
	a4, _ := ProjectDigest(base, alpha)
	b3, _ := ProjectDigest(base, beta)
	if a1 == a4 || b1 == b3 {
		t.Fatal("base change must change every project digest")
	}
	captains.Captains[0].CaptainProfile.Model = "captain-only"
	resolvedCaptain, _ := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{})
	resolvedBoundary, _ := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{Model: "boundary-only"})
	if resolvedCaptain.Digest != a4 || resolvedBoundary.Digest != a4 {
		t.Fatal("Captain profile or boundary override entered project digest")
	}
	alpha.Config.DefaultMode = "no-mistakes"
	withOverlay, _ := ProjectDigest(base, alpha)
	alpha.Mode = "direct-pr"
	stillOverlay, _ := ProjectDigest(base, alpha)
	if withOverlay != stillOverlay {
		t.Fatal("project mode overrode explicit overlay DefaultMode in digest")
	}
}

func TestResolvedSnapshotIsFrozenAndReturnsDeepCopies(t *testing.T) {
	home := t.TempDir()
	base, captains, projects := validDocuments()
	projects.Projects[0].Config.DispatchProfiles[0].Match = []string{"alpha"}
	projects.Projects[0].Config.DispatchProfiles[0].Use = []DispatchCandidate{{Harness: "claude"}}
	if err := StoreDocuments(home, base, captains, projects); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadResolvedSnapshot(home, "alpha", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	first := snapshot.Config()
	first.DispatchProfiles[0].Match[0] = "changed"
	first.DispatchProfiles[0].Use[0].Harness = "changed"
	if snapshot.Config().DispatchProfiles[0].Match[0] != "alpha" || snapshot.Config().DispatchProfiles[0].Use[0].Harness != "claude" {
		t.Fatal("snapshot accessor shares nested mutable storage")
	}
	projects.Projects[0].Config.Model = "new-on-disk"
	if err := StoreProjectRegistry(home, projects); err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().Model == "new-on-disk" {
		t.Fatal("existing snapshot observed later disk write")
	}
	newSnapshot, err := LoadResolvedSnapshot(home, "alpha", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if newSnapshot.Config().Model != "new-on-disk" {
		t.Fatal("new snapshot did not observe disk write")
	}
}

func TestResolveProjectDoesNotReadEnvironment(t *testing.T) {
	t.Setenv("MUNSU_MODEL_OVERRIDE", "environment-model")
	base, captains, projects := validDocuments()
	resolved, err := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "base-model" {
		t.Fatalf("resolver read process environment: %q", resolved.Model)
	}
	overridden, err := ResolveProject(base, captains, projects, "alpha", BoundaryOverrides{Model: "typed-boundary"})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Model != "typed-boundary" {
		t.Fatalf("typed override = %q", overridden.Model)
	}
}

func TestDocumentStoreRoundTripAndStrictDecode(t *testing.T) {
	home := t.TempDir()
	base, captains, projects := validDocuments()
	if err := StoreDocuments(home, base, captains, projects); err != nil {
		t.Fatal(err)
	}
	gotBase, gotCaptains, gotProjects, err := LoadDocuments(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base, gotBase) || !reflect.DeepEqual(captains, gotCaptains) || !reflect.DeepEqual(projects, gotProjects) {
		t.Fatalf("round trip mismatch\nbase=%+v\ncaptains=%+v\nprojects=%+v", gotBase, gotCaptains, gotProjects)
	}
	path := filepath.Join(home, BaseDocumentPath)
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "{", "{\"unknown\":true,", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadDocuments(home); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadDocuments() error = %v, want strict decode refusal", err)
	}
}

func validDocuments() (FleetBaseDocument, CaptainRegistryDocument, ProjectRegistryDocument) {
	base := FleetBaseDocument{SchemaVersion: FleetBaseSchemaVersion, Config: ProjectOverlay{SoldierHarness: "pi", Model: "base-model", DefaultMode: "no-mistakes", DispatchProfiles: []DispatchProfile{{Name: "base", Harness: "pi"}}}, CaptainProfile: CaptainProfile{Harness: "pi", Model: "base-model"}}
	captains := CaptainRegistryDocument{SchemaVersion: CaptainRegistrySchemaVersion, Captains: []CaptainRecord{{ID: "c1", Home: "/c1", Project: "alpha", CaptainProfile: CaptainProfile{Harness: "pi"}}}}
	projects := ProjectRegistryDocument{SchemaVersion: ProjectRegistrySchemaVersion, Projects: []ProjectRecord{{Name: "alpha", Path: "/alpha", Mode: "direct-pr", Config: ProjectOverlay{SoldierHarness: "claude", DispatchProfiles: []DispatchProfile{{Name: "alpha", Harness: "claude"}}}}, {Name: "beta", Path: "/beta", Mode: "direct-pr", Config: ProjectOverlay{SoldierHarness: "codex"}}}}
	return base, captains, projects
}
