//go:build integration

package fleet

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// fakeBinDir is a temp directory with fake pi/munsu binaries prepended to PATH
// by TestMain. Tests that need the real PATH can restore it.
var fakeBinDir string
var origPath string

// TestMain creates fake pi and munsu binaries in a temp PATH fixture so captain
// unit tests never depend on the installed Pi binary. Tests that explicitly
// validate an installed Pi (e.g., runtime integration tests) should restore
// the original PATH via t.Setenv("PATH", origPath).
func TestMain(m *testing.M) {
	var cleanup func()
	fakeBinDir, cleanup = setupFakeBins()
	origPath = os.Getenv("PATH")
	os.Setenv("PATH", fakeBinDir+string(filepath.ListSeparator)+origPath)

	code := m.Run()

	cleanup()
	os.Setenv("PATH", origPath)
	os.Exit(code)
}

// setupFakeBins creates executable shims for pi and munsu in a temp directory
// and returns the directory path and a cleanup function.
func setupFakeBins() (string, func()) {
	dir, err := os.MkdirTemp("", "captain-test-bins-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "captain TestMain: creating temp dir: %v\n", err)
		os.Exit(1)
	}

	// Fake pi: returns a supported version.
	piShim := filepath.Join(dir, "pi")
	if err := os.WriteFile(piShim, []byte("#!/bin/sh\necho '0.79.0'\n"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "captain TestMain: writing pi shim: %v\n", err)
		os.Exit(1)
	}

	// Fake node: returns "API probe passed" so probePiAPIs succeeds.
	nodeShim := filepath.Join(dir, "node")
	if err := os.WriteFile(nodeShim, []byte("#!/bin/sh\necho 'API probe passed'\n"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "captain TestMain: writing node shim: %v\n", err)
		os.Exit(1)
	}

	// Fake munsu: needed by EnsureCaptainPiExtensions for path resolution.
	munsuShim := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuShim, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "captain TestMain: writing munsu shim: %v\n", err)
		os.Exit(1)
	}

	return dir, func() { os.RemoveAll(dir) }
}

// --- Legacy registry / config-inheritance helpers ---
//
// ParseRegistry, RegistryPath, and getInheritableList were removed from the
// fleet package during the legacy-config hard cut. Their current production
// owners live unexported in internal/configmigration; these test-local ports
// mirror those owners so legacy-format registry tests keep compiling.

// ParseRegistry parses a legacy captains.md registry file and returns Info
// entries. Mirrors configmigration.parseRegistry (semantics preserved from the
// former fleet ParseRegistry).
func ParseRegistry(registryPath string) ([]Info, error) {
	f, err := os.Open(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening registry %s: %w", registryPath, err)
	}
	defer f.Close()

	var mates []Info
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		parts := strings.SplitN(rest, " - ", 2)
		if len(parts) < 1 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		if id == "" {
			continue
		}
		entry := Info{ID: id}

		if len(parts) >= 2 {
			metaPart := parts[1]
			if idx := strings.LastIndex(metaPart, "("); idx >= 0 {
				meta := metaPart[idx+1:]
				if endIdx := strings.LastIndex(meta, ")"); endIdx >= 0 {
					meta = meta[:endIdx]
				}
				entry.Home = extractMetaValue(meta, "home:")
				entry.Scope = extractMetaValue(meta, "scope:")
				entry.Project = extractMetaValue(meta, "projects:")
				entry.Added = extractMetaValue(meta, "added:")
			}
		}

		mates = append(mates, entry)
	}
	return mates, scanner.Err()
}

// extractMetaValue pulls the value for key out of a legacy captains.md
// meta block (key: value; ...). Mirrors configmigration.extractMetaValue.
func extractMetaValue(meta, key string) string {
	parts := strings.Split(meta, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, key) {
			v := strings.TrimSpace(strings.TrimPrefix(p, key))
			return v
		}
	}
	return ""
}

// RegistryPath returns the path to the legacy projects.md registry file.
// Mirrors configmigration.registryPath.
func RegistryPath(homeDir string) string {
	return filepath.Join(homeDir, "data", "projects.md")
}

// getInheritableList returns the list of inheritable config file names.
// Mirrors configmigration.getInheritableList (current behavior).
func getInheritableList() []string {
	env := os.Getenv("MUNSU_INHERITABLE_CONFIG")
	if env != "" {
		return strings.Split(env, ":")
	}
	return []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"}
}

// --- BuildLaunchArgs tests (preserved from PR1) ---

func TestCaptainIDFromTask(t *testing.T) {
	if got := CaptainIDFromTask("captain:munsu", map[string]string{"sm_id": "munsu"}); got != "munsu" {
		t.Errorf("got %q", got)
	}
	if got := CaptainIDFromTask("captain:munsu", nil); got != "munsu" {
		t.Errorf("prefix fallback got %q", got)
	}
	if got := CaptainIDFromTask("captain:other", map[string]string{"sm_id": "real"}); got != "real" {
		t.Errorf("sm_id prefer got %q", got)
	}
}

func TestBuildLaunchArgs_VerifiedCaptainHarness(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	charter := []byte("# Test charter\n\nFollow this exactly.\n")
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), charter, 0644); err != nil {
		t.Fatal(err)
	}
	writeCanonicalPiIntegration(t, smHome)

	binName, args, err := buildLaunchArgs(smHome, harness.Pi, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs() error: %v", err)
	}
	if binName != "pi" {
		t.Fatalf("binName = %q, want pi", binName)
	}
	if len(args) != 4 || args[0] != "-e" || args[2] != "--append-system-prompt" {
		t.Fatalf("args = %#v, want canonical integration and system-context charter", args)
	}
	prompt := args[3]
	if !strings.Contains(prompt, "[mu-system:captain-bootstrap]") || !strings.Contains(prompt, "<captain-charter>") {
		t.Fatalf("prompt missing bootstrap identity or charter wrapper: %q", prompt)
	}
	if strings.Count(prompt, string(charter)) != 1 {
		t.Fatalf("prompt must embed resolved charter exactly once: %q", prompt)
	}
	if strings.Contains(prompt, "Read .captain-charter.md") || strings.Contains(prompt, "munsu session-start") || strings.Contains(prompt, "state/.inbox") {
		t.Fatalf("prompt requests model-driven initialization or filesystem inspection: %q", prompt)
	}
}

func TestBuildLaunchArgs_PiLoadsCanonicalIntegrationExactlyOnce(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("# charter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeCanonicalPiIntegration(t, home)

	_, args, err := buildLaunchArgs(home, harness.Pi, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".pi", "extensions", harness.CanonicalPiIntegrationName)
	loads := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "-e" {
			loads++
			if i+1 >= len(args) || args[i+1] != canonical {
				t.Fatalf("extension args = %v, want canonical path %s", args, canonical)
			}
		}
	}
	if loads != 1 {
		t.Fatalf("extension load count = %d, want 1; args=%v", loads, args)
	}
	joined := strings.Join(args, " ")
	for _, alias := range harness.PiIntegrationAliasNames() {
		if strings.Contains(joined, alias) {
			t.Fatalf("args contain compatibility alias %q: %v", alias, args)
		}
	}
}

func TestBuildLaunchArgs_PiMissingCanonicalIntegrationFailsClosed(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("# charter\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := buildLaunchArgs(home, harness.Pi, t.TempDir())
	if err == nil {
		t.Fatal("expected missing canonical integration error")
	}
	if !strings.Contains(err.Error(), "canonical Pi integration") || !strings.Contains(err.Error(), "munsu integrate repair --harness pi --scope project") {
		t.Fatalf("error = %v, want actionable canonical integration repair", err)
	}
}

func TestBuildLaunchArgs_UnverifiedCaptainHarnesses(t *testing.T) {
	for _, name := range []string{harness.Claude, harness.Codex, harness.Opencode, harness.Grok, harness.Agy} {
		t.Run(name, func(t *testing.T) {
			_, _, err := buildLaunchArgs(t.TempDir(), name, t.TempDir())
			if err == nil {
				t.Fatal("expected unverified captain contract error")
			}
			if !strings.Contains(err.Error(), "does not have a verified captain launch contract") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBuildLaunchArgs_MissingCharterFailsClosed(t *testing.T) {
	_, _, err := buildLaunchArgs(t.TempDir(), harness.Pi, t.TempDir())
	if err == nil {
		t.Fatal("expected missing AGENTS.md error")
	}
	if !strings.Contains(err.Error(), "reading captain charter") {
		t.Fatalf("error = %v", err)
	}
}

func TestCaptainBuildLaunchArgs_UnknownHarness(t *testing.T) {
	_, _, err := buildLaunchArgs("/tmp", "unknown_harness", "/tmp")
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "not a verified harness") {
		t.Errorf("error should mention unverified harness, got: %v", err)
	}
}

func TestBuildLaunchArgs_ConfigModelPropagation(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)
	writeCanonicalPiIntegration(t, smHome)

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	model := "opencode-go/deepseek-v4-flash"
	os.WriteFile(filepath.Join(configDir, "model"), []byte(model+"\n"), 0644)

	binName, args, err := buildLaunchArgs(smHome, harness.Pi, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs error: %v", err)
	}
	if binName != "pi" {
		t.Errorf("binName = %q, want %q", binName, "pi")
	}

	wantPrefix := []string{"--model", model}
	if len(args) < len(wantPrefix) {
		t.Fatalf("args = %v, want prefix %v", args, wantPrefix)
	}
	for i := range wantPrefix {
		if args[i] != wantPrefix[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantPrefix[i])
		}
	}
}

// --- Seed tests ---

func TestDefaultCaptainCharter_ContainsReturnChannel(t *testing.T) {
	parent := "/tmp/marshal-home"
	charter := DefaultCaptainCharter("api", parent)
	if !strings.Contains(charter, home.FromGeneralLabel) {
		t.Fatalf("charter missing marshal marker label")
	}
	status := filepath.Join(parent, "state", "captain:api.status")
	if !strings.Contains(charter, status) {
		t.Fatalf("charter missing status path %q", status)
	}
	if !strings.Contains(charter, "PRIMARY status path") {
		t.Fatalf("charter missing PRIMARY status path doctrine")
	}
	if !strings.Contains(charter, "Delivery / Merge Authorization") || !strings.Contains(charter, "munsu teardown") {
		t.Fatalf("charter missing delivery/merge / teardown duty")
	}
	if !strings.Contains(charter, "Downlink: Captain") {
		t.Fatalf("charter missing downlink: Captain → Soldier doctrine")
	}
}

func TestSeedWithParent_WritesDefaultCaptainCharter(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "api")
	if err := seedWithParentTest("api", sm, parent, ""); err != nil {
		t.Fatal(err)
	}
	// The canonical charter lives in .captain-charter.md.
	body, err := os.ReadFile(filepath.Join(sm, CaptainCharterName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "captain:api.status") {
		t.Fatalf("default charter missing status file path, got: %s", body)
	}
	// AGENTS.md should be a minimal pointer, not the full charter.
	agentsBody, err := os.ReadFile(filepath.Join(sm, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsBody), ".captain-charter.md") {
		t.Fatalf("AGENTS.md should point to .captain-charter.md, got: %s", agentsBody)
	}
}

func TestSeed_CreatesDirectoryStructure(t *testing.T) {
	tmp := t.TempDir()
	homePath := filepath.Join(tmp, "captains", "test-sm")
	charter := "# Captain charter\n\nPersistent domain supervisor.\n"

	if err := seedTest("test-sm", homePath, charter); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(homePath); os.IsNotExist(err) {
		t.Fatalf("home dir %s was not created", homePath)
	}

	for _, dir := range []string{"state", "data", "config", "projects"} {
		p := filepath.Join(homePath, dir)
		if fi, err := os.Stat(p); err != nil {
			t.Errorf("subdirectory %s not created: %v", dir, err)
		} else if !fi.IsDir() {
			t.Errorf("%s exists but is not a directory", p)
		}
	}

	// The canonical charter is written to .captain-charter.md.
	charterPath := filepath.Join(homePath, CaptainCharterName)
	data, err := os.ReadFile(charterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != charter {
		t.Errorf("%s content = %q, want %q", CaptainCharterName, string(data), charter)
	}

	// AGENTS.md should be a minimal pointer.
	agentsPath := filepath.Join(homePath, "AGENTS.md")
	agentsData, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsData), ".captain-charter.md") {
		t.Errorf("AGENTS.md should point to .captain-charter.md, got: %s", agentsData)
	}

	markerData, err := os.ReadFile(filepath.Join(homePath, ProvenanceMarkerName))
	if err != nil {
		t.Fatal("provenance marker was not created:", err)
	}
	if !strings.Contains(string(markerData), "test-sm") {
		t.Errorf("provenance marker should contain id, got: %q", string(markerData))
	}
	if !strings.Contains(string(markerData), ProvenanceVersion) {
		t.Errorf("provenance marker should contain version, got: %q", string(markerData))
	}
}

func TestSeed_InvalidPath(t *testing.T) {
	err := seedTest("test-sm", "/nonexistent/parent/sm", "# charter")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// --- SeedWorktree tests ---

// initTestRepo creates a minimal git repo in dir with an initial commit
// on the default branch (main). The origin remote is set to parentRemote.

func TestSeedWorktree_CreatesWorktreeAndStructure(t *testing.T) {
	parent := t.TempDir()
	// If parent is a git repo with an origin, it needs one for remote validation.
	initTestRepo(t, parent, "https://github.com/test/repo.git")

	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify it's a git worktree.
	if _, err := os.Stat(filepath.Join(homePath, ".git")); err != nil {
		t.Fatal("worktree .git file not found")
	}

	// Verify directory structure.
	for _, dir := range []string{"state", "data", "config", "projects"} {
		p := filepath.Join(homePath, dir)
		if fi, err := os.Stat(p); err != nil {
			t.Errorf("subdirectory %s not created: %v", dir, err)
		} else if !fi.IsDir() {
			t.Errorf("%s exists but is not a directory", p)
		}
	}

	// Verify .captain-charter.md (untracked charter).
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s not created: %v", CaptainCharterName, err)
	}

	// Verify provenance home.
	if _, err := os.Stat(filepath.Join(homePath, ProvenanceMarkerName)); err != nil {
		t.Errorf("provenance marker not created: %v", err)
	}

	// Verify excludes are in info/exclude (not tracked .gitignore).
	gitPtrData, gErr := os.ReadFile(filepath.Join(homePath, ".git"))
	if gErr != nil {
		t.Fatal(gErr)
	}
	gitdirLine := strings.TrimSpace(string(gitPtrData))
	if !strings.HasPrefix(gitdirLine, "gitdir: ") {
		t.Fatalf(".git is not a gitdir pointer: %q", gitdirLine)
	}
	commonDir := filepath.Dir(filepath.Dir(strings.TrimPrefix(gitdirLine, "gitdir: ")))
	if _, err := os.Stat(filepath.Join(commonDir, "info", "exclude")); err != nil {
		t.Errorf("info/exclude not created: %v", err)
	}

	// Verify registered in parent.
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range mates {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("captain %s not registered in parent", id)
	}
}

func TestSeedWorktree_NonGitSource(t *testing.T) {
	parent := t.TempDir()
	repo := t.TempDir() // not a git repo

	err := seedFromWorktreeTest("test", "", repo, parent, "", false, "")
	if err == nil {
		t.Fatal("expected error for non-git source repo")
	}
	if !strings.Contains(err.Error(), "is not a git repository") {
		t.Errorf("error = %v, want 'not a git repository'", err)
	}
}

func TestSeedWorktree_Idempotent(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// First seed succeeds.
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Second seed without force is idempotent no-op.
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSeedWorktree_ForceReplaces(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// First seed succeeds.
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Second seed with --force replaces.
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", true, ""); err != nil {
		t.Fatal(err)
	}

	// Worktree still exists and is valid.
	if _, err := os.Stat(filepath.Join(homePath, ".git")); err != nil {
		t.Fatal("worktree removed after force seed")
	}
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s missing after force seed", CaptainCharterName)
	}
}

