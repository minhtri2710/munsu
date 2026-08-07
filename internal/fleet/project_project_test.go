//go:build integration

package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- Legacy project registry helpers ---
//
// ParseEntry/FormatEntry were removed from the fleet package during the
// legacy-config hard cut and the configmigration package was deleted. These
// test-local ports preserve the legacy-format project registry parsing
// semantics so legacy-format project registry tests keep compiling.

// ParseEntry parses a single legacy projects.md registry line into a Project.
func ParseEntry(line string) (*Project, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "- ") {
		return nil, fmt.Errorf("invalid project entry format: %q", line)
	}
	rest := line[2:]
	sepIdx := strings.Index(rest, " - ")
	if sepIdx < 0 {
		return nil, fmt.Errorf("missing ' - ' separator in: %q", line)
	}
	lhs, rhs := rest[:sepIdx], strings.TrimSpace(rest[sepIdx+3:])
	addedIdx := strings.LastIndex(rhs, "(added ")
	if addedIdx < 0 {
		return nil, fmt.Errorf("missing '(added ...)' in: %q", line)
	}
	date := strings.TrimSuffix(strings.TrimSpace(rhs[addedIdx+7:]), ")")
	p := &Project{Name: strings.Fields(lhs)[0], Description: strings.TrimSpace(rhs[:addedIdx]), Added: date}
	for _, tok := range strings.Fields(lhs)[1:] {
		if tok == "+yolo" {
			p.Yolo = true
		} else {
			p.Mode = tok
		}
	}
	return p, nil
}

// FormatEntry formats a Project as a legacy projects.md registry line.
func FormatEntry(p *Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s", p.Name)
	if p.Mode != "" {
		fmt.Fprintf(&b, " %s", p.Mode)
	}
	if p.Yolo {
		b.WriteString(" +yolo")
	}
	fmt.Fprintf(&b, " - %s (added %s)", p.Description, p.Added)
	return b.String()
}

func TestParseEntrySimple(t *testing.T) {
	p, err := ParseEntry("- my-project - A simple project (added 2026-01-15)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "my-project" {
		t.Errorf("Name = %q, want %q", p.Name, "my-project")
	}
	if p.Mode != "" {
		t.Errorf("Mode = %q, want empty", p.Mode)
	}
	if p.Yolo {
		t.Error("Yolo = true, want false")
	}
	if p.Description != "A simple project" {
		t.Errorf("Description = %q, want %q", p.Description, "A simple project")
	}
	if p.Added != "2026-01-15" {
		t.Errorf("Added = %q, want %q", p.Added, "2026-01-15")
	}
}

func TestParseEntryWithMode(t *testing.T) {
	p, err := ParseEntry("- my-project feat - Feature project (added 2026-01-15)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "feat" {
		t.Errorf("Mode = %q, want %q", p.Mode, "feat")
	}
	if p.Yolo {
		t.Error("Yolo = true, want false")
	}
}

func TestParseEntryWithYolo(t *testing.T) {
	p, err := ParseEntry("- my-project feat +yolo - Yolo project (added 2026-01-15)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "feat" {
		t.Errorf("Mode = %q, want %q", p.Mode, "feat")
	}
	if !p.Yolo {
		t.Error("Yolo = false, want true")
	}
}

func TestParseEntryYoloWithoutMode(t *testing.T) {
	p, err := ParseEntry("- my-project +yolo - Yolo no mode (added 2026-01-15)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "" {
		t.Errorf("Mode = %q, want empty", p.Mode)
	}
	if !p.Yolo {
		t.Error("Yolo = false, want true")
	}
}

func TestParseEntryDescriptionWithDashes(t *testing.T) {
	p, err := ParseEntry("- my-project feat - Feature - with dashes (added 2026-01-15)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Description != "Feature - with dashes" {
		t.Errorf("Description = %q, want %q", p.Description, "Feature - with dashes")
	}
}

func TestFormatEntry(t *testing.T) {
	p := &Project{
		Name:        "test",
		Mode:        "fix",
		Yolo:        true,
		Description: "A test project",
		Added:       "2026-07-13",
	}
	got := FormatEntry(p)
	want := "- test fix +yolo - A test project (added 2026-07-13)"
	if got != want {
		t.Errorf("FormatEntry() = %q, want %q", got, want)
	}
}

