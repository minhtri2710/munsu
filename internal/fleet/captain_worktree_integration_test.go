package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func seedPiWorktreeCaptain(t *testing.T, repo string) (string, string) {
	t.Helper()
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })
	t.Setenv("MUNSU_ROLE", "")
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	setupTypedParentHome(t, parent, "wt")
	h := filepath.Join(parent, "captains", "wt")
	if err := os.MkdirAll(h, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "wt", h, "", "wt"); err != nil {
		t.Fatal(err)
	}
	if err := SeedCaptain(CaptainSeedOptions{ID: "wt", Home: h, Repo: repo, ParentHome: parent, Integration: piInstallingIntegrationPort{t: t}}); err != nil {
		t.Fatal(err)
	}
	return parent, h
}

// TestLaunch_WorktreeCaptainWithInstalledIntegrationLaunches proves a managed
// worktree captain stays launchable after its harness integration is installed:
// seed excludes exactly the paths the integration reports.
func TestLaunch_WorktreeCaptainWithInstalledIntegrationLaunches(t *testing.T) {
	parent, h := seedPiWorktreeCaptain(t, newWorktreeFixture(t))
	if _, err := os.Stat(filepath.Join(h, filepath.FromSlash(piIntegrationPath))); err != nil {
		t.Fatalf("integration not installed: %v", err)
	}
	if err := Launch(h, parent, testLaunchEndpoint{}, fakeIntegrationPort{}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
}

// TestLaunch_TrackedIntegrationPathIsRefusedByName proves that a path the
// project repo tracks is not silently excluded: once the integration changes
// it, launch refuses and names the path.
func TestLaunch_TrackedIntegrationPathIsRefusedByName(t *testing.T) {
	repo := newWorktreeFixture(t)
	tracked := filepath.Join(repo, filepath.FromSlash(piIntegrationPath))
	if err := os.MkdirAll(filepath.Dir(tracked), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("// project-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, repo, "add", piIntegrationPath)
	gitTestRun(t, repo, "commit", "-m", "track pi integration")
	gitTestRun(t, repo, "push", "origin", "main")
	gitTestRun(t, repo, "fetch", "origin")

	parent, h := seedPiWorktreeCaptain(t, repo)
	err := Launch(h, parent, testLaunchEndpoint{}, fakeIntegrationPort{})
	if err == nil || !strings.Contains(err.Error(), "tracked changes: "+piIntegrationPath) {
		t.Fatalf("Launch error = %v, want tracked-change refusal naming %s", err, piIntegrationPath)
	}
}
