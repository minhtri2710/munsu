package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

func initTestRepo(t *testing.T, dir, parentRemote string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "test@test"}, {"config", "user.name", "Test"}, {"remote", "add", "origin", parentRemote}, {"commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := exec.Command("git", "-C", dir, "update-ref", "refs/remotes/origin/main", "HEAD").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main").Run(); err != nil {
		t.Fatal(err)
	}
}

func writeCaptainMeta(t *testing.T, parent, id, captainHome, window string) {
	t.Helper()
	canonical, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(parent, taskIDForCaptain(id), map[string]string{"kind": "captain", "sm_id": id, "home": canonical, "window": window, "backend": "fake"}); err != nil {
		t.Fatal(err)
	}
}

func writeCanonicalPiIntegration(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, ".pi", "extensions", "munsu-pi-integration.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// munsu-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func seedCaptainForTest(t *testing.T, parent, id string) string {
	t.Helper()
	captainHome := filepath.Join(parent, "captains", id)
	for _, dir := range []string{"state", "config", "data"} {
		if err := os.MkdirAll(filepath.Join(captainHome, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# "+id+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, id); err != nil {
		t.Fatal(err)
	}
	// Set up typed documents in parent home BEFORE registering captain.
	setupTypedParentHome(t, parent, id)
	// Register in typed captain registry with captain ID as project name.
	if err := Register(parent, id, captainHome, "captain", id); err != nil {
		t.Fatal(err)
	}
	// Publish the resolved snapshot to the captain home.
	if err := publishResolvedSnapshot(parent, captainHome); err != nil {
		t.Fatal(err)
	}
	return captainHome
}

// createTestPublishedSnapshot writes a minimal published snapshot to the
// captain home so ComputeInheritedConfigDigest and related functions work.
func createTestPublishedSnapshot(t *testing.T, captainHome string) {
	t.Helper()
	resolved := config.ResolvedProjectConfig{
		Project:           "test-project",
		ProjectPath:       captainHome,
		SoldierHarness:    "pi",
		RequireNoMistakes: true,
		Digest:            "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := config.StorePublishedSnapshot(captainHome, resolved); err != nil {
		t.Fatal(err)
	}
}

// setupTypedParentHome creates the basic typed config documents in the parent
// home for use in tests that require PropagateConfig.
func setupTypedParentHome(t *testing.T, parent string, projectName string) {
	t.Helper()
	// Create or update fleet base document with empty config.
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
	}
	if err := config.StoreFleetBase(parent, base); err != nil {
		t.Fatal(err)
	}
	// Read existing project registry and add the new project, to avoid
	// overwriting projects registered by other seedCaptainForTest calls.
	projects := config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
	}
	if existing, err := config.LoadProjectRegistry(parent); err == nil {
		projects.Projects = existing.Projects
	}
	// Check if project already exists.
	for _, p := range projects.Projects {
		if p.Name == projectName {
			return
		}
	}
	projects.Projects = append(projects.Projects, config.ProjectRecord{
		Name: projectName, Path: parent, Mode: "no-mistakes",
	})
	if err := config.StoreProjectRegistry(parent, projects); err != nil {
		t.Fatal(err)
	}
}