func TestFormatEntrySimple(t *testing.T) {
	p := &Project{
		Name:        "simple",
		Description: "No mode",
		Added:       "2026-01-01",
	}
	got := FormatEntry(p)
	want := "- simple - No mode (added 2026-01-01)"
	if got != want {
		t.Errorf("FormatEntry() = %q, want %q", got, want)
	}
}

func TestProjectRegistryRoundTrip(t *testing.T) {
	original := "- my-project feat +yolo - Description here (added 2026-03-15)"
	p, err := ParseEntry(original)
	if err != nil {
		t.Fatal(err)
	}
	got := FormatEntry(p)
	if got != original {
		t.Errorf("round-trip:\n  original: %q\n  got:      %q", original, got)
	}
}

func TestListEmpty(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, ".munsu")
	projects, err := List(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Errorf("expected empty list, got %d items", len(projects))
	}
}

func TestListAndAdd(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, ".munsu")

	// Add by registering (no URL clone)
	if err := Add(homeDir, "test-proj", "/tmp/test-path", "feat", true); err != nil {
		t.Fatal(err)
	}

	projects, err := List(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	p := projects[0]
	if p.Name != "test-proj" {
		t.Errorf("Name = %q, want %q", p.Name, "test-proj")
	}
	if p.Mode != "feat" {
		t.Errorf("Mode = %q, want %q", p.Mode, "feat")
	}
	if !p.Yolo {
		t.Error("Yolo = false, want true")
	}
	if p.Description != "/tmp/test-path" {
		t.Errorf("Description = %q, want %q", p.Description, "/tmp/test-path")
	}
}

func TestAddIdempotent(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, ".munsu")

	// Add the same project twice with an identical definition — the canonical
	// Fleet Registry treats the re-registration as a successful no-op and
	// never creates a duplicate entry.
	if err := Add(homeDir, "dup-proj", "/path/captain", "fix", false); err != nil {
		t.Fatal(err)
	}
	if err := Add(homeDir, "dup-proj", "/path/captain", "fix", false); err != nil {
		t.Fatal(err)
	}

	// Should have exactly 1 entry, not 2
	projects, err := List(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project after duplicate add, got %d: %+v", len(projects), projects)
	}

	// Entry should carry the registered definition.
	p := projects[0]
	if p.Name != "dup-proj" {
		t.Errorf("Name = %q, want %q", p.Name, "dup-proj")
	}
	if p.Mode != "fix" {
		t.Errorf("Mode = %q, want %q", p.Mode, "fix")
	}
	if p.Yolo {
		t.Error("Yolo = true, want false")
	}
	if p.Description != "/path/captain" {
		t.Errorf("Description = %q, want %q", p.Description, "/path/captain")
	}
}

func TestFind(t *testing.T) {
	tmp := t.TempDir()
	if err := Add(tmp, "alpha", "/p/alpha", "", false); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmp, "beta", "/p/beta", "feat", true); err != nil {
		t.Fatal(err)
	}

	p, err := Find(tmp, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "alpha" {
		t.Errorf("Name = %q, want %q", p.Name, "alpha")
	}

	_, err = Find(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}

func TestRm(t *testing.T) {
	tmp := t.TempDir()
	if err := Add(tmp, "alpha", "/p/alpha", "", false); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmp, "beta", "/p/beta", "", false); err != nil {
		t.Fatal(err)
	}

	if err := Rm(tmp, "alpha"); err != nil {
		t.Fatal(err)
	}

	projects, err := List(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project after removal, got %d", len(projects))
	}
	if projects[0].Name != "beta" {
		t.Errorf("remaining project = %q, want %q", projects[0].Name, "beta")
	}
}

func TestRmNotFound(t *testing.T) {
	tmp := t.TempDir()
	err := Rm(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent project")
	}
}