func TestSeedWorktree_RemoteMismatch(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/parent/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/different/repo.git")

	err := seedFromWorktreeTest("test", "", repo, parent, "", false, "")
	if err == nil {
		t.Fatal("expected error for mismatched remote")
	}
	if !strings.Contains(err.Error(), "does not match parent remote") {
		t.Errorf("error = %v, want remote mismatch", err)
	}
}

func TestSeedWorktree_ExplicitRef(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	// Create a second branch in the repo.
	if _, err := exec.Command("git", "-C", repo, "checkout", "-b", "feature-branch").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("git", "-C", repo, "commit", "--allow-empty", "-m", "feature").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	// Switch back to main for stable origin/HEAD.
	if _, err := exec.Command("git", "-C", repo, "checkout", "main").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, repo, parent, "", false, "feature-branch"); err != nil {
		t.Fatal(err)
	}

	// Verify on feature-branch (detached HEAD at feature-branch commit).
	branch, err := exec.Command("git", "-C", homePath, "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(branch)) == "main" {
		t.Error("worktree is on main, expected feature-branch")
	}
}

func TestSeedWorktree_RollbackOnFailure(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// Seed with a non-existent parent home for the charter path (will fail).
	// The worktree should be cleaned up.
	err := seedFromWorktreeTest(id, homePath, repo, "/nonexistent/parent", "", false, "")
	if err == nil {
		t.Fatal("expected error")
	}

	// Worktree should not exist.
	if _, err := os.Stat(homePath); !os.IsNotExist(err) {
		t.Error("worktree should have been rolled back on failure")
	}
}

func TestSeedWorktree_RollbackUnregisterOnFailure(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// SeedWorktree with a non-existent parent config dir to fail ConfigPush.
	// The worktree should be created, registered, then rolled back.
	err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, "")
	if err != nil {
		t.Fatal(err)
	}

	// Now corrupt the config-push by removing parent config dir.
	// A second force seed should succeed.
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", true, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSeedWorktree_ParentHomeCharter(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify default charter was written to .captain-charter.md (untracked).
	body, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "captain:test-captain.status") {
		t.Errorf("charter missing captain status path, got: %s", body)
	}
}

func TestIsManagedWorktree_ExistingPathNotWorktree(t *testing.T) {
	// Create a non-worktree directory at the target path.
	target := t.TempDir()

	managed, err := isManagedWorktree(target)
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Error("expected false for non-worktree path")
	}
}

func TestResolveDefaultBranch_UsesOriginHead(t *testing.T) {
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	// Rename default to main via -b main in init, confirm origin/HEAD exists.
	// initTestRepo uses -b main and adds origin remote, so origin/HEAD is set.
	branch, err := resolveDefaultBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("expected main, got %q", branch)
	}
}

func TestSeedWorktree_GitignoreContent(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	homePath := filepath.Join(parent, "captains", "test")
	if err := seedFromWorktreeTest("test", homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Read the .git worktree pointer to find info/exclude.
	gitPtrData, err := os.ReadFile(filepath.Join(homePath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	gitdirLine := strings.TrimSpace(string(gitPtrData))
	if !strings.HasPrefix(gitdirLine, "gitdir: ") {
		t.Fatalf(".git is not a gitdir pointer: %q", gitdirLine)
	}
	// Use the common dir (two levels up from worktree git dir).
	gitDir := strings.TrimPrefix(gitdirLine, "gitdir: ")
	commonDir := filepath.Dir(filepath.Dir(gitDir))
	excludeData, err := os.ReadFile(filepath.Join(commonDir, "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(excludeData)
	if !strings.Contains(content, "state/") {
		t.Error("info/exclude missing state/ entry")
	}
	if !strings.Contains(content, CaptainProvenanceName) {
		t.Errorf("info/exclude missing %s entry", CaptainProvenanceName)
	}
}

// --- Provenance tests ---
func TestProvenance_SeedAndValidate(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(tmp, 0755)

	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for missing marker")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error = %v", err)
	}

	if err := SeedProvenance(tmp, "test-id"); err != nil {
		t.Fatal(err)
	}

	id, err := ValidateProvenance(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if id != "test-id" {
		t.Errorf("id = %q, want %q", id, "test-id")
	}
}

func TestProvenance_InvalidFormat(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ProvenanceMarkerName), []byte("only-id\n"), 0644)
	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for malformed marker")
	}
}

func TestProvenance_WrongVersion(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ProvenanceMarkerName), []byte("old-v0\nsome-id\nsome/home\n"), 0644)
	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error = %v", err)
	}
}

// --- Validate / Migrate tests ---

func TestValidate_PassesForSeededHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	seedTest("test-sm", smHome, "# charter")

	err := Validate(smHome, tmp)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_RefusesFakeName(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "fake")
	seedTest("fake-sm", fakeHome, "# charter")

	err := Validate(fakeHome, tmp)
	if err == nil {
		t.Fatal("expected error for reserved name 'fake'")
	}
	if !strings.Contains(err.Error(), "reserved name") {
		t.Errorf("error = %v", err)
	}
}

func TestValidate_RefusesPrimaryName(t *testing.T) {
	tmp := t.TempDir()
	primaryHome := filepath.Join(tmp, "primary")
	seedTest("primary-sm", primaryHome, "# charter")

	err := Validate(primaryHome, tmp)
	if err == nil {
		t.Fatal("expected error for reserved name 'primary'")
	}
}

func TestValidate_RefusesSelfParent(t *testing.T) {
	tmp := t.TempDir()
	seedTest("test-sm", tmp, "# charter")

	err := Validate(tmp, tmp)
	if err == nil {
		t.Fatal("expected error for being parent home itself")
	}
	if !strings.Contains(err.Error(), "is the parent home itself") {
		t.Errorf("error = %v", err)
	}
}

func TestValidate_RefusesMissingDirs(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	SeedProvenance(smHome, "test-sm")

	err := Validate(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for missing AGENTS.md")
	}
}

func TestMigrate_WritesMarkerToSeededHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")

	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)

	if err := Migrate(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}

	id, err := ValidateProvenance(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if id != "test-sm" {
		t.Errorf("id = %q, want %q", id, "test-sm")
	}
}

func TestMigrate_RefusesReservedName(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "fake")
	os.MkdirAll(fakeHome, 0755)
	err := Migrate(fakeHome, "fake-sm")
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
	if !strings.Contains(err.Error(), "reserved name") {
		t.Errorf("error = %v", err)
	}
}

// --- Registry tests ---

func TestListCaptains_Empty(t *testing.T) {
	parent := t.TempDir()
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Errorf("expected empty list, got %d entries", len(mates))
	}
}

