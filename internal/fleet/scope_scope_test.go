//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// initRepoWithRemote is like initRepo but also adds a remote origin.
func initRepoWithRemote(t *testing.T, dir, originURL string) string {
	t.Helper()
	repo := initRepo(t, dir)
	scopeRunCmd(t, repo, "git", "remote", "add", "origin", originURL)
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
	scopeRunCmd(t, primaryDir, "git", "worktree", "add", wtDir)

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

func TestClassifyIdentity_DeletedPathFailsClosed(t *testing.T) {
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	identity, _, _, err := ClassifyIdentity(nonexistent)
	if err == nil {
		t.Fatal("expected deleted path classification error")
	}
	if identity != Unknown {
		t.Errorf("errored classification = %v, want Unknown (never a verdict it did not reach)", identity)
	}
}

// TestClassify_ErrorIdentityIsUnknown pins the zero value of Identity. It used
// to be Primary, so a Classify that failed before assigning anything reported
// "primary" for a path it could not classify at all — wrong evidence today,
// and a loaded trap for any future `if identity == Primary { block }` written
// without an Err check.
func TestClassify_ErrorIdentityIsUnknown(t *testing.T) {
	res := Classify(filepath.Join(t.TempDir(), "does-not-exist"))
	if res.Err == nil {
		t.Fatal("expected classification error for a deleted path")
	}
	if res.Identity != Unknown {
		t.Errorf("Identity = %v (%s), want Unknown", res.Identity, res.Identity)
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
	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
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
	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
	if cap != GatePresent {
		t.Errorf("with NO_MISTAKES_GATE=1, cap = %v, want GatePresent", cap)
	}
	if source != "env" {
		t.Errorf("with NO_MISTAKES_GATE, source = %q, want 'env'", source)
	}
}

func TestDetectGateCapability_EnvVarEmptyUsesPresence(t *testing.T) {
	repo := initRepo(t, t.TempDir())
	t.Setenv("NO_MISTAKES_GATE", "")
	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
	if cap != GatePresent || source != "env" {
		t.Fatalf("cap=%v source=%q, want gate-present env", cap, source)
	}
}

func TestDetectGateCapability_GitCommonDirMarker(t *testing.T) {
	checkout, _ := initGateCheckout(t)
	scoped := Classify(checkout)
	cap, source := scoped.GateCap, scoped.GateSource
	if cap != GatePresent || source != "git-common-dir" {
		t.Fatalf("cap=%v source=%q, want gate-present git-common-dir", cap, source)
	}
}

func TestDetectGateCapability_MarkerNoMatch(t *testing.T) {
	// Create a repo
	repo := initRepo(t, t.TempDir())
	originURL := "https://github.com/test-owner/other-repo.git"
	scopeRunCmd(t, repo, "git", "remote", "add", "origin", originURL)

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

	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
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

	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
	if cap != GateAbsent {
		t.Errorf("without markers dir, cap = %v, want GateAbsent", cap)
	}
	if source != "" {
		t.Errorf("without markers dir, source = %q, want empty", source)
	}
}

// --- Full classification tests ---

func TestClassify_GitCommonDirGate(t *testing.T) {
	checkout, commonDir := initGateCheckout(t)
	commonDir, err := canonicalPath(commonDir)
	if err != nil {
		t.Fatal(err)
	}
	res := Classify(checkout)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.CommonDir != commonDir {
		t.Fatalf("common dir = %q, want %q", res.CommonDir, commonDir)
	}
	if res.GateCap != GatePresent || res.GateSource != "git-common-dir" || res.GateRefusalError() == nil {
		t.Fatalf("result = %+v", res)
	}
}

func TestClassify_WorktreeWithGate(t *testing.T) {
	primaryDir := initRepoWithRemote(t, t.TempDir(), "https://github.com/test-owner/gated-repo.git")
	wtDir := filepath.Join(t.TempDir(), "wt")
	scopeRunCmd(t, primaryDir, "git", "worktree", "add", wtDir)

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
	if res.GateRefusalError() == nil {
		t.Error("ambient gate marker must refuse fleet entry from a worktree")
	}
}

func TestClassify_WorktreeWithoutGate(t *testing.T) {
	primaryDir := initRepo(t, t.TempDir())
	wtDir := filepath.Join(t.TempDir(), "wt")
	scopeRunCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	res := Classify(wtDir)

	if res.Identity != Worktree {
		t.Errorf("Classify(%q).Identity = %v, want Worktree", wtDir, res.Identity)
	}
	if res.GateRefusalError() != nil {
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
	if res.GateRefusalError() != nil {
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
	if res.GateRefusalError() == nil {
		t.Error("ambient gate marker must refuse fleet entry from an unrelated cwd")
	}
}

func TestClassifyIdentity_GitUnavailableFailsClosed(t *testing.T) {
	repo := initRepo(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if _, _, _, err := ClassifyIdentity(repo); err == nil {
		t.Fatal("expected git unavailable error")
	}
}

// --- Precedence tests ---

func TestGatePrecedence_EnvOverMarker(t *testing.T) {
	// When env is set, even absent/NM_HOME should not matter
	repo := initRepo(t, t.TempDir())

	t.Setenv("NO_MISTAKES_GATE", "1")
	t.Setenv("NM_HOME", filepath.Join(t.TempDir(), "nonexistent"))

	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
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

	scoped := Classify(repo)
	cap, source := scoped.GateCap, scoped.GateSource
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
	if res.GateRefusalError() == nil {
		t.Error("Classify(symlinked primary with gate).IsGateRefusal() = false, want true")
	}
}
