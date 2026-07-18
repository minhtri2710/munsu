package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCmd is a test helper that runs a command in a directory.
func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed (dir=%s): %v\n%s", name, args, dir, err, string(out))
	}
}

// initRepo creates a minimal git repo at dir with an initial commit and
// returns the canonical resolved path.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "initial")
	return dir
}

// initRepoWithRemote is like initRepo but also adds a remote origin.
func initRepoWithRemote(t *testing.T, dir, originURL string) string {
	t.Helper()
	repo := initRepo(t, dir)
	runCmd(t, repo, "git", "remote", "add", "origin", originURL)
	return repo
}

// --- ClassifyIdentity tests ---

func TestClassifyIdentity_PrimaryCheckout(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	identity, gitDir, commonDir, err := ClassifyIdentity(repo)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", repo, err)
	}
	if identity != Primary {
		t.Errorf("ClassifyIdentity(%q) = %v, want Primary", repo, identity)
	}
	if gitDir != commonDir {
		t.Errorf("gitDir (%s) should equal commonDir (%s) for primary checkout", gitDir, commonDir)
	}
}

func TestClassifyIdentity_UnrelatedPath(t *testing.T) {
	tmp := t.TempDir()
	// No git repo here

	identity, gitDir, commonDir, err := ClassifyIdentity(tmp)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", tmp, err)
	}
	if identity != Unrelated {
		t.Errorf("ClassifyIdentity(%q) = %v, want Unrelated", tmp, identity)
	}
	if gitDir != "" || commonDir != "" {
		t.Errorf("expected empty gitDir/commonDir for unrelated, got %q / %q", gitDir, commonDir)
	}
}

func TestClassifyIdentity_Worktree(t *testing.T) {
	// Create a primary repo
	primaryDir := initRepo(t, t.TempDir())

	// Create a worktree
	wtDir := filepath.Join(t.TempDir(), "worktree")
	runCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	identity, gitDir, commonDir, err := ClassifyIdentity(wtDir)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", wtDir, err)
	}
	if identity != Worktree {
		t.Errorf("ClassifyIdentity(%q) = %v, want Worktree", wtDir, identity)
	}
	if gitDir == commonDir {
		t.Errorf("gitDir (%s) should NOT equal commonDir (%s) for worktree", gitDir, commonDir)
	}
}

func TestClassifyIdentity_DeletedPath(t *testing.T) {
	// A path that doesn't exist
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")

	identity, gitDir, commonDir, err := ClassifyIdentity(nonexistent)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", nonexistent, err)
	}
	if identity != Unrelated {
		t.Errorf("ClassifyIdentity(%q) = %v, want Unrelated for non-existent path", nonexistent, identity)
	}
	if gitDir != "" || commonDir != "" {
		t.Errorf("expected empty gitDir/commonDir for non-existent, got %q / %q", gitDir, commonDir)
	}
}

func TestClassifyIdentity_SymlinkedRepo(t *testing.T) {
	realDir := initRepo(t, t.TempDir())

	// Create a symlink to the real repo
	symDir := filepath.Join(t.TempDir(), "symlink-repo")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Fatal(err)
	}

	identity, gitDir, commonDir, err := ClassifyIdentity(symDir)
	if err != nil {
		t.Fatalf("ClassifyIdentity via symlink = _, _, _, %v", err)
	}
	// The symlinked path should resolve to the same identity as the real dir
	if identity != Primary {
		t.Errorf("ClassifyIdentity via symlink = %v, want Primary", identity)
	}
	if gitDir != commonDir {
		t.Errorf("gitDir (%s) should equal commonDir (%s) for primary via symlink", gitDir, commonDir)
	}
}

// --- Gate capability tests ---

func TestDetectGateCapability_EnvVar(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	// NO_MISTAKES_GATE not set -> absent
	cap, source := DetectGateCapability(repo)
	if cap != GateAbsent {
		t.Errorf("without NO_MISTAKES_GATE, cap = %v, want GateAbsent", cap)
	}
	if source != "" {
		t.Errorf("without NO_MISTAKES_GATE, source = %q, want empty", source)
	}
}