func TestListCaptains_WithRegistryFile(t *testing.T) {
	parent := t.TempDir()
	// Typed captain registry: ListCaptains reads data/captains.json. Legacy
	// captains.md parsing (meta blocks, comment skipping) now lives in
	// configmigration and is covered by the ParseRegistry tests below.
	if err := config.StoreCaptainRegistry(parent, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
		Captains: []config.CaptainRecord{
			{ID: "sm-alpha", Home: "/home/sm-alpha", Project: "project-a"},
			{ID: "sm-beta", Home: "/home/sm-beta", Project: "project-b"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 2 {
		t.Errorf("expected 2 captains, got %d", len(mates))
	}

	found := map[string]bool{}
	for _, m := range mates {
		found[m.ID] = true
		if m.ID == "sm-alpha" {
			if m.Home != "/home/sm-alpha" {
				t.Errorf("sm-alpha home = %q, want %q", m.Home, "/home/sm-alpha")
			}
			if m.Project != "project-a" {
				t.Errorf("sm-alpha project = %q, want %q", m.Project, "project-a")
			}
		}
	}
	// Scope and Added are legacy captains.md fields with no typed equivalent.
	if !found["sm-alpha"] {
		t.Error("sm-alpha not found in list")
	}
	if !found["sm-beta"] {
		t.Error("sm-beta not found in list")
	}
}

func TestListCaptains_SkipsCommentLines(t *testing.T) {
	parent := t.TempDir()
	// JSON registries have no comment lines; comment skipping lives in the
	// legacy captains.md parser (configmigration.parseRegistry, mirrored by
	// ParseRegistry). ListCaptains returns exactly the registered captains.
	if err := config.StoreCaptainRegistry(parent, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
		Captains: []config.CaptainRecord{
			{ID: "valid-sm", Home: "/home/valid-sm", Project: "test"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 {
		t.Errorf("expected 1 captain, got %d", len(mates))
	}
	if mates[0].ID != "valid-sm" {
		t.Errorf("expected valid-sm, got %q", mates[0].ID)
	}
}

func TestParseRegistry_FullEntry(t *testing.T) {
	tmp := t.TempDir()
	registryPath := filepath.Join(tmp, "captains.md")
	content := `# Captains
- monitor-z - # Monitoring captain (home: /home/monitor-z; scope: infra monitoring; projects: monitoring; added: 2026-07-18)
`
	os.WriteFile(registryPath, []byte(content), 0644)

	mates, err := ParseRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 {
		t.Fatalf("expected 1, got %d", len(mates))
	}
	if mates[0].ID != "monitor-z" {
		t.Errorf("id = %q", mates[0].ID)
	}
	if mates[0].Home != "/home/monitor-z" {
		t.Errorf("home = %q", mates[0].Home)
	}
	if mates[0].Scope != "infra monitoring" {
		t.Errorf("scope = %q", mates[0].Scope)
	}
	if mates[0].Project != "monitoring" {
		t.Errorf("project = %q", mates[0].Project)
	}
	if mates[0].Added != "2026-07-18" {
		t.Errorf("added = %q", mates[0].Added)
	}
}

func TestParseRegistry_MissingFile(t *testing.T) {
	mates, err := ParseRegistry("/nonexistent/captains.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Errorf("expected 0, got %d", len(mates))
	}
}

// --- ConfigPush tests ---

func TestConfigPush_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)

	err := configPush(parent, smHome)
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestConfigPush_Basic(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	// Typed parent config: the inheritable surface is the resolved project
	// config (soldier harness + dispatch profiles) published as a snapshot.
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: "pi",
			DispatchProfiles: []config.DispatchProfile{
				{Name: "default", Harness: "pi", Model: "claude-sonnet"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatalf("resolved snapshot was not published: %v", err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}
	profiles := snapshot.Config().DispatchProfiles
	if len(profiles) != 1 || profiles[0].Harness != "pi" || profiles[0].Model != "claude-sonnet" {
		t.Errorf("snapshot dispatchProfiles = %+v, want the inherited profile", profiles)
	}
}

// TestConfigPush_MirrorDeletions proves that when the parent removes an
// inherited setting, the captain's next push no longer carries it. The typed
// snapshot is regenerated from current parent state, so a removed harness is
// not retained across pushes (the typed mirror-deletion equivalent).
func TestConfigPush_MirrorDeletions(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	storeBase := func(harness string) error {
		overlay := config.ProjectOverlay{}
		if harness != "" {
			overlay.SoldierHarness = harness
		}
		return config.StoreFleetBase(parent, config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        overlay,
		})
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storeBase("pi"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	// First push inherits the harness.
	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}

	// Parent removes the harness — the next push must drop it.
	if err := storeBase(""); err != nil {
		t.Fatal(err)
	}
	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err = config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().SoldierHarness != "" {
		t.Errorf("snapshot soldierHarness = %q after removal, want empty (mirror deletion)", snapshot.Config().SoldierHarness)
	}
}

func TestConfigPush_OnlyInheritableDeleted(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	// Captain-local (non-inherited) config must survive config push.
	os.WriteFile(filepath.Join(smHome, "config", "model"), []byte("some-model\n"), 0644)

	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	// The inherited harness is published; the captain-local model file is
	// owned by the captain and must not be deleted.
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}
	if _, err := os.Stat(filepath.Join(smHome, "config", "model")); os.IsNotExist(err) {
		t.Error("non-inheritable model should NOT have been deleted")
	}
}

// TestConfigPush_CaptainShared proves that captain-level shared settings from
// the parent (the captain profile) reach the captain's resolved snapshot.
// The legacy general-shared.md file copy was removed with the typed-config
// hard cut; the typed equivalent is the fleet base captainProfile.
func TestConfigPush_CaptainShared(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
		CaptainProfile: config.CaptainProfile{
			Harness: "pi",
			Model:   "claude-sonnet",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatalf("resolved snapshot was not published: %v", err)
	}
	prof := snapshot.Config().CaptainProfile
	if prof.Harness != "pi" || prof.Model != "claude-sonnet" {
		t.Errorf("snapshot captainProfile = %+v, want the shared profile", prof)
	}
}

// TestConfigPush_CaptainSharedMirrorDeletion proves that when the parent
// removes the shared captain profile, the next push no longer carries it.
func TestConfigPush_CaptainSharedMirrorDeletion(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	storeBase := func(profile config.CaptainProfile) error {
		return config.StoreFleetBase(parent, config.FleetBaseDocument{
			SchemaVersion:  config.FleetBaseSchemaVersion,
			Config:         config.ProjectOverlay{SoldierHarness: "pi"},
			CaptainProfile: profile,
		})
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storeBase(config.CaptainProfile{Harness: "pi", Model: "claude-sonnet"}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().CaptainProfile.Harness == "" {
		t.Error("snapshot should carry the shared captain profile")
	}

	// Parent removes the shared profile — the next push drops it.
	if err := storeBase(config.CaptainProfile{}); err != nil {
		t.Fatal(err)
	}
	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err = config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().CaptainProfile.Harness != "" || snapshot.Config().CaptainProfile.Model != "" {
		t.Errorf("snapshot captainProfile = %+v after removal, want empty (mirror deletion)", snapshot.Config().CaptainProfile)
	}
}

func TestConfigPush_RejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	outside := t.TempDir()
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(smHome, "config")); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := configPush(parent, smHome)
	if err == nil || !strings.Contains(err.Error(), "escapes captain container") {
		t.Fatalf("ConfigPush error = %v, want symlink-escape refusal", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "soldier-harness")); !os.IsNotExist(err) {
		t.Fatalf("outside destination was mutated: %v", err)
	}
}

// TestConfigPush_IdempotentPreservesMtime proves that pushing unchanged
// config does not re-propagate: the generation tracking stays put when the
// resolved config digest is unchanged (the typed idempotency equivalent of
// not rewriting an unchanged inherited file).
func TestConfigPush_IdempotentPreservesMtime(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(filepath.Join(smHome, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	first, err := configPushWithResult(parent, smHome)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("first push should be a change (generation 0 → 1)")
	}
	time.Sleep(20 * time.Millisecond)
	second, err := configPushWithResult(parent, smHome)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Errorf("unchanged push advanced generation: %d -> %d", first.Generation, second.Generation)
	}
	if second.Generation != first.Generation {
		t.Errorf("generation = %d, want %d (unchanged config must not re-propagate)", second.Generation, first.Generation)
	}
}

func TestConfigPush_ProjectsRegistry(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	SeedProvenance(smHome, "test-sm")

	// Typed project registry on the General home: configPush resolves and
	// publishes the captain's project as the inherited config snapshot.
	repo := t.TempDir()
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "munsu", Path: repo, Mode: "no-mistakes"},
			{Name: "toy", Path: "/tmp/toy", Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "captain", "munsu"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	// The captain's published snapshot resolves its project from the parent
	// project registry.
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatalf("resolved snapshot was not published: %v", err)
	}
	if snapshot.Config().Project != "munsu" {
		t.Errorf("snapshot project = %q, want %q", snapshot.Config().Project, "munsu")
	}
	if snapshot.Config().ProjectPath != repo {
		t.Errorf("snapshot project path = %q, want %q", snapshot.Config().ProjectPath, repo)
	}

	// Project registry reading still works on the General home.
	projects, err := List(parent)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	path, err := ResolveRepoPath(parent, "munsu")
	if err != nil {
		t.Fatalf("ResolveRepoPath: %v", err)
	}
	if path != repo {
		t.Errorf("ResolveRepoPath = %q, want %q", path, repo)
	}
}

// TestConfigPush_RemovedProjectFailsClosed is the migrated mirror-deletion
// test: when the parent's project registry no longer carries the captain's
// project (parent "has no projects"), configPush must fail closed instead of
// silently publishing a stale resolution.
func TestConfigPush_RemovedProjectFailsClosed(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	SeedProvenance(smHome, "test-sm")

	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{SchemaVersion: config.ProjectRegistrySchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "captain", "stale"); err != nil {
		t.Fatal(err)
	}

	err := configPush(parent, smHome)
	if err == nil || !strings.Contains(err.Error(), "unknown project") {
		t.Fatalf("configPush error = %v, want fail-closed on removed project", err)
	}
	if _, statErr := os.Stat(filepath.Join(smHome, config.PublishedSnapshotPath)); !os.IsNotExist(statErr) {
		t.Error("no published snapshot should exist when the parent project registry has no matching project")
	}
}

func TestSeedWithParent_InheritsProjectsAndConfig(t *testing.T) {
	parent := t.TempDir()

	sm := filepath.Join(parent, "captains", "ops")
	if err := seedWithParentTest("ops", sm, parent, ""); err != nil {
		t.Fatal(err)
	}

	// Seed installs typed documents on the parent and propagates the
	// resolved project config (inherited harness + project binding) into
	// the captain's published snapshot.
	snapshot, err := config.LoadPublishedSnapshot(sm)
	if err != nil {
		t.Fatalf("seed did not publish inherited snapshot: %v", err)
	}
	if snapshot.Config().Project != "ops" {
		t.Errorf("snapshot project = %q, want %q", snapshot.Config().Project, "ops")
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot did not inherit soldier-harness: got %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}
}

// TestSeedWithParent_WritesParentHomeConfig verifies that seed writes
// config/parent-home in the captain home.
func TestSeedWithParent_WritesParentHomeConfig(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.MkdirAll(filepath.Join(parent, "data"), 0755)

	sm := filepath.Join(parent, "captains", "ops")
	if err := seedWithParentTest("ops", sm, parent, ""); err != nil {
		t.Fatal(err)
	}

	dat, err := os.ReadFile(filepath.Join(sm, "config", "parent-home"))
	if err != nil {
		t.Fatalf("config/parent-home should exist: %v", err)
	}
	if strings.TrimSpace(string(dat)) != parent {
		t.Errorf("parent-home = %q, want %q", strings.TrimSpace(string(dat)), parent)
	}
}

// TestConfigPush_RefreshesParentHome verifies that ConfigPush refreshes
// config/parent-home in the captain home.
func TestConfigPush_RefreshesParentHome(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.MkdirAll(filepath.Join(parent, "data"), 0755)

	captainHome := t.TempDir()
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)

	// Seed captain with provenance
	if err := SeedProvenance(captainHome, "test-captain"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# Test Captain\n"), 0644)

	// Write a stale parent-home to verify ConfigPush refreshes it
	staleHome := filepath.Join(parent, "stale")
	os.MkdirAll(filepath.Dir(staleHome), 0755)
	if err := config.Set(captainHome, "parent-home", staleHome); err != nil {
		t.Fatal(err)
	}

	// Run ConfigPush — should overwrite stale parent-home with current parent
	if err := configPush(parent, captainHome); err != nil {
		t.Fatal(err)
	}

	dat, err := os.ReadFile(filepath.Join(captainHome, "config", "parent-home"))
	if err != nil {
		t.Fatalf("config/parent-home should exist: %v", err)
	}
	if strings.TrimSpace(string(dat)) != parent {
		t.Errorf("parent-home = %q after refresh, want %q", strings.TrimSpace(string(dat)), parent)
	}
}

// TestUpdate_StateOnlyCaptainConfigPush verifies that Update on a state-only
// captain (no git worktree) still runs ConfigPush to write config/parent-home.
// This is requirement 1: existing state-only Captain update paths must atomically
// write config/parent-home from the authoritative registered General home.
func TestUpdate_StateOnlyCaptainConfigPush(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.MkdirAll(filepath.Join(parent, "data"), 0755)

	captainHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)

	// Seed captain with provenance but NO parent-home config (simulating
	// an already-provisioned state-only captain from before parent-home was introduced).
	if err := SeedProvenance(captainHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# Test Captain\n"), 0644)

	// Verify parent-home does NOT exist yet
	if _, err := os.Stat(filepath.Join(captainHome, "config", "parent-home")); err == nil {
		t.Fatal("test setup: parent-home should NOT exist before Update")
	}

	// Run Update — should detect state-only home and still run ConfigPush
	res := Update(captainHome, parent)
	if res.Outcome != StateOnlySkipped {
		t.Fatalf("Update outcome = %s, want %s", res.Outcome, StateOnlySkipped)
	}

	// Verify parent-home was written by ConfigPush
	dat, err := os.ReadFile(filepath.Join(captainHome, "config", "parent-home"))
	if err != nil {
		t.Fatalf("config/parent-home should exist after Update on state-only captain: %v", err)
	}
	if strings.TrimSpace(string(dat)) != parent {
		t.Errorf("parent-home = %q after Update, want %q", strings.TrimSpace(string(dat)), parent)
	}
}

// TestRecoverTransaction_ConfigPushStep verifies that the RecoverTransaction
// includes a config-push step that writes config/parent-home.
func TestRecoverTransaction_ConfigPushStep(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.MkdirAll(filepath.Join(parent, "data"), 0755)

	captainHome := seedCaptainForTest(t, parent, "state-only-sm")

	// Verify parent-home does NOT exist yet
	if _, err := os.Stat(filepath.Join(captainHome, "config", "parent-home")); err == nil {
		t.Fatal("test setup: parent-home should NOT exist before recover")
	}

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}}}
	sm := Info{ID: "state-only-sm", Home: captainHome}
	res := tx.Recover(parent, sm)

	// Find the config-push step
	foundConfigPush := false
	for _, step := range res.Steps {
		if step.Name == "config-push" {
			foundConfigPush = true
			if step.State != StepOk {
				t.Errorf("config-push step state = %s, want ok: %s", step.State, step.Detail)
			}
			break
		}
	}
	if !foundConfigPush {
		t.Fatal("config-push step not found in RecoverTransaction steps")
	}

	// Verify parent-home was written
	dat, err := os.ReadFile(filepath.Join(captainHome, "config", "parent-home"))
	if err != nil {
		t.Fatalf("config/parent-home should exist after RecoverTransaction: %v", err)
	}
	if strings.TrimSpace(string(dat)) != parent {
		t.Errorf("parent-home = %q after recover, want %q", strings.TrimSpace(string(dat)), parent)
	}
}

// TestEnsureWatcher_NoLongerRequiresParentHome verifies that EnsureWatcher
// no longer requires config/parent-home. The watcher is recovery-only and
// does not need parent-home for terminal receipt routing.
func TestGetInheritableListCaptains_Default(t *testing.T) {
	os.Unsetenv("MUNSU_INHERITABLE_CONFIG")
	list := getInheritableList()
	expected := []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}

func TestGetInheritableListCaptains_EnvOverride(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "soldier-harness:model:custom-config")
	list := getInheritableList()
	expected := []string{"soldier-harness", "model", "custom-config"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}

func TestGetInheritableListCaptains_EmptyEnv(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "")
	list := getInheritableList()
	expected := []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}

// --- ShQuote / buildLaunchScript tests ---

func TestShQuote_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"path/with/slashes", "'path/with/slashes'"},
		{"dollar$ign", "'dollar$ign'"},
		{"back`tick", "'back`tick'"},
		{"double\"quote", "'double\"quote'"},
	}
	for _, tt := range tests {
		got := shQuote(tt.input)
		if got != tt.want {
			t.Errorf("shQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShQuote_EmbeddedSingleQuote(t *testing.T) {
	got := shQuote("it's")
	// Expected: 'it'\''s'
	want := "'it'\\''s'"
	if got != want {
		t.Errorf("shQuote with single quote = %q, want %q", got, want)
	}
}

func TestShQuote_NewlinesAndSpecials(t *testing.T) {
	input := "line1\nline2"
	got := shQuote(input)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("shQuote should wrap in single quotes, got: %q", got)
	}
	// The newline inside the single quotes should be preserved in the quoted form.
	if len(got) < len(input)+2 {
		t.Errorf("shQuote too short: %q", got)
	}
}

func TestShQuote_ShellExecutableCharacters(t *testing.T) {
	// Characters like $() should be inside single quotes, not executed.
	input := "$(echo pwned)"
	got := shQuote(input)
	if got != "'$(echo pwned)'" {
		t.Errorf("shQuote should escape $() by wrapping in single quotes, got: %q", got)
	}
}

func TestBuildLaunchScript(t *testing.T) {
	tmp := t.TempDir()
	binPath := "/usr/local/bin/pi"
	args := []string{"--model", "gpt-5", "# charter"}
	cwd := tmp

	cmd, err := buildLaunchScript(binPath, args, cwd, tmp)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}
	scriptPath := filepath.Join(cwd, ".captain-launch.sh")
	if cmd != "bash "+shQuote(scriptPath) {
		t.Fatalf("command = %q, want bash-wrapped script path", cmd)
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading launch script: %v", err)
	}
	script := string(body)
	if !strings.HasPrefix(script, "#!/usr/bin/env bash\n") {
		end := 40
		if len(script) < end {
			end = len(script)
		}
		t.Errorf("script should start with bash shebang, got: %q", script[:end])
	}
	if !strings.Contains(script, "export MUNSU_HOME="+shQuote(cwd)) {
		t.Errorf("script should export MUNSU_HOME, got: %s", script)
	}
	if !strings.Contains(script, "export MUNSU_ROLE=captain") {
		t.Errorf("script should export MUNSU_ROLE, got: %s", script)
	}
	if !strings.Contains(script, "exec ") {
		t.Errorf("script should contain 'exec ', got: %s", script)
	}
	if !strings.Contains(script, binPath) {
		t.Errorf("script should contain bin path %q, got: %s", binPath, script)
	}
	for _, arg := range args {
		if !strings.Contains(script, shQuote(arg)) {
			t.Errorf("script should contain quoted arg %q, got: %s", arg, script)
		}
	}
}

func TestBuildLaunchScript_SafeQuoting(t *testing.T) {
	tmp := t.TempDir()
	binPath := "/usr/local/bin/pi"
	args := []string{"# charter with $HOME and `backticks` and $(whoami)"}
	cwd := filepath.Join(tmp, "sm test")
	os.MkdirAll(cwd, 0755)

	cmd, err := buildLaunchScript(binPath, args, cwd, tmp)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(cwd, ".captain-launch.sh"))
	if err != nil {
		t.Fatalf("reading launch script: %v", err)
	}
	script := string(body)
	if !strings.Contains(script, shQuote(args[0])) {
		t.Errorf("dangerous arg not properly quoted in: %s", script)
	}
	if !strings.HasPrefix(cmd, "bash ") {
		t.Errorf("command should be bash-wrapped, got %q", cmd)
	}
}

func TestBuildLaunchScript_ShellExecution(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "sm test")
	os.MkdirAll(smHome, 0755)

	recorder := filepath.Join(tmp, "recorded.txt")

	// Create a small shell script that writes its cwd and argv to a file.
	testBin := filepath.Join(tmp, "test-recorder")
	// The script: write cwd, then write argv count, then write each arg.
	binContent := "#!/bin/sh\n"
	binContent += "pwd > '" + recorder + "'\n"
	binContent += "echo \"argv $#\" >> '" + recorder + "'\n"
	binContent += "for a in \"$@\"; do echo \"  [$a]\" >> '" + recorder + "'; done\n"
	if err := os.WriteFile(testBin, []byte(binContent), 0755); err != nil {
		t.Fatal(err)
	}

	// Build a launch script with special characters.
	args := []string{"# charter with $HOME and `backticks` and $(whoami)"}
	scriptCmd, err := buildLaunchScript(testBin, args, smHome, smHome)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}

	// Execute via /bin/sh -c (the returned command is already bash <script>).
	cmd := exec.Command("/bin/sh", "-c", scriptCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell execution failed: %v\noutput: %s", err, string(out))
	}

	// Read recorded output.
	data, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatalf("reading recorded output: %v", err)
	}
	recorded := string(data)

	// Verify cwd is the general home.
	if !strings.Contains(recorded, smHome) {
		t.Errorf("recorded output should contain smHome %q, got: %s", smHome, recorded)
	}
	// Verify special characters are preserved as literal strings in argv.
	if !strings.Contains(recorded, "$HOME") {
		t.Errorf("recorded output should contain literal $HOME, got: %s", recorded)
	}
	if !strings.Contains(recorded, "$(whoami)") {
		t.Errorf("recorded output should contain literal $(whoami), got: %s", recorded)
	}
	if !strings.Contains(recorded, "`backticks`") {
		t.Errorf("recorded output should contain literal backticks, got: %s", recorded)
	}
}

// --- sha256Content tests ---

func TestSha256Content_Deterministic(t *testing.T) {
	data := []byte("test content")
	h1 := captainSHA256Content(data)
	h2 := captainSHA256Content(data)
	if h1 != h2 {
		t.Errorf("sha256Content should be deterministic, got %q vs %q", h1, h2)
	}
}

func TestSha256Content_Different(t *testing.T) {
	h1 := captainSHA256Content([]byte("content A"))
	h2 := captainSHA256Content([]byte("content B"))
	if h1 == h2 {
		t.Errorf("sha256Content should differ for different content")
	}
}

func TestSha256Content_Empty(t *testing.T) {
	h := captainSHA256Content([]byte(""))
	if h == "" {
		t.Errorf("sha256Content should return non-empty for empty input")
	}
}

// --- Launch tests using capability fakes ---
func TestLaunch_RefusesUnmarkedHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	err := Launch(smHome, tmp, testLaunchEndpoint{})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestLaunch_RefusesCaptainRole(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	seedTest("test-sm", smHome, "# charter")
	t.Setenv("MUNSU_ROLE", "captain")
	err := Launch(smHome, tmp, testLaunchEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "cannot launch other captains") {
		t.Fatalf("Launch() error = %v, want nested-captain refusal", err)
	}
}