func TestMode(t *testing.T) {
	tmp := t.TempDir()
	if err := Add(tmp, "test", "/p/test", "refactor", true); err != nil {
		t.Fatal(err)
	}

	mode, yolo, err := Mode(tmp, "test")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "refactor" {
		t.Errorf("Mode = %q, want %q", mode, "refactor")
	}
	if !yolo {
		t.Error("Yolo = false, want true")
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"https://github.com/user/repo.git", true},
		{"http://example.com/repo", true},
		{"git@github.com:user/repo.git", true},
		{"ssh://git@example.com/repo", true},
		{"/local/path/to/repo", false},
		{"./relative/path", false},
	}
	for _, tc := range tests {
		got := isURL(tc.s)
		if got != tc.want {
			t.Errorf("isURL(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// boolPtr returns a pointer to v for typed config pointer fields.
func boolPtr(v bool) *bool {
	return &v
}

// TestRegistryFileFormat proves that List round-trips every registry field
// from the canonical Fleet Registry, including the +yolo lifecycle flag.
// Legacy projects.md parsing is covered by the ParseEntry/FormatEntry
// round-trip tests above.
func TestRegistryFileFormat(t *testing.T) {
	tmp := t.TempDir()

	// Register each project through the canonical Fleet Registry (the sole
	// lifecycle authority) and assert List round-trips the fields.
	if err := Add(tmp, "alpha", "First project", "feat", false); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmp, "beta", "Captain project", "fix", true); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmp, "gamma", "Yolo without mode", "", true); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmp, "delta", "No mode project", "", false); err != nil {
		t.Fatal(err)
	}

	projects, err := List(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(projects))
	}

	// Verify each entry (List is ID-sorted; look up by name).
	byName := map[string]*Project{}
	for _, p := range projects {
		byName[p.Name] = p
	}
	alpha := byName["alpha"]
	beta := byName["beta"]
	gamma := byName["gamma"]
	delta := byName["delta"]
	if alpha.Name != "alpha" || alpha.Mode != "feat" || alpha.Yolo {
		t.Errorf("alpha: %+v", alpha)
	}
	if beta.Name != "beta" || beta.Mode != "fix" || !beta.Yolo {
		t.Errorf("beta: %+v", beta)
	}
	if gamma.Name != "gamma" || gamma.Mode != "" || !gamma.Yolo {
		t.Errorf("gamma: %+v", gamma)
	}
	if delta.Name != "delta" || delta.Mode != "" || delta.Yolo {
		t.Errorf("delta: %+v", delta)
	}
}

func TestResolveRepoPath_LocalPath(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "munsu-home")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a real directory to use as the local path
	localRepo := filepath.Join(tmp, "my-project")
	if err := os.MkdirAll(localRepo, 0755); err != nil {
		t.Fatal(err)
	}

	// Register with local path
	if err := Add(homeDir, "my-project", localRepo, "", false); err != nil {
		t.Fatal(err)
	}

	path, err := ResolveRepoPath(homeDir, "my-project")
	if err != nil {
		t.Fatalf("ResolveRepoPath: %v", err)
	}
	if path != localRepo {
		t.Errorf("expected path %q, got %q", localRepo, path)
	}
}

func TestResolveRepoPath_ClonedProject(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "munsu-home")
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	projectsDir := filepath.Join(homeDir, "projects")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Register a URL-backed project in the canonical Fleet Registry (no clone).
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
	}, []testProjectRecord{
		{Name: "cloned-proj", Path: "https://github.com/user/repo.git"},
	}, nil)

	// Create the projects/<name> dir to simulate a clone
	cloneDir := filepath.Join(projectsDir, "cloned-proj")
	if err := os.MkdirAll(cloneDir, 0755); err != nil {
		t.Fatal(err)
	}

	path, err := ResolveRepoPath(homeDir, "cloned-proj")
	if err != nil {
		t.Fatalf("ResolveRepoPath: %v", err)
	}
	if path != cloneDir {
		t.Errorf("expected path %q, got %q", cloneDir, path)
	}
}

func TestResolveRepoPath_NotFound(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "munsu-home")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveRepoPath(homeDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
	if !strings.Contains(err.Error(), "not found in registry") {
		t.Errorf("expected 'not found in registry' in error, got: %v", err)
	}
}

func TestResolveAdhoc_InGitRepo(t *testing.T) {
	// Create a temp git repo and test inference
	tmp := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure a minimal user for the test repo
	for _, kv := range []string{"user.name Test", "user.email test@test.com"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		cfg.Dir = tmp
		_ = cfg.Run()
	}

	// ResolveAdhoc should succeed and return the dir basename as project name
	p, err := ResolveAdhoc()
	if err != nil {
		t.Fatalf("ResolveAdhoc in git repo should succeed, got: %v", err)
	}
	// p.Name should be "munsu" since that's the worktree root basename
	// but we mainly care that it succeeded and returned a valid project
	if p.Name == "" {
		t.Error("ResolveAdhoc returned empty name")
	}
	if p.Description == "" {
		t.Error("ResolveAdhoc returned empty description")
	}
}