func TestDetectGateCapability_EnvVarSet(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	t.Setenv("NO_MISTAKES_GATE", "1")
	cap, source := DetectGateCapability(repo)
	if cap != GatePresent {
		t.Errorf("with NO_MISTAKES_GATE=1, cap = %v, want GatePresent", cap)
	}
	if source != "env" {
		t.Errorf("with NO_MISTAKES_GATE, source = %q, want 'env'", source)
	}
}

func TestDetectGateCapability_EnvVarEmpty(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	t.Setenv("NO_MISTAKES_GATE", "")
	cap, source := DetectGateCapability(repo)
	if cap != GateAbsent {
		t.Errorf("with NO_MISTAKES_GATE='', cap = %v, want GateAbsent (empty string not treated as present)", cap)
	}
	if source != "" {
		t.Errorf("with NO_MISTAKES_GATE='', source = %q, want empty", source)
	}
}

func TestDetectGateCapability_MarkerDir(t *testing.T) {
	// Create a repo with a known origin URL
	repo := initRepo(t, t.TempDir())
	originURL := "https://github.com/test-owner/test-repo.git"
	runCmd(t, repo, "git", "remote", "add", "origin", originURL)

	// Create the no-mistakes repos marker directory with a matching marker
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	markersDir := filepath.Join(nmHome, "repos")
	if err := os.MkdirAll(markersDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a marker as a bare git dir
	markerDir := filepath.Join(markersDir, "abc123def456.git")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatal(err)
	}
	markerConfig := "[remote \"origin\"]\n\turl = " + originURL + "\n"
	if err := os.WriteFile(filepath.Join(markerDir, "config"), []byte(markerConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Override NM_HOME for the test
	t.Setenv("NM_HOME", nmHome)

	cap, source := DetectGateCapability(repo)
	if cap != GatePresent {
		t.Errorf("with matching marker, cap = %v, want GatePresent", cap)
	}
	if source != "marker" {
		t.Errorf("with matching marker, source = %q, want 'marker'", source)
	}
}

func TestDetectGateCapability_MarkerNoMatch(t *testing.T) {
	// Create a repo
	repo := initRepo(t, t.TempDir())
	originURL := "https://github.com/test-owner/other-repo.git"
	runCmd(t, repo, "git", "remote", "add", "origin", originURL)

	// Create the no-mistakes repos marker directory with a DIFFERENT marker
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	markersDir := filepath.Join(nmHome, "repos")
	if err := os.MkdirAll(markersDir, 0755); err != nil {
		t.Fatal(err)
	}

	markerDir := filepath.Join(markersDir, "xyz789.git")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatal(err)
	}
	markerConfig := "[remote \"origin\"]\n\turl = https://github.com/different/different-repo.git\n"
	if err := os.WriteFile(filepath.Join(markerDir, "config"), []byte(markerConfig), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NM_HOME", nmHome)

	cap, source := DetectGateCapability(repo)
	if cap != GateAbsent {
		t.Errorf("with non-matching marker, cap = %v, want GateAbsent", cap)
	}
	if source != "" {
		t.Errorf("with non-matching marker, source = %q, want empty", source)
	}
}

func TestDetectGateCapability_MarkerDirNotExist(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	// Point NM_HOME to a non-existent directory
	t.Setenv("NM_HOME", filepath.Join(t.TempDir(), "no-such-dir"))

	cap, source := DetectGateCapability(repo)
	if cap != GateAbsent {
		t.Errorf("without markers dir, cap = %v, want GateAbsent", cap)
	}
	if source != "" {
		t.Errorf("without markers dir, source = %q, want empty", source)
	}
}

// --- Full classification tests ---

func TestClassify_PrimaryNoGate(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	res := Classify(repo)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", repo, res.Err)
	}
	if res.Identity != Primary {
		t.Errorf("Classify(%q).Identity = %v, want Primary", repo, res.Identity)
	}
	if res.GateCap != GateAbsent {
		t.Errorf("Classify(%q).GateCap = %v, want GateAbsent", repo, res.GateCap)
	}
	if res.IsGateRefusal() {
		t.Error("Classify(Primary, no gate).IsGateRefusal() = true, want false")
	}
}

func TestClassify_PrimaryWithEnvGate(t *testing.T) {
	repo := initRepo(t, t.TempDir())
	t.Setenv("NO_MISTAKES_GATE", "1")

	res := Classify(repo)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", repo, res.Err)
	}
	if res.Identity != Primary {
		t.Errorf("Classify(%q).Identity = %v, want Primary", repo, res.Identity)
	}
	if res.GateCap != GatePresent {
		t.Errorf("Classify(%q).GateCap = %v, want GatePresent", repo, res.GateCap)
	}
	if res.GateSource != "env" {
		t.Errorf("Classify(%q).GateSource = %q, want 'env'", repo, res.GateSource)
	}
	if !res.IsGateRefusal() {
		t.Error("Classify(Primary, gate).IsGateRefusal() = false, want true")
	}
	refusalErr := res.GateRefusalError()
	if refusalErr == nil {
		t.Fatal("Classify(Primary, gate).GateRefusalError() = nil, want error")
	}
	if !strings.Contains(refusalErr.Error(), "gate agent refused") {
		t.Errorf("GateRefusalError should contain 'gate agent refused', got: %v", refusalErr)
	}
}