func TestLaunch_RefusesFromCaptainParentHome(t *testing.T) {
	parent := t.TempDir()
	if err := SeedProvenance(parent, "parent-sm"); err != nil {
		t.Fatal(err)
	}
	smHome := filepath.Join(t.TempDir(), "child-sm")
	seedTest("child-sm", smHome, "# charter")
	t.Setenv("MUNSU_ROLE", "")
	err := Launch(smHome, parent, testLaunchEndpoint{})
	if err == nil || !strings.Contains(err.Error(), "cannot launch another captain") {
		t.Fatalf("Launch() error = %v, want parent-captain refusal", err)
	}
}

func TestHandoff_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(sm, 0755)

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestHandoff_RequiresTasksAxi(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(sm, 0755)
	SeedProvenance(sm, "test-sm")

	origPath := captainLookPath
	captainLookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	defer func() { captainLookPath = origPath }()

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for missing tasks-axi")
	}
	if !strings.Contains(err.Error(), "tasks-axi not found") {
		t.Errorf("error should mention missing tasks-axi, got: %v", err)
	}
}

func TestHandoff_RefusesSelfParent(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(parent, 0755)
	SeedProvenance(parent, "parent-sm")

	err := Handoff(parent, parent, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for same home")
	}
	if !strings.Contains(err.Error(), "destination is parent home itself") {
		t.Errorf("error should mention parent home, got: %v", err)
	}
}

func TestHandoffPassesQueuedKeysToTasksAxiMv(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TASK-1", "TASK-2"} {
		seedHandoffTaskV2Default(t, parent, id)
	}

	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n## Queued\n- [ ] TASK-1\n- [ ] TASK-2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()

	argsPath := filepath.Join(parent, "args.txt")
	fakeTasksAxi := filepath.Join(parent, "fake-tasks-axi")
	fakeScript := "#!/bin/sh\nif [ \"$1\" = show ]; then echo 'state: queued'; exit 0; fi\nprintf '%s\\n' \"$@\" > " + shQuote(argsPath) + "\n"
	if err := os.WriteFile(fakeTasksAxi, []byte(fakeScript), 0755); err != nil {
		t.Fatal(err)
	}
	captainLookPath = func(name string) (string, error) { return fakeTasksAxi, nil }
	isTasksAxiBackend = func(string) bool { return true }

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"mv", "TASK-1", "TASK-2",
	}
	if len(args) < len(want) {
		t.Fatalf("args = %#v, want prefix %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	// Durable handoff operates on staged backlog copies, not home paths.
	if !strings.HasSuffix(args[4], "destination-backlog-post") {
		t.Errorf("args[4] = %q, want suffix destination-backlog-post", args[4])
	}
	if !strings.HasSuffix(args[6], "source-backlog-post") {
		t.Errorf("args[6] = %q, want suffix source-backlog-post", args[6])
	}
}

func TestHandoff_RefusesManualBackend(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(sm, 0755)
	SeedProvenance(sm, "test-sm")

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()

	// Override isTasksAxiBackend to return false (manual backend).
	isTasksAxiBackend = func(string) bool { return false }

	captainLookPath = func(name string) (string, error) {
		return "/usr/bin/tasks-axi", nil
	}

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for manual backend")
	}
	if !strings.Contains(err.Error(), "backlog backend is not set to tasks-axi") {
		t.Errorf("error should mention backend mismatch, got: %v", err)
	}
}

// --- Retire tests ---

func TestRetire_RefusesUnmarkedHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)

	err := Retire(smHome, tmp, false, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestRetire_RefusesUnmarkedWithRemoveHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "sentinel"), []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Retire(smHome, tmp, true, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected ownership refusal for unmarked destructive retire")
	}
	if _, err := os.Stat(filepath.Join(smHome, "sentinel")); err != nil {
		t.Fatalf("unowned home was mutated: %v", err)
	}
}

func TestRetire_RemoveHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	if err := Retire(smHome, parent, true, false, &testRetireEndpoint{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(smHome); !os.IsNotExist(err) {
		t.Error("captain home should have been removed")
	}
}

func TestRetire_KeepHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	if err := Retire(smHome, parent, false, false, &testRetireEndpoint{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(smHome); os.IsNotExist(err) {
		t.Error("captain home should have been retained")
	}
}

func TestRetire_NonexistentHomeRefused(t *testing.T) {
	parent := t.TempDir()
	if err := Retire("/nonexistent/sm", parent, true, false, &testRetireEndpoint{}); err == nil {
		t.Fatal("expected nonexistent unowned home refusal")
	}
}

// --- Retire meta validation tests ---

func TestRetire_RefusesWrongKindMeta(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write bad meta.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "captain:test-sm.meta"),
		[]byte("kind=not-captain\nsm_id=test-sm\nhome="+smHome+"\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected error for wrong meta kind")
	}
	if !strings.Contains(err.Error(), "kind=") {
		t.Errorf("error should mention kind mismatch, got: %v", err)
	}
}

func TestRetire_RefusesMismatchedID(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write meta with different sm_id.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "captain:test-sm.meta"),
		[]byte("kind=captain\nsm_id=wrong-id\nhome="+smHome+"\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected error for mismatched sm_id")
	}
	if !strings.Contains(err.Error(), "sm_id") {
		t.Errorf("error should mention sm_id mismatch, got: %v", err)
	}
}

func TestRetire_RefusesMismatchedHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write meta with different home.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "captain:test-sm.meta"),
		[]byte("kind=captain\nsm_id=test-sm\nhome=/some/other/path\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected error for mismatched home")
	}
	if !strings.Contains(err.Error(), "home=") {
		t.Errorf("error should mention home mismatch, got: %v", err)
	}
}

// --- acquireExclusiveLock tests ---

func TestAcquireExclusiveLock(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release function")
	}

	// Lock file should exist.
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file was not created")
	}

	// Release.
	release()

	// Lock file should be removed.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file was not removed after release")
	}
}

func TestAcquireExclusiveLock_ConcurrentRefusal(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Captain acquire with LOCK_NB should fail immediately.
	// Use channel + timeout to prove non-blocking behavior.
	done := make(chan struct{})
	var captainErr error
	go func() {
		_, captainErr = acquireExclusiveLock(lockPath)
		close(done)
	}()

	select {
	case <-done:
		if captainErr == nil {
			t.Fatal("captain concurrent lock should have failed with LOCK_NB")
		}
		if !strings.Contains(captainErr.Error(), "held by another process") {
			t.Logf("captain lock error (expected): %v", captainErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("captain lock acquisition blocked for 5s — LOCK_NB not working")
	}

	release1()

	// Acquire again after release (generation-safe).
	release2, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

// --- Nudge marker tests ---

func TestNudgeMarkerPath(t *testing.T) {
	parent := t.TempDir()
	path := nudgeMarkerPath(parent, "test-sm")
	want := filepath.Join(parent, "state", ".captain-nudge-pending", "test-sm.pending")
	if path != want {
		t.Errorf("nudgeMarkerPath = %q, want %q", path, want)
	}
}

func TestWriteAndReadNudgeMarker(t *testing.T) {
	parent := t.TempDir()
	smID := "test-sm"
	smHome := "/home/test-sm"
	instructions := "# charter content"
	message := "reread"

	err := writeNudgeMarker(parent, smID, smHome, "abc123", instructions, message)
	if err != nil {
		t.Fatalf("writeNudgeMarker error: %v", err)
	}

	marker, err := readNudgeMarker(parent, smID)
	if err != nil {
		t.Fatalf("readNudgeMarker error: %v", err)
	}
	if marker == nil {
		t.Fatal("nudge marker not found")
	}
	if marker["id"] != smID {
		t.Errorf("marker id = %q, want %q", marker["id"], smID)
	}
	if marker["home"] != smHome {
		t.Errorf("marker home = %q, want %q", marker["home"], smHome)
	}
	if marker["instructions"] != instructions {
		t.Errorf("marker instructions = %q, want %q", marker["instructions"], instructions)
	}
	if marker["message"] != message {
		t.Errorf("marker message = %q, want %q", marker["message"], message)
	}
}

func TestRemoveNudgeMarker(t *testing.T) {
	parent := t.TempDir()
	smID := "test-sm"

	writeNudgeMarker(parent, smID, "/home/test-sm", "abc", "# charter", "reread")
	if _, err := readNudgeMarker(parent, smID); err != nil || true {
		// Marker exists.
		marker, _ := readNudgeMarker(parent, smID)
		if marker == nil {
			t.Fatal("expected marker to exist after write")
		}
	}

	removeNudgeMarker(parent, smID)
	marker, _ := readNudgeMarker(parent, smID)
	if marker != nil {
		t.Error("marker should have been removed")
	}
}

func TestReadNudgeMarker_Nonexistent(t *testing.T) {
	parent := t.TempDir()
	marker, err := readNudgeMarker(parent, "nonexistent-sm")
	if err != nil {
		t.Fatalf("readNudgeMarker error: %v", err)
	}
	if marker != nil {
		t.Errorf("expected nil for nonexistent marker, got %v", marker)
	}
}

// --- safeFF tests (real git repos) ---

type safeFFFixture struct {
	parent  string
	captain string
	before  string
	after   string
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

func newSafeFFFixture(t *testing.T) safeFFFixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	parent := filepath.Join(root, "parent")
	captain := filepath.Join(root, "captain")
	for _, dst := range []string{parent, captain} {
		if out, err := exec.Command("git", "clone", remote, dst).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		gitTestRun(t, dst, "config", "user.name", "Munsu Test")
		gitTestRun(t, dst, "config", "user.email", "munsu@example.invalid")
	}
	gitTestRun(t, parent, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("state/ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "add", ".gitignore", "AGENTS.md")
	gitTestRun(t, parent, "commit", "-m", "initial")
	before := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "-u", "origin", "main")
	gitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "checkout", "-B", "main", before)
	gitTestRun(t, captain, "remote", "set-head", "origin", "main")
	gitTestRun(t, parent, "remote", "set-head", "origin", "main")

	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "commit", "-am", "advance instructions")
	after := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "origin", "main")
	// Seed the already-local object without changing the general checkout.
	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "reset", "--hard", before)
	return safeFFFixture{parent: parent, captain: captain, before: before, after: after}
}

func TestSafeFF_OffBranchRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.captain, "checkout", "-b", "feature")
	if _, _, _, err := safeFF(f.captain, f.parent); err == nil || !strings.Contains(err.Error(), "expected \"main\"") {
		t.Fatalf("safeFF error = %v, want off-default-branch refusal", err)
	}
}

func TestSafeFF_MissingOriginHEADRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.parent, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	if _, _, _, err := safeFF(f.captain, f.parent); err == nil || !strings.Contains(err.Error(), "origin/HEAD") {
		t.Fatalf("safeFF error = %v, want missing origin/HEAD refusal", err)
	}
}

func TestAcquireExclusiveLock_OldReleasePreservesReplacement(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")
	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lockPath, lockPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(strings.Repeat("a", 64)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("old generation release removed replacement lock: %v", err)
	}
}

func TestSafeFF_TrackedChangesRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.WriteFile(filepath.Join(f.captain, "AGENTS.md"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := safeFF(f.captain, f.parent); err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("safeFF error = %v, want tracked-change refusal", err)
	}
}

func TestSafeFF_UnignoredUntrackedRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.WriteFile(filepath.Join(f.captain, "rogue.txt"), []byte("rogue\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := safeFF(f.captain, f.parent); err == nil || !strings.Contains(err.Error(), "unignored untracked") {
		t.Fatalf("safeFF error = %v, want unignored-file refusal", err)
	}
}

func TestSafeFF_GitignoredArtifactAllowed(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.MkdirAll(filepath.Join(f.captain, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.captain, "state", "ignored"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, after, _, err := safeFF(f.captain, f.parent)
	if err != nil {
		t.Fatalf("safeFF: %v", err)
	}
	if before != f.before || after != f.after {
		t.Fatalf("safeFF = (%s, %s), want (%s, %s)", before, after, f.before, f.after)
	}
}

func TestSafeFF_ParentFeatureCheckoutStillTargetsDefaultBranch(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.parent, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(f.parent, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, f.parent, "add", "feature.txt")
	gitTestRun(t, f.parent, "commit", "-m", "feature only")
	_, after, _, err := safeFF(f.captain, f.parent)
	if err != nil {
		t.Fatalf("safeFF: %v", err)
	}
	if after != f.after {
		t.Fatalf("after = %s, want default-branch commit %s", after, f.after)
	}
}

// --- acquireExclusiveLock token tests ---

func TestAcquireExclusiveLock_TokenGeneration(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}

	// Lock file should exist with hex token content.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.TrimSpace(string(data))
	if len(content) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("expected 64 hex chars, got %d: %q", len(content), content)
	}

	// Release should clean up.
	release()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after release")
	}
}

func TestAcquireExclusiveLock_NoRemoveOnFailure(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Create a file with lock-like content to simulate the file still existing.
	// Write a marker so we can detect if it's removed.
	os.WriteFile(lockPath, []byte("other-content\n"), 0644)

	// Captain acquire should fail (LOCK_NB) but NOT remove the file.
	_, err = acquireExclusiveLock(lockPath)
	if err == nil {
		t.Fatal("expected captain acquire to fail")
	}

	// The file should still exist with its original content (not removed).
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal("lock file was removed — bug: os.Remove on LOCK_NB failure")
	}
	if string(data) != "other-content\n" {
		t.Errorf("lock file content changed: %q", string(data))
	}

	release1()
}

// --- Converge tests ---

func TestConverge_EmptyRegistry(t *testing.T) {
	parent := t.TempDir()
	_, err := Converge(parent, nil, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: nil, Mailbox: nil})
	if err != nil {
		t.Fatalf("Converge(nil) error: %v", err)
	}
	_, err = Converge(parent, []Info{}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: nil, Mailbox: nil})
	if err != nil {
		t.Fatalf("Converge(empty) error: %v", err)
	}
}

func TestConverge_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()

	_, err := Converge(parent, []Info{
		{ID: "test-sm", Home: "/nonexistent"},
	}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: &captainNotificationTransport{acknowledged: true}, Mailbox: &captainTestMailboxSender{}})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "provenance validation failed") {
		t.Errorf("error = %v", err)
	}
}

