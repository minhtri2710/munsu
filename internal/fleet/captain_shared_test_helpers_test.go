package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
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
	// Initialize the parent as a canonical home so the Fleet Registry (the
	// sole lifecycle authority) can operate on it.
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
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
// The Backend is an explicit fixture literal ("tmux") so StorePublishedSnapshot
// (which fails closed on an empty identity) accepts it.
func createTestPublishedSnapshot(t *testing.T, captainHome string) {
	t.Helper()
	resolved := config.ResolvedProjectConfig{
		Project:           "test-project",
		ProjectPath:       captainHome,
		SoldierHarness:    "pi",
		Backend:           "tmux",
		RequireNoMistakes: true,
		Digest:            "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := config.StorePublishedSnapshot(captainHome, resolved); err != nil {
		t.Fatal(err)
	}
}

// setupTypedParentHome creates the basic typed config documents in the parent
// home for use in tests that require PropagateConfig. The parent is made a
// canonical home so the Fleet Registry (the sole lifecycle authority) can
// operate on it; Project and Captain registration flows through Register.
func setupTypedParentHome(t *testing.T, parent string, projectName string) {
	t.Helper()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	// Create or update fleet base document with the typed Backend identity so
	// publishResolvedSnapshot (config.ResolveProject) resolves a non-empty
	// backend: an empty identity is a typed validation failure at HEAD.
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			Backend: "tmux",
		},
	}
	if err := config.StoreFleetBase(parent, base); err != nil {
		t.Fatal(err)
	}
}

// testProjectRecord carries the scoped Project facts a test fixture registers
// through the canonical Fleet Registry (the sole lifecycle authority).
type testProjectRecord struct {
	Name   string
	Path   string
	Mode   string
	Config config.ProjectOverlay
}

// initTestHome creates a fresh canonical home so the Fleet Registry (the sole
// lifecycle authority) can operate on it. The Fleet Registry is home-backed;
// test fixtures must open a canonical home rather than a plain directory.
func initTestHome(t *testing.T) string {
	t.Helper()
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	return homeDir
}

// testCaptainRecord carries the scoped Captain facts a test fixture registers
// through the canonical Fleet Registry, including its owning Project binding.
type testCaptainRecord struct {
	ID      string
	Home    string
	Project string
}

// storeTestDocuments registers the given base, projects, and captains through
// the canonical Fleet Registry (the sole lifecycle authority) and stores the
// Config-owned project overlays. It mirrors the legacy StoreDocuments fixture
// setup for tests that previously wrote Config-owned registry documents.
func storeTestDocuments(t *testing.T, homeDir string, base config.FleetBaseDocument, projects []testProjectRecord, captains []testCaptainRecord) {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreFleetBase(homeDir, base); err != nil {
		t.Fatal(err)
	}
	r, err := openRegistry(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		projectID, err := domain.NewProjectID(p.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetProject(projectID); err == nil {
			// Project already registered (e.g. seeded earlier); skip to avoid a
			// conflicting-definition error when the fixture re-registers it.
			if err := config.StoreProjectOverlay(homeDir, p.Name, p.Config); err != nil {
				t.Fatal(err)
			}
			continue
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		rev, err := r.ProjectRevision()
		if err != nil {
			t.Fatal(err)
		}
		req := RegisterProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    projectID,
			Name:         p.Name,
			Path:         p.Path,
			Mode:         p.Mode,
			Precondition: preconditionOf(rev),
			Reason:       "test",
		}
		if _, err := r.RegisterProject(opFor(req), req); err != nil {
			t.Fatal(err)
		}
		if err := config.StoreProjectOverlay(homeDir, p.Name, p.Config); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range captains {
		captainID, err := domain.NewCaptainID(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetCaptain(captainID); err == nil {
			// Captain already registered (e.g. seeded earlier); skip to avoid a
			// conflicting-definition error when the fixture re-registers it.
			continue
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		rev, err := r.CaptainRevision()
		if err != nil {
			t.Fatal(err)
		}
		req := RegisterCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    captainID,
			Home:         c.Home,
			Scope:        "",
			Precondition: preconditionOf(rev),
			Reason:       "test",
		}
		if _, err := r.RegisterCaptain(opFor(req), req); err != nil {
			t.Fatal(err)
		}
		if c.Project != "" {
			projectID, err := domain.NewProjectID(c.Project)
			if err != nil {
				t.Fatal(err)
			}
			bindRev, err := r.BindingRevision()
			if err != nil {
				t.Fatal(err)
			}
			bind := BindCaptainRequest{
				HomeID:       r.HomeID(),
				CaptainID:    captainID,
				ProjectID:    projectID,
				Precondition: preconditionOf(bindRev),
				Reason:       "test",
			}
			if _, err := r.BindCaptain(opFor(bind), bind); err != nil {
				t.Fatal(err)
			}
		}
	}
}