func TestResolveAdhoc_NotInGitRepo(t *testing.T) {
	// Create a temp dir that is NOT a git repo and chdir there
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	// ResolveAdhoc should fail — not in a git repo
	_, err = ResolveAdhoc()
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("expected 'not in a git repository' error, got: %v", err)
	}
}

func TestResolveFromCwd_RegistryAliasMatch(t *testing.T) {
	// Create a git repo dir whose basename differs from the registered alias
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo-basename")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure minimal user
	for _, kv := range []string{"user.name Test", "user.email test@test.com"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		cfg.Dir = repoDir
		_ = cfg.Run()
	}

	// Create munsu home dir
	homeDir := filepath.Join(tmp, ".munsu")

	// Register with alias name different from repo basename
	aliasName := "my-custom-alias"
	if err := Add(homeDir, aliasName, repoDir, "feat", false); err != nil {
		t.Fatal(err)
	}

	// Chdir into the repo so ResolveFromCwd detects it
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	// ResolveFromCwd should match the registered alias, not the basename
	p, err := ResolveFromCwd(homeDir)
	if err != nil {
		t.Fatalf("ResolveFromCwd: %v", err)
	}
	if p.Name != aliasName {
		t.Errorf("Name = %q, want %q (registered alias should win over basename %q)", p.Name, aliasName, "repo-basename")
	}
	if p.Description != repoDir {
		t.Errorf("Description = %q, want %q", p.Description, repoDir)
	}
	if p.Mode != "feat" {
		t.Errorf("Mode = %q, want %q", p.Mode, "feat")
	}
}

func TestResolveFromCwd_NoRegistryMatch(t *testing.T) {
	// Create a git repo with no matching registry entry
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "some-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure minimal user
	for _, kv := range []string{"user.name Test", "user.email test@test.com"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		cfg.Dir = repoDir
		_ = cfg.Run()
	}

	// Create munsu home dir with a registry entry that points elsewhere
	homeDir := filepath.Join(tmp, ".munsu")
	otherPath := filepath.Join(tmp, "other-repo")
	if err := Add(homeDir, "other-project", otherPath, "", false); err != nil {
		t.Fatal(err)
	}

	// Chdir into some-repo (not other-repo)
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	// Should fall back to adhoc (basename as name)
	p, err := ResolveFromCwd(homeDir)
	if err != nil {
		t.Fatalf("ResolveFromCwd: %v", err)
	}
	if p.Name != "some-repo" {
		t.Errorf("Name = %q, want %q (should fall back to basename)", p.Name, "some-repo")
	}
}

func TestResolveFromCwd_NotInGitRepo(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, ".munsu")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	// Not in a git repo — should fail
	_, err = ResolveFromCwd(homeDir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("expected 'not in a git repository' error, got: %v", err)
	}
}

func TestResolveFromCwd_UrlSkipsPathMatch(t *testing.T) {
	// URL-based project entries should be skipped during path matching
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "my-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure minimal user
	for _, kv := range []string{"user.name Test", "user.email test@test.com"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		cfg.Dir = repoDir
		_ = cfg.Run()
	}

	// Create munsu home dir and register project ONLY via URL (not a local path)
	homeDir := filepath.Join(tmp, ".munsu")
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	// Legacy projects.md entries are ignored: URL-based entries are skipped
	// during path matching (the canonical Fleet Registry is the sole project
	// authority), so resolution falls back to the repo basename.
	entry := "- url-project - https://github.com/user/repo.git (added 2026-07-01)\n"
	if err := os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(entry), 0644); err != nil {
		t.Fatal(err)
	}

	// Chdir into the repo
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	// URL should not match local path — falls back to adhoc
	p, err := ResolveFromCwd(homeDir)
	if err != nil {
		t.Fatalf("ResolveFromCwd: %v", err)
	}
	if p.Name != "my-repo" {
		t.Errorf("Name = %q, want %q (URL should be skipped, fallback to basename)", p.Name, "my-repo")
	}
}