func TestConverge_ValidMarkersWithConfigPush(t *testing.T) {
	parent := t.TempDir()
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("# Parent charter\n"), 0644)

	// Typed parent config binds both captains to projects so converge's
	// inheritance push publishes a resolved snapshot per captain.
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}

	// Create two captains with provenance markers.
	sm1 := filepath.Join(parent, "captains", "sm-alpha")
	os.MkdirAll(filepath.Join(sm1, "state"), 0755)
	os.MkdirAll(filepath.Join(sm1, "config"), 0755)
	os.MkdirAll(filepath.Join(sm1, "data"), 0755)
	os.WriteFile(filepath.Join(sm1, "AGENTS.md"), []byte("# Alpha\n"), 0644)
	SeedProvenance(sm1, "sm-alpha")

	sm2 := filepath.Join(parent, "captains", "sm-beta")
	os.MkdirAll(filepath.Join(sm2, "state"), 0755)
	os.MkdirAll(filepath.Join(sm2, "config"), 0755)
	os.MkdirAll(filepath.Join(sm2, "data"), 0755)
	os.WriteFile(filepath.Join(sm2, "AGENTS.md"), []byte("# Beta\n"), 0644)
	SeedProvenance(sm2, "sm-beta")

	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "sm-alpha", Path: sm1, Mode: "no-mistakes"},
			{Name: "sm-beta", Path: sm2, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreCaptainRegistry(parent, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
		Captains: []config.CaptainRecord{
			{ID: "sm-alpha", Home: sm1, Project: "sm-alpha"},
			{ID: "sm-beta", Home: sm2, Project: "sm-beta"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Run converge.
	_, err := Converge(parent, []Info{
		{ID: "sm-alpha", Home: sm1},
		{ID: "sm-beta", Home: sm2},
	}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: &captainNotificationTransport{acknowledged: true}, Mailbox: &captainTestMailboxSender{}})

	// State-only homes skip safeFF gracefully; converge should succeed.
	if err != nil {
		t.Fatalf("converge should succeed for state-only homes: %v", err)
	}

	// Config push should have published a resolved snapshot for both.
	snap1, err := config.LoadPublishedSnapshot(sm1)
	if err != nil {
		t.Errorf("sm-alpha resolved snapshot not published: %v", err)
	} else if snap1.Config().SoldierHarness != "pi" {
		t.Errorf("sm-alpha snapshot soldierHarness = %q, want %q", snap1.Config().SoldierHarness, "pi")
	}

	snap2, err := config.LoadPublishedSnapshot(sm2)
	if err != nil {
		t.Errorf("sm-beta resolved snapshot not published: %v", err)
	} else if snap2.Config().SoldierHarness != "pi" {
		t.Errorf("sm-beta snapshot soldierHarness = %q, want %q", snap2.Config().SoldierHarness, "pi")
	}
}

func TestConverge_RefusesRegistryIDMismatch(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)
	// Seed with id "actual-id"
	SeedProvenance(smHome, "actual-id")

	// But registry says "wrong-id".
	_, err := Converge(parent, []Info{
		{ID: "wrong-id", Home: smHome},
	}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: &captainNotificationTransport{acknowledged: true}, Mailbox: &captainTestMailboxSender{}})
	if err == nil {
		t.Fatal("expected error for ID mismatch")
	}
	if !strings.Contains(err.Error(), "does not match registry id") {
		t.Errorf("error should mention ID mismatch, got: %v", err)
	}
}

// --- taskIDForCaptain tests ---

func TestTaskIDForCaptain(t *testing.T) {
	id := taskIDForCaptain("test-sm")
	if id != "captain:test-sm" {
		t.Errorf("taskIDForCaptain = %q, want %q", id, "captain:test-sm")
	}
}

func TestRegister_Idempotent(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "api")
	os.MkdirAll(sm, 0755)
	if err := SeedProvenance(sm, "api"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "api", sm, "scope", "proj"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "api", sm, "scope", "proj"); err != nil {
		t.Fatal(err)
	}
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 || mates[0].ID != "api" {
		t.Fatalf("mates=%+v", mates)
	}
}

func TestSeedWithParent_Registers(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "ops")
	if err := seedWithParentTest("ops", sm, parent, ""); err != nil {
		t.Fatal(err)
	}
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 || mates[0].ID != "ops" {
		t.Fatalf("mates=%+v", mates)
	}
}

func TestBuildLaunchArgs_PiLoadsOnlyCanonicalIntegration(t *testing.T) {
	parent := t.TempDir()
	sm := t.TempDir()
	extDir := filepath.Join(sm, ".pi", "extensions")
	os.MkdirAll(extDir, 0755)
	os.WriteFile(filepath.Join(sm, "AGENTS.md"), []byte("# charter\n"), 0644)
	for _, name := range []string{
		"munsu-pi-integration.ts",
		"munsu-captain-turnend-guard.ts",
		"munsu-captain-pi-watch.ts",
		"fm-primary-turnend-guard.ts",
		"fm-primary-pi-watch.ts",
	} {
		os.WriteFile(filepath.Join(extDir, name), []byte("//x\n"), 0644)
	}

	_, _, err := buildLaunchArgs(sm, "pi", parent)
	if err == nil || !strings.Contains(err.Error(), "compatibility Pi integration alias") {
		t.Fatalf("buildLaunchArgs() error = %v, want compatibility alias refusal", err)
	}
}

func TestUnregister_RemovesEntry(t *testing.T) {
	parent := t.TempDir()
	smA := filepath.Join(parent, "captains", "alpha")
	smB := filepath.Join(parent, "captains", "beta")
	os.MkdirAll(smA, 0755)
	os.MkdirAll(smB, 0755)
	if err := SeedProvenance(smA, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(smB, "beta"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "alpha", smA, "scope-a", "proj-a"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "beta", smB, "scope-b", "proj-b"); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(parent, "alpha"); err != nil {
		t.Fatal(err)
	}
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 || mates[0].ID != "beta" {
		t.Fatalf("mates=%+v", mates)
	}
}

func TestUnregister_MissingIDIdempotent(t *testing.T) {
	parent := t.TempDir()
	if err := Unregister(parent, "ghost"); err != nil {
		t.Fatal(err)
	}
}

func TestInFlightSoldierIDs(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.WriteFile(filepath.Join(home, "state", "TASK-1.meta"), []byte("kind=ship\nwindow=w1\n"), 0644)
	os.WriteFile(filepath.Join(home, "state", "TASK-2.meta"), []byte("kind=scout\nwindow=w2\n"), 0644)
	os.WriteFile(filepath.Join(home, "state", "TASK-3.meta"), []byte("kind=captain\nwindow=w3\n"), 0644)
	os.WriteFile(filepath.Join(home, "state", "TASK-4.meta"), []byte("kind=other\n"), 0644)

	ids, err := inFlightSoldierIDs(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["TASK-1"] || !got["TASK-2"] {
		t.Fatalf("ids=%v", ids)
	}
}

func TestRetire_UnregistersFromRegistry(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "scope", "proj"); err != nil {
		t.Fatal(err)
	}

	if err := Retire(smHome, parent, false, false, &testRetireEndpoint{}); err != nil {
		t.Fatal(err)
	}

	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Fatalf("expected empty registry after retire, got %+v", mates)
	}
}

func TestRetire_RefusesInFlightWithoutForce(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "scope", "proj"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(smHome, "state", "soldier-1.meta"), []byte("kind=ship\nwindow=w\n"), 0644)

	err := Retire(smHome, parent, false, false, &testRetireEndpoint{})
	if err == nil {
		t.Fatal("expected refuse for in-flight soldiers")
	}
	if !strings.Contains(err.Error(), "in-flight") {
		t.Fatalf("error=%v", err)
	}
	mates, listErr := ListCaptains(parent)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(mates) != 1 {
		t.Fatalf("registry should be unchanged on refuse, got %+v", mates)
	}
}

func TestRetire_ForceAllowsInFlight(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "scope", "proj"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(smHome, "state", "soldier-1.meta"), []byte("kind=ship\nwindow=w\n"), 0644)

	if err := Retire(smHome, parent, false, true, &testRetireEndpoint{}); err != nil {
		t.Fatal(err)
	}
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Fatalf("expected empty registry after force retire, got %+v", mates)
	}
}

func TestEnsureCaptainPiExtensions_InstallsBeforeLaunchArgs(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "ext-sm")
	if err := seedWithParentTest("ext-sm", sm, parent, "# charter\n"); err != nil {
		t.Fatal(err)
	}

	// Seed path must leave at least munsu-captain-* or munsu-pi-integration when pi/munsu available.
	extDir := filepath.Join(sm, ".pi", "extensions")
	var found []string
	for _, name := range []string{harness.CanonicalPiIntegrationName} {
		if _, err := os.Stat(filepath.Join(extDir, name)); err == nil {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		// Soft-skip host: still prove Ensure is idempotent and Launch wiring is safe.
		if err := ensureCaptainIntegration(sm, fakeIntegrationPort{}); err != nil {
			t.Fatalf("EnsureCaptainPiExtensions: %v", err)
		}
		// Manually plant the canonical extension to assert buildLaunchArgs -e path still works.
		os.MkdirAll(extDir, 0755)
		os.WriteFile(filepath.Join(extDir, "munsu-pi-integration.ts"), []byte("// planted\n"), 0644)
	} else if len(found) != 1 || found[0] != "munsu-pi-integration.ts" {
		t.Fatalf("seed installed non-canonical extensions: %v", found)
	}

	// ConfigPush must re-ensure without error.
	if err := configPush(parent, sm); err != nil {
		t.Fatalf("ConfigPush: %v", err)
	}

	name, args, err := buildLaunchArgs(sm, harness.Pi, parent)
	if err != nil {
		t.Fatal(err)
	}
	if name != "pi" {
		t.Fatalf("name=%q", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-e") {
		t.Fatalf("launch args missing -e after ensure: %v", args)
	}
}

func TestEnsureCaptainPiExtensions_RefusesUnmarked(t *testing.T) {
	err := ensureCaptainIntegration(t.TempDir(), fakeIntegrationPort{})
	if err == nil || !strings.Contains(err.Error(), "unmarked home") {
		t.Fatalf("EnsureCaptainPiExtensions() error = %v, want unmarked refusal", err)
	}
}

// --- Recover tests ---

// writeCaptainMeta writes a captain task meta for test purposes.

// seedCaptainForTest creates a captain home with provenance marker and optional AGENTS.md.

func TestRecover_EmptyRegistry(t *testing.T) {
	res, err := Recover(t.TempDir(), nil, RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}})
	if err != nil {
		t.Fatalf("Recover(nil) error: %v", err)
	}
	if res.Relaunched != 0 || len(res.Entries) != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

func TestRecover_SeededCaptainNotLaunched(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "sm-seeded")
	// No task meta written → checkAliveViaBackend returns (false,nil) but launched=false.

	res, err := Recover(parent, []Info{{ID: "sm-seeded", Home: smHome}}, RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}})
	if err != nil {
		t.Fatalf("Recover error: %v", err)
	}
	if res.Seeded != 1 || res.Relaunched != 0 {
		t.Errorf("counts = %+v, want seeded=1", res)
	}
	if len(res.Entries) != 1 || res.Entries[0].Outcome != RecoverSeeded {
		t.Errorf("entry = %+v, want RecoverSeeded", res.Entries)
	}
}

func TestRecover_PiIntegrationStatusControlsRelaunch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		wantLaunch int
		wantFailed int
	}{
		{name: "installed", state: "installed", wantLaunch: 1},
		{name: "absent", state: "absent", wantFailed: 1},
		{name: "drifted", state: "drifted", wantFailed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			if err := config.Set(parent, "captain-harness", "pi"); err != nil {
				t.Fatal(err)
			}
			home := seedCaptainForTest(t, parent, "pi-captain")
			writeCanonicalPiIntegration(t, home)
			writeCaptainMeta(t, parent, "pi-captain", home, "dead-window")
			launch := &countingLaunchEndpoint{}
			result, err := Recover(parent, []Info{{ID: "pi-captain", Home: home}}, RecoverCapabilities{
				Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: tc.state, Message: "test status"}},
				Launch:      launch,
				Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if launch.calls != tc.wantLaunch || result.Failed != tc.wantFailed {
				t.Fatalf("launch calls=%d failed=%d, want %d/%d; result=%+v", launch.calls, result.Failed, tc.wantLaunch, tc.wantFailed, result)
			}
		})
	}
}

func TestRecover_NonPiHarnessDoesNotRequirePiIntegration(t *testing.T) {
	parent := t.TempDir()
	if err := config.Set(parent, "captain-harness", harness.Claude); err != nil {
		t.Fatal(err)
	}
	home := seedCaptainForTest(t, parent, "claude-captain")
	writeCaptainMeta(t, parent, "claude-captain", home, "dead-window")
	integration := &countingStatusIntegrationPort{}
	result, err := Recover(parent, []Info{{ID: "claude-captain", Home: home}}, RecoverCapabilities{Integration: integration, Launch: &countingLaunchEndpoint{}, Probe: &testProbeEndpoint{result: CaptainProbeResult{}}})
	if err != nil {
		t.Fatal(err)
	}
	if integration.calls != 0 {
		t.Fatalf("Pi integration status calls = %d, want 0 for non-Pi harness", integration.calls)
	}
	if result.Failed != 1 || !strings.Contains(result.Entries[0].Error, "verified captain launch contract") {
		t.Fatalf("result=%+v, want unchanged non-Pi launch contract result", result)
	}
}

func TestRecover_BadProvenanceFailsEntry(t *testing.T) {
	parent := t.TempDir()
	// Home exists but has no provenance home.
	smHome := filepath.Join(parent, "captains", "sm-bad")
	os.MkdirAll(smHome, 0755)

	res, err := Recover(parent, []Info{{ID: "sm-bad", Home: smHome}}, RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}})
	if err != nil {
		t.Fatalf("Recover error: %v", err)
	}
	if res.Failed != 1 {
		t.Errorf("counts = %+v, want failed=1", res)
	}
}

func TestRecoverResult_String(t *testing.T) {
	res := &RecoverResult{Entries: []RecoverEntry{
		{ID: "a", Outcome: RecoverAlive},
		{ID: "b", Outcome: RecoverFailed, Error: "boom"},
	}}
	s := res.String()
	if !strings.Contains(s, "a: alive") || !strings.Contains(s, "b: FAILED: boom") {
		t.Errorf("String() = %q", s)
	}

	empty := (&RecoverResult{}).String()
	if empty != "no captains registered" {
		t.Errorf("empty String() = %q", empty)
	}
}

// --- ProbeLiveness tests ---

func TestProbeLiveness_ReportsSeededWithoutMeta(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "sm-x")

	probes := ProbeLiveness(parent, []Info{{ID: "sm-x", Home: smHome}}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
	if len(probes) != 1 || probes[0].Status != "seeded" {
		t.Errorf("probes = %+v, want one seeded", probes)
	}
}

func TestProbeLiveness_EmptyAndUnknown(t *testing.T) {
	if ProbeLiveness(t.TempDir(), nil, nil) != nil {
		t.Error("expected nil for empty registry")
	}
	probes := ProbeLiveness(t.TempDir(), []Info{{ID: "x", Home: ""}}, nil)
	if len(probes) != 1 || probes[0].Status != "unknown" {
		t.Errorf("probes = %+v, want unknown for empty home", probes)
	}
}

func TestBuildLaunchArgs_CaptainHarnessMultiToken(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)
	writeCanonicalPiIntegration(t, smHome)

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "captain-harness"), []byte("pi cliproxyapi/grok-4.5 low\n"), 0644)
	// legacy model must not win over multi-token
	os.WriteFile(filepath.Join(configDir, "model"), []byte("should-not-use\n"), 0644)

	_, args, err := buildLaunchArgs(smHome, harness.Pi, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs: %v", err)
	}
	// Expect --model cliproxyapi/grok-4.5 and --thinking low (pi effort flag)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "cliproxyapi/grok-4.5") {
		t.Errorf("args missing multi-token model: %v", args)
	}
	if strings.Contains(joined, "should-not-use") {
		t.Errorf("legacy model should not apply: %v", args)
	}
	// pi EffortFlag is --thinking
	foundThinking := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--thinking" && args[i+1] == "low" {
			foundThinking = true
		}
		if args[i] == "--model" && args[i+1] != "cliproxyapi/grok-4.5" {
			t.Errorf("unexpected model arg: %v", args)
		}
	}
	if !foundThinking {
		t.Errorf("expected --thinking low in args: %v", args)
	}
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