func TestClassify_PrimaryWithMarkerGate(t *testing.T) {
	repo := initRepoWithRemote(t, t.TempDir(), "https://github.com/test-owner/test-repo.git")

	// Create matching marker
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	markersDir := filepath.Join(nmHome, "repos")
	if err := os.MkdirAll(markersDir, 0755); err != nil {
		t.Fatal(err)
	}
	markerDir := filepath.Join(markersDir, "abc123def456.git")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatal(err)
	}
	markerConfig := "[remote \"origin\"]\n\turl = https://github.com/test-owner/test-repo.git\n"
	if err := os.WriteFile(filepath.Join(markerDir, "config"), []byte(markerConfig), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NM_HOME", nmHome)

	res := Classify(repo)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", repo, res.Err)
	}
	if res.Identity != Primary {
		t.Errorf("Classify(%q).Identity = %v, want Primary", repo, res.Identity)
	}
	if res.GateCap != GatePresent {
		t.Errorf("Classify(%q).GateCap = %v, want GatePresent", repo, res.GateCap)
	}
	if res.GateSource != "marker" {
		t.Errorf("Classify(%q).GateSource = %q, want 'marker'", repo, res.GateSource)
	}
	if !res.IsGateRefusal() {
		t.Error("Classify(Primary, marker gate).IsGateRefusal() = false, want true")
	}
}

func TestClassify_WorktreeWithGate(t *testing.T) {
	primaryDir := initRepoWithRemote(t, t.TempDir(), "https://github.com/test-owner/gated-repo.git")
	wtDir := filepath.Join(t.TempDir(), "wt")
	runCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	// Set gate via env
	t.Setenv("NO_MISTAKES_GATE", "1")

	res := Classify(wtDir)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", wtDir, res.Err)
	}
	if res.Identity != Worktree {
		t.Errorf("Classify(%q).Identity = %v, want Worktree", wtDir, res.Identity)
	}
	// Gate is detected even in worktrees (env is global)
	if res.GateCap != GatePresent {
		t.Errorf("Classify(%q).GateCap = %v, want GatePresent", wtDir, res.GateCap)
	}
	// A worktree should NOT trigger gate refusal — only primary checkouts do
	if res.IsGateRefusal() {
		t.Error("Classify(Worktree, gate).IsGateRefusal() = true, want false")
	}
}

func TestClassify_WorktreeWithoutGate(t *testing.T) {
	primaryDir := initRepo(t, t.TempDir())
	wtDir := filepath.Join(t.TempDir(), "wt")
	runCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	res := Classify(wtDir)

	if res.Identity != Worktree {
		t.Errorf("Classify(%q).Identity = %v, want Worktree", wtDir, res.Identity)
	}
	if res.IsGateRefusal() {
		t.Error("Classify(Worktree, no gate).IsGateRefusal() = true, want false")
	}
}

func TestClassify_UnrelatedNoGate(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo

	res := Classify(tmp)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", tmp, res.Err)
	}
	if res.Identity != Unrelated {
		t.Errorf("Classify(%q).Identity = %v, want Unrelated", tmp, res.Identity)
	}
	if res.IsGateRefusal() {
		t.Error("Classify(Unrelated).IsGateRefusal() = true, want false")
	}
}

