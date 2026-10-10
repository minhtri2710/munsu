package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

func initTestRepo(t *testing.T, dir, parentRemote string) {
	t.Helper()
	// core.autocrlf is set explicitly because these repos live in t.TempDir()
	// and inherit the host's git config, where it defaults to true on Windows.
	// The charter tests write LF, commit, and then compare the checked-out
	// bytes to what they wrote, so an inherited autocrlf turns a byte-for-byte
	// preservation assertion into an assertion about the host -- #549 group 9.
	// munsu's own checkout is governed by .gitattributes, which cannot reach a
	// repo created here.
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "core.autocrlf", "false"}, {"config", "user.email", "test@test"}, {"config", "user.name", "Test"}, {"remote", "add", "origin", parentRemote}, {"commit", "--allow-empty", "-m", "initial"}} {
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

// piIntegrationPath is the home-relative file the pi captain integration installs.
var piIntegrationPath = ".pi/extensions/" + harness.CanonicalPiIntegrationName

// piInstallingIntegrationPort installs the canonical pi integration file and
// reports it through CaptainPaths, as captainIntegrationAdapter does.
type piInstallingIntegrationPort struct{ t *testing.T }

func (p piInstallingIntegrationPort) EnsureCaptain(home, _ string) error {
	writeCanonicalPiIntegration(p.t, home)
	return nil
}
func (piInstallingIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return IntegrationStatus{State: "installed"}, nil
}
func (piInstallingIntegrationPort) CaptainPaths(string, string) ([]string, error) {
	return []string{piIntegrationPath}, nil
}

func writeCanonicalPiIntegration(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, filepath.FromSlash(piIntegrationPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// munsu-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// worktreeCaptainHome builds an unregistered managed-worktree captain home
// at captainHome, mirroring seed for the fixture's pi captain harness.
func worktreeCaptainHome(t *testing.T, captainHome, id string) {
	t.Helper()
	repo := newWorktreeFixture(t)
	gitTestRun(t, repo, "worktree", "add", "--detach", captainHome, "origin/main")
	if err := writeWorktreeExcludes(captainHome, captainWorktreeExcludes(piIntegrationPath)); err != nil {
		t.Fatal(err)
	}
	if err := writeCaptainProvenance(captainHome, repo); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"state", "config", "data"} {
		if err := os.MkdirAll(filepath.Join(captainHome, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeCaptainCharter(captainHome, "# "+id+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := home.SeedCaptainProvenance(captainHome, id); err != nil {
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
	worktreeCaptainHome(t, captainHome, id)
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
		Project:        "test-project",
		ProjectPath:    captainHome,
		SoldierHarness: "pi",
		Backend:        "tmux",
		CaptainProfile: config.CaptainProfile{Harness: "pi"},
		Digest:         "0000000000000000000000000000000000000000000000000000000000000000",
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
	// publishResolvedSnapshot (config resolver) resolves a non-empty
	// backend: an empty identity is a typed validation failure at HEAD.
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.FleetBaseConfig{
			Backend: "tmux",
		},
		CaptainProfile: config.CaptainProfile{Harness: "pi"},
	}
	if err := config.StoreFleetBase(parent, base); err != nil {
		t.Fatal(err)
	}
}

// captainHomeWithSnapshot creates a captain home whose published snapshot
// carries the given CaptainProfile. The snapshot is the ONLY captain harness
// identity source for recovery/launch operations; no flat pins are consulted.
func captainHomeWithSnapshot(t *testing.T, profile config.CaptainProfile) string {
	t.Helper()
	home := t.TempDir()
	resolved := config.ResolvedProjectConfig{
		Project:        "test-project",
		ProjectPath:    home,
		Backend:        "tmux",
		CaptainProfile: profile,
		Digest:         "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := config.StorePublishedSnapshot(home, resolved); err != nil {
		t.Fatal(err)
	}
	return home
}

// testProjectRecord carries the scoped Project facts a test fixture registers
// through the canonical Fleet Registry (the sole lifecycle authority).
type testProjectRecord struct {
	Name   string
	Path   string
	Config config.ProjectOverlay
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

func gitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newWorktreeFixture creates a remote, a project clone on main with one
// commit, pushes, sets origin/HEAD, and returns the project repo path.
func newWorktreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	project := filepath.Join(root, "project")
	if out, err := exec.Command("git", "clone", remote, project).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	// See initTestRepo: a repo built in t.TempDir() inherits the host's
	// core.autocrlf, and the managed-worktree tests compare checked-out bytes
	// to the bytes they wrote. Set on the clone rather than the bare remote
	// because the linked worktree SeedFromWorktree creates shares this config.
	gitTestRun(t, project, "config", "core.autocrlf", "false")
	gitTestRun(t, project, "config", "user.name", "Munsu Test")
	gitTestRun(t, project, "config", "user.email", "munsu@example.invalid")
	gitTestRun(t, project, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# Project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, project, "add", "README.md")
	gitTestRun(t, project, "commit", "-m", "initial")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	gitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitTestRun(t, project, "remote", "set-head", "origin", "main")
	// Re-fetch so origin/HEAD resolves.
	gitTestRun(t, project, "fetch", "origin")
	return project
}