func TestSeedFromWorktree_CreatesDetachedWorktree(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Home directory exists.
	if _, err := os.Stat(homePath); os.IsNotExist(err) {
		t.Fatal("home directory was not created")
	}

	// .git is a file (worktree marker), not a directory.
	gitFi, err := os.Stat(filepath.Join(homePath, ".git"))
	if err != nil {
		t.Fatal(".git marker missing:", err)
	}
	if gitFi.IsDir() {
		t.Fatal(".git is a directory, expected worktree file marker")
	}

	// HEAD is detached at origin/main.
	head := gitTestRun(t, homePath, "rev-parse", "HEAD")
	if head == "" {
		t.Fatal("empty HEAD")
	}
	expected := gitTestRun(t, project, "rev-parse", "origin/main")
	if head != expected {
		t.Errorf("HEAD = %s, want %s (origin/main)", head, expected)
	}

	// .captain-provenance exists.
	provPath := filepath.Join(homePath, CaptainProvenanceName)
	if _, err := os.Stat(provPath); err != nil {
		t.Fatal("provenance file missing:", err)
	}
	provData, err := os.ReadFile(provPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provData), "source-repo:") {
		t.Errorf("provenance missing source-repo, got: %s", provData)
	}
	if !strings.Contains(string(provData), "commit:") {
		t.Errorf("provenance missing commit, got: %s", provData)
	}
	if !strings.Contains(string(provData), "created:") {
		t.Errorf("provenance missing created, got: %s", provData)
	}

	// Exclude file exists in worktree git info/exclude and covers operational dirs.
	gitPtrData, err := os.ReadFile(filepath.Join(homePath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	gitdirLine := strings.TrimSpace(string(gitPtrData))
	if !strings.HasPrefix(gitdirLine, "gitdir: ") {
		t.Fatalf(".git is not a gitdir pointer: %q", gitdirLine)
	}
	gitDir := strings.TrimPrefix(gitdirLine, "gitdir: ")
	commonDir := filepath.Dir(filepath.Dir(gitDir))
	excludePath := filepath.Join(commonDir, "info", "exclude")
	excludeData, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range worktreeExcludeContent {
		if !strings.Contains(string(excludeData), entry) {
			t.Errorf("info/exclude missing entry %q, got: %s", entry, excludeData)
		}
	}

	// Standard captain home dirs exist.
	for _, dir := range []string{"state", "data", "config", "projects"} {
		p := filepath.Join(homePath, dir)
		if fi, err := os.Stat(p); err != nil {
			t.Errorf("subdirectory %s not created: %v", dir, err)
		} else if !fi.IsDir() {
			t.Errorf("%s exists but is not a directory", p)
		}
	}

	// .captain-charter.md exists (untracked charter file).
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s missing: %v", CaptainCharterName, err)
	}

	// .munsu-captain-home exists.
	if _, err := os.Stat(filepath.Join(homePath, ProvenanceMarkerName)); err != nil {
		t.Errorf("provenance marker missing: %v", err)
	}

	// Registered in parent.
	registered, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range registered {
		if r.ID == "test-captain" {
			found = true
			break
		}
	}
	if !found {
		t.Error("captain not registered in parent home")
	}
}

func TestSeedFromWorktree_Idempotent(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	// First call: creates the worktree.
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Second call: must be a no-op.
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal("second seed should be no-op:", err)
	}
}

func TestSeedFromWorktree_RefusesStateOnlyHome(t *testing.T) {
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "existing-sm")

	// Create a state-only captain home first.
	if err := seedWithParentTest("existing-sm", homePath, parent, ""); err != nil {
		t.Fatal(err)
	}

	project := newWorktreeFixture(t)

	// Worktree seed on an existing state-only home must fail.
	if err := seedFromWorktreeTest("existing-sm", homePath, project, parent, "", false, ""); err == nil {
		t.Fatal("expected error for state-only home, got nil")
	}
}

// TestSeedFromWorktree_ManagedWorktreeClean verifies that Seed followed by
// ConfigPush on a managed worktree leaves no unexpected untracked files.
// Regression: the Captain Pi-extension installer must not create .pi/extensions/
// in managed worktree fixtures under hermetic TestMain.
func TestSeedFromWorktree_ManagedWorktreeClean(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	// Pre-populate parent config so ConfigPush has something to push.
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// Seed the managed worktree.
	if err := seedFromWorktreeTest(id, homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Run ConfigPush — must not create .pi/ artifacts.
	if err := configPush(parent, homePath); err != nil {
		t.Fatal(err)
	}

	// Verify the managed worktree has no unexpected tracked/untracked files.
	// Allowed untracked files: state/, data/, config/, projects/, .captain-charter.md,
	// .munsu-captain-home, .captain-launch.sh are excluded via info/exclude.
	// Anything else (e.g., .pi/) must not appear.
	out, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	status := strings.TrimSpace(string(out))
	if status != "" {
		t.Errorf("managed worktree has unexpected git status:\n%s", status)
	}

	// Also verify .pi/ does not exist.
	if _, err := os.Stat(filepath.Join(homePath, ".pi")); err == nil {
		t.Error(".pi/ directory should not exist in managed worktree after hermetic Seed/ConfigPush")
	}
}

func TestIsManagedWorktree(t *testing.T) {
	t.Run("returns false for non-existent path", func(t *testing.T) {
		managed, err := isManagedWorktree(filepath.Join(t.TempDir(), "nonexistent"))
		if err != nil {
			t.Fatal(err)
		}
		if managed {
			t.Error("expected false for non-existent path")
		}
	})

	t.Run("returns false for bare directory", func(t *testing.T) {
		managed, err := isManagedWorktree(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if managed {
			t.Error("expected false for bare dir")
		}
	})

	t.Run("returns false for regular git clone", func(t *testing.T) {
		project := newWorktreeFixture(t)
		managed, err := isManagedWorktree(project)
		if err != nil {
			t.Fatal(err)
		}
		if managed {
			t.Error("expected false for regular clone")
		}
	})

	t.Run("returns true for seeded worktree captain home", func(t *testing.T) {
		project := newWorktreeFixture(t)
		parent := t.TempDir()
		homePath := filepath.Join(parent, "captains", "test-captain")
		if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
			t.Fatal(err)
		}
		managed, err := isManagedWorktree(homePath)
		if err != nil {
			t.Fatal(err)
		}
		if !managed {
			t.Error("expected true for seeded worktree")
		}
	})
}

func TestDefaultBranch_WithOriginHEAD(t *testing.T) {
	project := newWorktreeFixture(t)
	branch, err := resolveDefaultBranch(project)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}
}

func TestDefaultBranch_FallbackToMain(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	exec.Command("git", "init", "--bare", remote).Run()
	project := filepath.Join(root, "project")
	exec.Command("git", "clone", remote, project).Run()
	gitTestRun(t, project, "config", "user.name", "Munsu Test")
	gitTestRun(t, project, "config", "user.email", "munsu@example.invalid")
	gitTestRun(t, project, "checkout", "-b", "main")
	os.WriteFile(filepath.Join(project, "file"), []byte("x"), 0644)
	gitTestRun(t, project, "add", "file")
	gitTestRun(t, project, "commit", "-m", "init")
	gitTestRun(t, project, "push", "-u", "origin", "main")

	// Remove origin/HEAD symbolic ref to test fallback.
	gitTestRun(t, project, "remote", "set-head", "origin", "--delete")

	branch, err := resolveDefaultBranch(project)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}
}

// --- MigrateToWorktree tests ---

// stateOnlyHomeFixture creates a state-only captain home in parent and returns its path.
func stateOnlyHomeFixture(t *testing.T, parent, id string) string {
	t.Helper()
	smHome := filepath.Join(parent, "captains", id)
	if err := seedTest(id, smHome, "# charter for "+id); err != nil {
		t.Fatalf("seedTest(%s): %v", id, err)
	}
	return smHome
}