func TestClassify_UnrelatedWithEnvGate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("NO_MISTAKES_GATE", "1")

	res := Classify(tmp)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", tmp, res.Err)
	}
	if res.Identity != Unrelated {
		t.Errorf("Classify(%q).Identity = %v, want Unrelated", tmp, res.Identity)
	}
	// Even though env is set, unrelated isn't a repo — still no refusal
	if res.IsGateRefusal() {
		t.Error("Classify(Unrelated, env gate).IsGateRefusal() = true, want false")
	}
}

// --- GateRefusalError tests ---

func TestGateRefusalError(t *testing.T) {
	// Non-gated primary — should not error
	repo := initRepo(t, t.TempDir())
	err := GateRefusalError(repo)
	if err != nil {
		t.Errorf("GateRefusalError for non-gated primary = %v, want nil", err)
	}

	// Gated primary — should error
	t.Setenv("NO_MISTAKES_GATE", "1")
	err = GateRefusalError(repo)
	if err == nil {
		t.Error("GateRefusalError for gated primary = nil, want error")
	}
	if !strings.Contains(err.Error(), "gate agent refused") {
		t.Errorf("GateRefusalError should contain 'gate agent refused', got: %v", err)
	}
}

func TestIsGateAgentActive(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	// No gate
	if IsGateAgentActive(repo) {
		t.Error("IsGateAgentActive without gate = true, want false")
	}

	// With env gate
	t.Setenv("NO_MISTAKES_GATE", "1")
	if !IsGateAgentActive(repo) {
		t.Error("IsGateAgentActive with gate = false, want true")
	}
}

// --- Precedence tests ---

func TestGatePrecedence_EnvOverMarker(t *testing.T) {
	// When env is set, even absent/NM_HOME should not matter
	repo := initRepo(t, t.TempDir())

	t.Setenv("NO_MISTAKES_GATE", "1")
	t.Setenv("NM_HOME", filepath.Join(t.TempDir(), "nonexistent"))

	cap, source := DetectGateCapability(repo)
	if cap != GatePresent {
		t.Errorf("with NO_MISTAKES_GATE=1, cap = %v, want GatePresent even without markers", cap)
	}
	if source != "env" {
		t.Errorf("source = %q, want 'env'", source)
	}
}

// --- Malformed marker tests ---

func TestDetectGateCapability_MalformedMarker(t *testing.T) {
	repo := initRepoWithRemote(t, t.TempDir(), "https://github.com/test-owner/target-repo.git")

	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	markersDir := filepath.Join(nmHome, "repos")
	if err := os.MkdirAll(markersDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a marker dir without .git suffix — should be skipped
	plainDir := filepath.Join(markersDir, "notagitsuffix")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a .git dir but with no config file — should not match
	noConfigDir := filepath.Join(markersDir, "noconfig.git")
	if err := os.MkdirAll(noConfigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a proper marker for a different repo — should not match
	diffMarker := filepath.Join(markersDir, "different.git")
	if err := os.MkdirAll(diffMarker, 0755); err != nil {
		t.Fatal(err)
	}
	diffConfig := "[remote \"origin\"]\n\turl = https://github.com/other/different.git\n"
	if err := os.WriteFile(filepath.Join(diffMarker, "config"), []byte(diffConfig), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NM_HOME", nmHome)

	cap, source := DetectGateCapability(repo)
	if cap != GateAbsent {
		t.Errorf("with only non-matching/malformed markers, cap = %v, want GateAbsent", cap)
	}
	if source != "" {
		t.Errorf("source = %q, want empty", source)
	}
}

// --- Symlink through gate detection ---

func TestClassify_SymlinkedPrimaryWithGate(t *testing.T) {
	realDir := initRepo(t, t.TempDir())
	symDir := filepath.Join(t.TempDir(), "symlinked")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NO_MISTAKES_GATE", "1")

	res := Classify(symDir)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", symDir, res.Err)
	}
	if res.Identity != Primary {
		t.Errorf("Classify(symlink).Identity = %v, want Primary (canonical resolution)", res.Identity)
	}
	if !res.IsGateRefusal() {
		t.Error("Classify(symlinked primary with gate).IsGateRefusal() = false, want true")
	}
}