func TestMigrateToWorktree_SuccessPath(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	id := "test-captain"
	smHome := stateOnlyHomeFixture(t, parent, id)

	// Write some operational state.
	os.MkdirAll(filepath.Join(smHome, "state", "sub"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", "sub", "data.txt"), []byte("runtime\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "config", "custom.cfg"), []byte("setting=1\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "data", "notes.md"), []byte("# notes\n"), 0644)

	// Migrate to managed worktree.
	if err := migrateToWorktreeTest(smHome, project, id, parent); err != nil {
		t.Fatal(err)
	}

	// 1. Home is now a managed worktree.
	managed, err := isManagedWorktree(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if !managed {
		t.Error("home should be a managed worktree after migration")
	}

	// 2. .munsu-captain-home marker exists.
	if _, err := os.Stat(filepath.Join(smHome, ProvenanceMarkerName)); err != nil {
		t.Errorf("provenance marker missing: %v", err)
	}

	// 3. .captain-provenance exists.
	if _, err := os.Stat(filepath.Join(smHome, CaptainProvenanceName)); err != nil {
		t.Errorf("captain provenance missing: %v", err)
	}

	// 4. Charter preserved as untracked .captain-charter.md (not dirtying tracked AGENTS.md).
	if _, err := os.Stat(filepath.Join(smHome, CaptainCharterName)); err != nil {
		t.Errorf("%s missing: %v", CaptainCharterName, err)
	}

	// 5. Worktree admin path points at final home (no temp path).
	wtListCaptains := gitTestRun(t, project, "worktree", "list", "--porcelain")
	if !strings.Contains(wtListCaptains, smHome) {
		t.Errorf("git worktree list missing final home %s; got:\n%s", smHome, wtListCaptains)
	}
	if strings.Contains(wtListCaptains, ".worktree-") {
		t.Errorf("git worktree list still has temp path; got:\n%s", wtListCaptains)
	}

	// 6. Operational dirs preserved with content.
	data, err := os.ReadFile(filepath.Join(smHome, "state", "sub", "data.txt"))
	if err != nil {
		t.Errorf("state/sub/data.txt not preserved: %v", err)
	} else if string(data) != "runtime\n" {
		t.Errorf("state/sub/data.txt content = %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(smHome, "config", "custom.cfg"))
	if err != nil {
		t.Errorf("config/custom.cfg not preserved: %v", err)
	} else if string(data) != "setting=1\n" {
		t.Errorf("config/custom.cfg content = %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(smHome, "data", "notes.md"))
	if err != nil {
		t.Errorf("data/notes.md not preserved: %v", err)
	} else if string(data) != "# notes\n" {
		t.Errorf("data/notes.md content = %q", string(data))
	}

	// 7. Backup directory exists.
	backupGlob, _ := filepath.Glob(smHome + ".backup-*")
	if len(backupGlob) == 0 {
		t.Error("backup directory not found")
	}

	// 8. Registered in parent.
	found := false
	mates, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mates {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("captain %s not registered in parent", id)
	}

	// 9. Git is detached (worktree state).
	head := gitTestRun(t, smHome, "rev-parse", "HEAD")
	if head == "" {
		t.Error("empty HEAD in worktree")
	}
	gitFi, err := os.Stat(filepath.Join(smHome, ".git"))
	if err != nil {
		t.Fatal(".git marker missing:", err)
	}
	if gitFi.IsDir() {
		t.Error(".git is a directory, expected worktree file marker")
	}
}

func TestMigrateToWorktree_RefusesManagedWorktree(t *testing.T) {
	parent := t.TempDir()
	project := newWorktreeFixture(t)

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)
	if err := seedFromWorktreeTest(id, homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Attempt migration on already-managed worktree.
	err := migrateToWorktreeTest(homePath, project, id, parent)
	if err == nil {
		t.Fatal("expected error for already-managed worktree")
	}
	if !strings.Contains(err.Error(), "already a managed worktree") {
		t.Errorf("error = %v, want 'already a managed worktree'", err)
	}
}

func TestMigrateToWorktree_RefusesNonStateOnly(t *testing.T) {
	parent := t.TempDir()
	project := newWorktreeFixture(t)

	// Create a bare directory with no captain structure.
	bareDir := filepath.Join(t.TempDir(), "bare")
	os.MkdirAll(bareDir, 0755)

	err := migrateToWorktreeTest(bareDir, project, "test", parent)
	if err == nil {
		t.Fatal("expected error for non-state-only path")
	}
	if !strings.Contains(err.Error(), "not a state-only home") {
		t.Errorf("error = %v, want refusal of non-state-only", err)
	}
}

func TestMigrateToWorktree_RollbackOnWorktreeFailure(t *testing.T) {
	parent := t.TempDir()
	id := "test-captain"
	smHome := stateOnlyHomeFixture(t, parent, id)

	// Use a non-existent repo path to cause worktree creation to fail.
	nonExistentRepo := filepath.Join(t.TempDir(), "nonexistent")

	err := migrateToWorktreeTest(smHome, nonExistentRepo, id, parent)
	if err == nil {
		t.Fatal("expected error for non-existent repo")
	}

	// Original home should still be intact.
	if _, stErr := os.Stat(filepath.Join(smHome, "AGENTS.md")); stErr != nil {
		t.Errorf("original home was damaged: AGENTS.md missing: %v", stErr)
	}
	if !isStateOnlyHome(smHome) {
		t.Error("home should still be a state-only home after failed migration")
	}
}

func TestMigrateToWorktree_RemoteMismatchRefused(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/parent/repo.git")
	id := "test-captain"
	smHome := stateOnlyHomeFixture(t, parent, id)

	// Repo with different remote.
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/different/repo.git")

	err := migrateToWorktreeTest(smHome, repo, id, parent)
	if err == nil {
		t.Fatal("expected error for mismatched remote")
	}
	if !strings.Contains(err.Error(), "does not match parent remote") {
		t.Errorf("error = %v, want remote mismatch", err)
	}
}

// ---------------------------------------------------------------------------
// Regression tests for captain-migration-postconditions
// ---------------------------------------------------------------------------

// TestSeedWorktree_GitClean proves that after SeedFromWorktree, the managed
// worktree is git-clean — no tracked modifications, and the only untracked
// files are properly gitignored via info/exclude.
func TestSeedWorktree_GitClean(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify worktree is git-clean.

	// Verify worktree is git-clean.
	status := gitTestRun(t, homePath, "status", "--porcelain")
	if status != "" {
		t.Errorf("worktree is not git-clean, status:\n%s", status)
	}

	// Verify .captain-charter.md exists but is gitignored (ls-files shows nothing).
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s not created: %v", CaptainCharterName, err)
	}
	charterInIndex := gitTestRun(t, homePath, "ls-files", CaptainCharterName)
	if charterInIndex != "" {
		t.Errorf("%s should not be tracked, but found in index: %q", CaptainCharterName, charterInIndex)
	}

	// Verify source tracked files (e.g. README.md) are unchanged from origin.
	originRef := gitTestRun(t, project, "rev-parse", "origin/main")
	headRef := gitTestRun(t, homePath, "rev-parse", "HEAD")
	if headRef != originRef {
		t.Errorf("HEAD = %q, want origin/main = %q — worktree on wrong ref", headRef, originRef)
	}
}

// TestRepairWorktreeAdminPath proves that after renaming a worktree directory,
// repairWorktreeAdminPath updates git's worktree admin so that "git worktree list"
// shows the final (renamed) path, not the old temp path.

// TestRepairWorktreeAdminPath proves that after renaming a worktree directory,
// repairWorktreeAdminPath updates git's worktree admin so that "git worktree list"
// shows the final (renamed) path, not the old temp path.
func TestRepairWorktreeAdminPath(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	// Create worktree at a temp path, then rename to final captain home.
	tempPath := filepath.Join(parent, "captains", "temp-worktree")
	if err := seedFromWorktreeTest("test-captain", tempPath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify git worktree list shows temp path before rename.
	worktreeOut := gitTestRun(t, project, "worktree", "list", "--porcelain")
	if !strings.Contains(worktreeOut, tempPath) {
		t.Fatalf("expected worktree list to contain %q before rename, got:\n%s", tempPath, worktreeOut)
	}

	// Atomic rename: move temp path to final captain home.
	finalPath := filepath.Join(parent, "captains", "final-captain")
	if err := os.Rename(tempPath, finalPath); err != nil {
		t.Fatal(err)
	}

	// After rename, git worktree list still shows old temp path — stale.
	staleOut := gitTestRun(t, project, "worktree", "list", "--porcelain")
	if strings.Contains(staleOut, finalPath) {
		t.Skip("rename already updated git worktree list — nothing to repair")
	}

	// Now repair the admin path.
	if err := repairWorktreeAdminPath(finalPath, ""); err != nil {
		t.Fatalf("repairWorktreeAdminPath failed: %v", err)
	}

	// Verify git worktree list now shows final path.
	repairedOut := gitTestRun(t, project, "worktree", "list", "--porcelain")
	if !strings.Contains(repairedOut, finalPath) {
		t.Errorf("expected worktree list to contain %q after repair, got:\n%s", finalPath, repairedOut)
	}

	// Verify the worktree is still functional.
	gitTestRun(t, finalPath, "rev-parse", "HEAD")
	if _, err := os.Stat(filepath.Join(finalPath, CaptainCharterName)); err != nil {
		t.Errorf("%s missing after rename and repair: %v", CaptainCharterName, err)
	}
}

// TestUpdate_ManagedWorktreeUsesProvenanceRepo proves that Update() works for
// managed worktree captains even when parentHome (the General state home) is NOT
// a git repo. This covers Defect 3: captain update must use the source-repo from
// .captain-provenance, not treat the General state home as a git repo.

// TestUpdate_ManagedWorktreeUsesProvenanceRepo proves that Update() works for
// managed worktree captains even when parentHome (the General state home) is NOT
// a git repo. This covers Defect 3: captain update must use the source-repo from
// .captain-provenance, not treat the General state home as a git repo.
func TestUpdate_ManagedWorktreeUsesProvenanceRepo(t *testing.T) {
	// Create a source git repo (the project repo).
	project := newWorktreeFixture(t)
	initialCommit := gitTestRun(t, project, "rev-parse", "HEAD")

	// Create a fake General state home (NOT a git repo).
	parent := t.TempDir()
	// Write the parent provenance marker so ValidateProvenance passes.
	if err := os.MkdirAll(filepath.Join(parent, "captains"), 0755); err != nil {
		t.Fatal(err)
	}

	// Seed a managed worktree captain (creates .captain-provenance with source-repo).
	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify .captain-provenance exists and points to the project repo.
	sourceRepo := readCaptainProvenance(homePath)
	if sourceRepo == "" {
		t.Fatal(".captain-provenance missing or has no source-repo")
	}
	if sourceRepo != project {
		t.Logf("source-repo = %q, project fixture = %q", sourceRepo, project)
	}

	// Advance the project repo (source) with a new commit.
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# Updated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, project, "add", "README.md")
	gitTestRun(t, project, "commit", "-m", "update")
	gitTestRun(t, project, "push", "origin", "main")
	newCommit := gitTestRun(t, project, "rev-parse", "HEAD")
	gitTestRun(t, homePath, "fetch", "origin", "main")
	// Reset captain back to initial commit (as if it hasn't been updated yet).
	gitTestRun(t, homePath, "reset", "--hard", initialCommit)

	// Verify captain is behind.
	currentHead := gitTestRun(t, homePath, "rev-parse", "HEAD")
	if currentHead == newCommit {
		t.Skip("initial and new commit are same — cannot test fast-forward")
	}

	// Run Update: parentHome is NOT a git repo, but safeFF should use
	// provenance source-repo to resolve the upstream.
	resp := Update(homePath, parent)
	if resp.Outcome != FastForwarded {
		t.Fatalf("Update outcome = %q, want %q (before=%s, after=%s, err=%v)",
			resp.Outcome, FastForwarded, safeStr(resp.Before), safeStr(resp.After), resp.Err)
	}
	if resp.Before == resp.After {
		t.Fatal("expected Before != After on fast-forward")
	}

	// Verify captain is now at the new commit.
	updatedHead := gitTestRun(t, homePath, "rev-parse", "HEAD")
	if updatedHead != newCommit {
		t.Errorf("captain HEAD = %q, want %q", updatedHead, newCommit)
	}
}

// TestUpdate_ManagedWorktreeAlreadyCurrent proves that Update() returns
// AlreadyCurrent for a managed worktree that is already at the latest commit,
// using provenance source-repo resolution (Defect 3 regression).

// TestUpdate_ManagedWorktreeAlreadyCurrent proves that Update() returns
// AlreadyCurrent for a managed worktree that is already at the latest commit,
// using provenance source-repo resolution (Defect 3 regression).
func TestUpdate_ManagedWorktreeAlreadyCurrent(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Captain is already at origin/main. Update should return AlreadyCurrent.
	resp := Update(homePath, parent)
	if resp.Outcome != AlreadyCurrent {
		t.Fatalf("Update outcome = %q, want %q (err=%v)", resp.Outcome, AlreadyCurrent, resp.Err)
	}
}

// TestUpdate_ManagedWorktreeNoParentGit proves that Update() correctly resolves
// the source-repo from .captain-provenance when parentHome is not a git repo
// and returns the appropriate outcome.

// TestUpdate_ManagedWorktreeNoParentGit proves that Update() correctly resolves
// the source-repo from .captain-provenance when parentHome is not a git repo
// and returns the appropriate outcome.
func TestUpdate_ManagedWorktreeNoParentGit(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Verify parent is NOT a git repo.
	if _, err := os.Stat(filepath.Join(parent, ".git")); !os.IsNotExist(err) {
		t.Skip("parent unexpectedly has .git — cannot test no-parent-git scenario")
	}

	// Update must not fail with a git error involving parent. It should resolve
	// from provenance and succeed (AlreadyCurrent since nothing advanced).
	resp := Update(homePath, parent)
	if resp.Outcome != AlreadyCurrent {
		t.Fatalf("Update outcome = %q, want %q (err=%v)", resp.Outcome, AlreadyCurrent, resp.Err)
	}
}

// TestMigrateRollbackSafety proves that when SeedFromWorktree fails partway
// through, the worktree is cleaned up and no partial artifacts remain.

// TestMigrateRollbackSafety proves that when SeedFromWorktree fails partway
// through, the worktree is cleaned up and no partial artifacts remain.
func TestMigrateRollbackSafety(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	id := "test-captain"
	homePath := filepath.Join(parent, "captains", id)

	// Seed with a non-existent parent home for the charter path (will fail).
	err := seedFromWorktreeTest(id, homePath, repo, "/nonexistent/parent", "", false, "")
	if err == nil {
		t.Fatal("expected error for non-existent parent charter path")
	}

	// The worktree should not exist (rolled back on failure).
	if _, err := os.Stat(homePath); !os.IsNotExist(err) {
		t.Error("worktree should have been rolled back on failure, but still exists")
	}

	// Verify the source repo has no stale worktree registration (best-effort).
	worktreeOut, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(worktreeOut), homePath) {
		t.Errorf("stale worktree entry remains in source repo after rollback:\n%s", worktreeOut)
	}
}

// =============================================================================
// Config inheritance parity tests
// =============================================================================

// TestConfigPush_InheritsEnvOverriddenKeys proves that MUNSU_INHERITABLE_CONFIG
// no longer filters config push: after the typed-config hard cut the resolved
// config is authoritative and the full inherited surface is always propagated.
// The env list helper itself (configmigration.getInheritableList) is covered by
// the TestGetInheritableListCaptains_* tests below.
func TestConfigPush_InheritsEnvOverriddenKeys(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "custom-key:another-key:extra-key")

	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	// The full inherited surface is propagated even though the env list
	// names unrelated keys — nothing is filtered out.
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatalf("resolved snapshot was not published: %v", err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q (env override must not filter)", snapshot.Config().SoldierHarness, "pi")
	}
}

// TestConfigPush_InheritsEnvMirrorDeletions proves that mirror deletion still
// applies with MUNSU_INHERITABLE_CONFIG set, and that captain-local config is
// never touched.
func TestConfigPush_InheritsEnvMirrorDeletions(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "custom-key")

	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	// Captain-local (non-inherited) key must survive regardless of env.
	os.WriteFile(filepath.Join(smHome, "config", "model"), []byte("some-model\n"), 0644)

	storeBase := func(harness string) error {
		overlay := config.ProjectOverlay{}
		if harness != "" {
			overlay.SoldierHarness = harness
		}
		return config.StoreFleetBase(parent, config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        overlay,
		})
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storeBase("pi"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}
	if _, err := os.Stat(filepath.Join(smHome, "config", "model")); os.IsNotExist(err) {
		t.Error("non-inheritable model should NOT have been deleted")
	}

	// Parent removes the inherited harness — mirror deletion applies with env set.
	if err := storeBase(""); err != nil {
		t.Fatal(err)
	}
	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	snapshot, err = config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config().SoldierHarness != "" {
		t.Errorf("snapshot soldierHarness = %q after removal, want empty (mirror deletion)", snapshot.Config().SoldierHarness)
	}
	if _, err := os.Stat(filepath.Join(smHome, "config", "model")); os.IsNotExist(err) {
		t.Error("non-inheritable model should NOT have been deleted")
	}
}

// TestConfigPush_InheritsAllowsEmptyEnvListCaptains proves that an empty
// MUNSU_INHERITABLE_CONFIG does not change propagation: the typed resolved
// config is still fully inherited.
func TestConfigPush_InheritsAllowsEmptyEnvListCaptains(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "")

	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-sm", Path: smHome, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-sm", smHome, "", "test-sm"); err != nil {
		t.Fatal(err)
	}

	if err := configPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	// Empty env falls back to default behavior — the resolved config is
	// propagated as usual.
	snapshot, err := config.LoadPublishedSnapshot(smHome)
	if err != nil {
		t.Fatalf("resolved snapshot was not published: %v", err)
	}
	if snapshot.Config().SoldierHarness != "pi" {
		t.Errorf("snapshot soldierHarness = %q, want %q", snapshot.Config().SoldierHarness, "pi")
	}
}

// TestConfigPush_RefusesTrackedDestination proves that ConfigPush refuses when
// a destination file is tracked in captain git, even if the path is safe.
// The published resolved snapshot (config/resolved-project.json) is committed
// to the captain repo AFTER seeding to simulate a user-tracked file that
// ConfigPush must not overwrite.
func TestConfigPush_RefusesTrackedDestination(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	// Track AGENTS.md so the worktree seed carries a user-owned file.
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("# Agents\n"), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")

	homePath := filepath.Join(parent, "captains", "test-captain")
	// Typed parent config binds the captain to a project so the seed's
	// PropagateConfig publishes a resolved snapshot into the worktree.
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-captain", Path: project, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreCaptainRegistry(parent, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
		Captains: []config.CaptainRecord{
			{ID: "test-captain", Home: homePath, Project: "test-captain"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Commit the published snapshot in the captain worktree so the next
	// configPush must refuse to overwrite a tracked destination.
	// config/ is git-ignored via worktree exclude, so force-add is required.
	if _, err := os.Stat(filepath.Join(homePath, config.PublishedSnapshotPath)); err != nil {
		t.Fatalf("seed did not publish a resolved snapshot: %v", err)
	}
	gitTestRun(t, homePath, "add", "-f", config.PublishedSnapshotPath)
	gitTestRun(t, homePath, "commit", "-m", "track resolved snapshot")

	// ConfigPush should refuse because the snapshot destination is tracked.
	err := configPush(parent, homePath)
	if err == nil {
		t.Fatal("expected error for tracked destination in git worktree")
	}
	if !strings.Contains(err.Error(), "is tracked in captain git") {
		t.Errorf("error should mention tracked in captain git, got: %v", err)
	}
}

// =============================================================================
// Managed-home clean-state parity tests
// =============================================================================

// TestManagedCleanState_PreservesHolds proves that holds/*.hold files survive
// ConfigPush/RefreshCharter and do not dirty git status in a managed worktree.
func TestManagedCleanState_PreservesHolds(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	// Write AGENTS.md into source repo so it's tracked.
	trackedAgents := "# Project AGENTS.md\n\nUser-owned tracked content.\n"
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(trackedAgents), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	// Create origin/main tracking ref for SeedFromWorktree.
	gitTestRun(t, project, "remote", "set-head", "origin", "main")

	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Create holds directory with a hold file — simulates in-flight soldier hold.
	holdsDir := filepath.Join(homePath, "holds")
	os.MkdirAll(holdsDir, 0755)
	holdContent := "hold: some-reason\ncreated: 2026-07-23\n"
	os.WriteFile(filepath.Join(holdsDir, "TASK-42.hold"), []byte(holdContent), 0644)
	os.WriteFile(filepath.Join(holdsDir, "TASK-99.hold"), []byte("hold: awaiting-review\n"), 0644)

	// Run ConfigPush — must not remove holds or dirty git.
	if err := configPush(parent, homePath); err != nil {
		t.Fatal(err)
	}

	// Assert holds still exist.
	data, err := os.ReadFile(filepath.Join(holdsDir, "TASK-42.hold"))
	if err != nil {
		t.Errorf("TASK-42.hold missing after ConfigPush: %v", err)
	} else if string(data) != holdContent {
		t.Errorf("TASK-42.hold content changed: %q", string(data))
	}

	if _, err := os.Stat(filepath.Join(holdsDir, "TASK-99.hold")); os.IsNotExist(err) {
		t.Error("TASK-99.hold missing after ConfigPush")
	}

	// Assert tracked AGENTS.md is still byte-for-byte unchanged.
	agentsBody, err := os.ReadFile(filepath.Join(homePath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agentsBody) != trackedAgents {
		t.Fatalf("AGENTS.md was modified:\nwant: %q\ngot:  %q", trackedAgents, string(agentsBody))
	}

	// Assert git status is clean.
	statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Fatalf("worktree has uncommitted changes after ConfigPush:\n%s", string(statusOut))
	}

	// Assert .captain-charter.md exists with current charter.
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); os.IsNotExist(err) {
		t.Errorf("%s missing after ConfigPush", CaptainCharterName)
	}

	// RefreshCharter and re-assert cleanliness.
	if err := RefreshCharter(homePath, parent); err != nil {
		t.Fatal(err)
	}

	// Holds still present.
	if _, err := os.Stat(filepath.Join(holdsDir, "TASK-42.hold")); os.IsNotExist(err) {
		t.Error("TASK-42.hold missing after RefreshCharter")
	}
	// Git still clean.
	statusOut2, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut2))) > 0 {
		t.Fatalf("worktree dirty after RefreshCharter:\n%s", string(statusOut2))
	}
}

// TestManagedCleanState_OperationalDirsAreIgnored proves that all operational dirs
// (state/, config/, tmp/, sessions/, holds/) are git-ignored and do not appear
// in git status --porcelain in a managed worktree.
func TestManagedCleanState_OperationalDirsAreIgnored(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	// Track AGENTS.md so we can verify it stays clean.
	trackedAgents := "# Project AGENTS.md\n"
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(trackedAgents), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	gitTestRun(t, project, "remote", "set-head", "origin", "main")

	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Write content to each operational dir.
	os.WriteFile(filepath.Join(homePath, "state", "test.state"), []byte("op state\n"), 0644)
	os.WriteFile(filepath.Join(homePath, "tmp", "test.tmp"), []byte("temp data\n"), 0644)
	os.WriteFile(filepath.Join(homePath, "sessions", "test.session"), []byte("session data\n"), 0644)
	os.MkdirAll(filepath.Join(homePath, "holds"), 0755)
	os.WriteFile(filepath.Join(homePath, "holds", "test.hold"), []byte("hold data\n"), 0644)
	// config/ is already created by seed; write a file in it.
	os.WriteFile(filepath.Join(homePath, "config", "local-config"), []byte("local config\n"), 0644)

	// Assert all operational dirs are git-ignored.
	for _, dir := range []string{"state/test.state", "tmp/test.tmp", "sessions/test.session", "holds/test.hold"} {
		// check-ignore must return exit 0 for ignored paths.
		if err := exec.Command("git", "-C", homePath, "check-ignore", "-q", "--", dir).Run(); err != nil {
			t.Errorf("%s should be git-ignored, but check-ignore failed: %v", dir, err)
		}
	}

	// config/local-config is NOT gitignored (config/ is excluded at worktree level).
	if err := exec.Command("git", "-C", homePath, "check-ignore", "-q", "--", "config/local-config").Run(); err != nil {
		t.Errorf("config/local-config should be git-ignored via worktree exclude, but check-ignore failed: %v", err)
	}

	// Git status must be clean despite all the operational content.
	statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Fatalf("worktree has uncommitted changes despite operational dir content:\n%s", string(statusOut))
	}
}

// TestManagedCleanState_AGENTSMD_PreservedAfterMultipleConfigPush proves that
// tracked AGENTS.md is preserved byte-for-byte across multiple ConfigPush + RefreshCharter
// cycles, and the worktree remains git-clean throughout.
func TestManagedCleanState_AGENTSMD_PreservedAfterMultipleConfigPush(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	trackedAgents := "# My Custom AGENTS.md\n\nThis content must survive multiple pushes.\n"
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(trackedAgents), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	gitTestRun(t, project, "remote", "set-head", "origin", "main")

	homePath := filepath.Join(parent, "captains", "test-captain")
	// Typed parent config binds the captain to a project so each configPush
	// publishes a resolved snapshot into the worktree.
	if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreProjectRegistry(parent, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-captain", Path: project, Mode: "no-mistakes"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreCaptainRegistry(parent, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
		Captains: []config.CaptainRecord{
			{ID: "test-captain", Home: homePath, Project: "test-captain"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Run multiple ConfigPush + RefreshCharter cycles.
	for i := 0; i < 5; i++ {
		// Vary the inherited harness each cycle to verify inheritance pushes.
		content := fmt.Sprintf("pi-%d", i)
		if err := config.StoreFleetBase(parent, config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        config.ProjectOverlay{SoldierHarness: content},
		}); err != nil {
			t.Fatalf("StoreFleetBase cycle %d: %v", i, err)
		}

		if err := configPush(parent, homePath); err != nil {
			t.Fatalf("ConfigPush cycle %d failed: %v", i, err)
		}
		if err := RefreshCharter(homePath, parent); err != nil {
			t.Fatalf("RefreshCharter cycle %d failed: %v", i, err)
		}

		// AGENTS.md must remain unchanged.
		agentsBody, err := os.ReadFile(filepath.Join(homePath, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(agentsBody) != trackedAgents {
			t.Fatalf("AGENTS.md changed after cycle %d:\nwant: %q\ngot:  %q", i, trackedAgents, string(agentsBody))
		}

		// Git status must stay clean.
		statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.TrimSpace(string(statusOut))) > 0 {
			t.Fatalf("worktree dirty after cycle %d:\n%s", i, string(statusOut))
		}

		// Inherited config must reflect latest push.
		snapshot, err := config.LoadPublishedSnapshot(homePath)
		if err != nil {
			t.Fatalf("resolved snapshot missing after cycle %d: %v", i, err)
		}
		if snapshot.Config().SoldierHarness != content {
			t.Errorf("snapshot soldierHarness cycle %d = %q, want %q", i, snapshot.Config().SoldierHarness, content)
		}
	}

	// Final check: .captain-charter.md exists and is up-to-date.
	charterBody, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatalf("%s missing after cycles: %v", CaptainCharterName, err)
	}
	if !strings.Contains(string(charterBody), CaptainCharterVersion) {
		t.Errorf("%s should contain version %q", CaptainCharterName, CaptainCharterVersion)
	}
}

// TestManagedCleanState_HoldsSurviveMultipleCycles proves that runtime holds
// (*.hold files in holds/) persist across multiple ConfigPush/RefreshCharter cycles
// without being removed and without dirtying git status.
func TestManagedCleanState_HoldsSurviveMultipleCycles(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	trackedAgents := "# Project AGENTS.md\n"
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(trackedAgents), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	gitTestRun(t, project, "remote", "set-head", "origin", "main")

	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Create holds before any ConfigPush.
	holdsDir := filepath.Join(homePath, "holds")
	os.MkdirAll(holdsDir, 0755)
	holdPaths := []string{
		filepath.Join(holdsDir, "TASK-1.hold"),
		filepath.Join(holdsDir, "TASK-2.hold"),
		filepath.Join(holdsDir, "TASK-3.hold"),
	}
	for _, p := range holdPaths {
		os.WriteFile(p, []byte("hold: active\n"), 0644)
	}

	// Parent config so ConfigPush does work.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Run multiple cycles — holds must survive.
	for i := 0; i < 3; i++ {
		if err := configPush(parent, homePath); err != nil {
			t.Fatalf("ConfigPush cycle %d failed: %v", i, err)
		}
		if err := RefreshCharter(homePath, parent); err != nil {
			t.Fatalf("RefreshCharter cycle %d failed: %v", i, err)
		}

		// All holds still exist.
		for _, p := range holdPaths {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				t.Errorf("hold %s vanished after cycle %d", filepath.Base(p), i)
			}
		}

		// Git status must remain clean.
		statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.TrimSpace(string(statusOut))) > 0 {
			t.Fatalf("worktree dirty after cycle %d:\n%s", i, string(statusOut))
		}
	}

	// Final count check.
	entries, err := os.ReadDir(holdsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 hold files after cycles, got %d", len(entries))
	}
}

// TestManagedCleanState_ConfigPushDoesNotTouchUntrackedFiles proves that
// ConfigPush does not dirty git status when untracked-but-gitignored files exist
// in the worktree.
func TestManagedCleanState_ConfigPushDoesNotTouchUntrackedFiles(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()

	trackedAgents := "# Project AGENTS.md\n"
	os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(trackedAgents), 0644)
	gitTestRun(t, project, "add", "AGENTS.md")
	gitTestRun(t, project, "commit", "-m", "add AGENTS.md")
	gitTestRun(t, project, "push", "-u", "origin", "main")
	gitTestRun(t, project, "remote", "set-head", "origin", "main")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// Create a runtime state file BEFORE ConfigPush.
	os.WriteFile(filepath.Join(homePath, "state", "run-token"), []byte("abc123\n"), 0644)

	// Run ConfigPush.
	if err := configPush(parent, homePath); err != nil {
		t.Fatal(err)
	}

	// The state/run-token file must still exist.
	data, err := os.ReadFile(filepath.Join(homePath, "state", "run-token"))
	if err != nil {
		t.Errorf("state/run-token missing after ConfigPush: %v", err)
	} else if string(data) != "abc123\n" {
		t.Errorf("state/run-token content changed: %q", string(data))
	}

	// And git must be clean.
	statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Fatalf("worktree dirty after ConfigPush with runtime files:\n%s", string(statusOut))
	}
}

// TestMigrateToWorktree_HoldsPreservedClean proves that holds/*.hold files are
// preserved during Managed migration and the new worktree remains git-clean.
// This is the regression test for captain-migration-holds-clean: copied holds
// survive the atomic swap and do not dirty the managed worktree's git status.
func TestMigrateToWorktree_HoldsPreservedClean(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	id := "test-captain"
	smHome := stateOnlyHomeFixture(t, parent, id)

	// Create holds directory with multiple hold files — simulates in-flight
	// soldier decision holds that must survive migration to managed worktree.
	holdsDir := filepath.Join(smHome, "holds")
	if err := os.MkdirAll(holdsDir, 0755); err != nil {
		t.Fatal(err)
	}

	holdContents := map[string]string{
		"nomistakes-pi-contract-decision-fix-location.hold": "origin-id=nomistakes-pi-contract\ndecision-key=fix-location\nreason=Should the permanent fix live in no-mistakes upstream (small PR to add pi neutralization) or in munsu's own no-mistakes adapter/fork?\n",
		"nomistakes-pi-contract-decision-adm-priority.hold": "origin-id=nomistakes-pi-contract\ndecision-key=adm-priority\nreason=Should ADM priority be treated as blocking or advisory for no-mistakes gate?\n",
		"TASK-42.hold": "hold: awaiting-general-decision\ncreated: 2026-07-23\n",
		"TASK-99.hold": "hold: awaiting-review\ncreated: 2026-07-23\n",
	}

	var holdFilePaths []string
	for name, content := range holdContents {
		p := filepath.Join(holdsDir, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		holdFilePaths = append(holdFilePaths, p)
	}

	// Write some operational state for cross-check.
	os.MkdirAll(filepath.Join(smHome, "state", "sub"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", "sub", "data.txt"), []byte("runtime\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "config", "custom.cfg"), []byte("setting=1\n"), 0644)

	// Run migration.
	if err := migrateToWorktreeTest(smHome, project, id, parent); err != nil {
		t.Fatal(err)
	}

	// 1. Home is now a managed worktree.
	managed, err := isManagedWorktree(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if !managed {
		t.Fatal("home should be a managed worktree after migration")
	}

	// 2. All hold files were copied and content is byte-for-byte identical.
	for name, wantContent := range holdContents {
		p := filepath.Join(smHome, "holds", name)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("hold %q not found in migrated worktree: %v", name, err)
			continue
		}
		if string(data) != wantContent {
			t.Errorf("hold %q content changed:\nwant: %q\ngot:  %q", name, wantContent, string(data))
		}
	}

	// 3. Git status is clean — holds/ is excluded via info/exclude.
	statusOut, err := exec.Command("git", "-C", smHome, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Fatalf("worktree is not git-clean after migration:\n%s", string(statusOut))
	}

	// 4. Backup directory exists and contains the original holds (migration evidence).
	backupGlob, err := filepath.Glob(smHome + ".backup-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backupGlob) == 0 {
		t.Error("backup directory not found after migration")
	} else {
		backupHolds := filepath.Join(backupGlob[0], "holds")
		for name := range holdContents {
			if _, err := os.Stat(filepath.Join(backupHolds, name)); os.IsNotExist(err) {
				t.Errorf("hold %q missing from backup at %s", name, backupHolds)
			}
		}
		// Also verify state/config survived in backup.
		if _, err := os.Stat(filepath.Join(backupGlob[0], "state", "sub", "data.txt")); os.IsNotExist(err) {
			t.Error("state/sub/data.txt missing from backup")
		}
		if _, err := os.Stat(filepath.Join(backupGlob[0], "config", "custom.cfg")); os.IsNotExist(err) {
			t.Error("config/custom.cfg missing from backup")
		}
	}

	// 5. Operational state was also preserved in the live worktree.
	data, err := os.ReadFile(filepath.Join(smHome, "state", "sub", "data.txt"))
	if err != nil {
		t.Errorf("state/sub/data.txt not preserved: %v", err)
	} else if string(data) != "runtime\n" {
		t.Errorf("state/sub/data.txt content = %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(smHome, "config", "custom.cfg"))
	if err != nil {
		t.Errorf("config/custom.cfg not preserved: %v", err)
	} else if string(data) != "setting=1\n" {
		t.Errorf("config/custom.cfg content = %q", string(data))
	}

	// 6. Worktree admin path points at final home (no temp path).
	wtListCaptains := gitTestRun(t, project, "worktree", "list", "--porcelain")
	if !strings.Contains(wtListCaptains, smHome) {
		t.Errorf("git worktree list missing final home %s", smHome)
	}
	if strings.Contains(wtListCaptains, ".worktree-") {
		t.Errorf("git worktree list still has temp path; got:\n%s", wtListCaptains)
	}

	// 7. .captain-charter.md and provenance exist.
	if _, err := os.Stat(filepath.Join(smHome, CaptainCharterName)); os.IsNotExist(err) {
		t.Errorf("%s missing after migration", CaptainCharterName)
	}
	if _, err := os.Stat(filepath.Join(smHome, CaptainProvenanceName)); os.IsNotExist(err) {
		t.Errorf("%s missing after migration", CaptainProvenanceName)
	}
}

func TestSeedCaptainFromWorktree_RequiresIntegrationBeforeMutation(t *testing.T) {
	h := filepath.Join(t.TempDir(), "captain")
	err := SeedCaptainFromWorktree(CaptainWorktreeSeedOptions{ID: "test", Home: h, Repo: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "integration capability") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(h); !os.IsNotExist(err) {
		t.Fatalf("home mutated before capability check: %v", err)
	}
}

func TestSeedCaptainFromWorktree_IntegrationFailureRollsBack(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")
	h := filepath.Join(t.TempDir(), "captain")
	port := &countingIntegrationPort{err: fmt.Errorf("install failed")}
	err := SeedCaptainFromWorktree(CaptainWorktreeSeedOptions{ID: "test-captain", Home: h, Repo: repo, ParentHome: parent, Integration: port})
	if err == nil || port.calls != 1 {
		t.Fatalf("error=%v calls=%d", err, port.calls)
	}
	if _, statErr := os.Stat(h); !os.IsNotExist(statErr) {
		t.Fatalf("home not rolled back: %v", statErr)
	}
}

func TestSeedCaptainFromWorktree_InvokesIntegrationOnce(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent, "https://github.com/test/repo.git")
	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")
	port := &countingIntegrationPort{}
	err := SeedCaptainFromWorktree(CaptainWorktreeSeedOptions{ID: "test-captain", Home: filepath.Join(parent, "captains/test-captain"), Repo: repo, ParentHome: parent, Integration: port})
	if err != nil || port.calls != 1 {
		t.Fatalf("error=%v calls=%d", err, port.calls)
	}
}

func TestMigrateCaptainToWorktree_InvokesIntegrationOnce(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	id := "test-captain"
	h := stateOnlyHomeFixture(t, parent, id)
	port := &countingIntegrationPort{}
	err := MigrateCaptainToWorktree(CaptainMigrationOptions{CaptainHome: h, Repo: project, ID: id, ParentHome: parent, Integration: port})
	if err != nil || port.calls != 1 {
		t.Fatalf("error=%v calls=%d", err, port.calls)
	}
}

func TestMigrateCaptainToWorktree_IntegrationFailureRestoresHome(t *testing.T) {
	project := newWorktreeFixture(t)
	parent := t.TempDir()
	id := "test-captain"
	h := stateOnlyHomeFixture(t, parent, id)
	sentinel := filepath.Join(h, "state", "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserve me"), 0644); err != nil {
		t.Fatal(err)
	}
	port := &countingIntegrationPort{err: fmt.Errorf("install failed")}
	err := MigrateCaptainToWorktree(CaptainMigrationOptions{CaptainHome: h, Repo: project, ID: id, ParentHome: parent, Integration: port})
	if err == nil || port.calls != 1 {
		t.Fatalf("error=%v calls=%d", err, port.calls)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve me" {
		t.Fatalf("sentinel=%q error=%v", got, readErr)
	}
	if managed, _ := isManagedWorktree(h); managed {
		t.Fatal("failed migration remained authoritative worktree")
	}
	if !isStateOnlyHome(h) {
		t.Fatal("original state-only home was not restored")
	}
}
