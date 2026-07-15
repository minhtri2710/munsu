package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestRoundTrip(t *testing.T) {
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

func TestRegistryFileFormat(t *testing.T) {
	tmp := t.TempDir()
	regPath := RegistryPath(tmp)

	// Write entries directly to simulate real file format
	entries := []string{
		"- alpha feat - First project (added 2026-01-01)",
		"- beta fix +yolo - Second project (added 2026-03-15)",
		"- gamma +yolo - Yolo without mode (added 2026-06-01)",
		"- delta - No mode project (added 2026-07-01)",
	}
	if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte(strings.Join(entries, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	projects, err := List(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(projects))
	}

	// Verify each entry
	if projects[0].Name != "alpha" || projects[0].Mode != "feat" || projects[0].Yolo {
		t.Errorf("alpha: %+v", projects[0])
	}
	if projects[1].Name != "beta" || projects[1].Mode != "fix" || !projects[1].Yolo {
		t.Errorf("beta: %+v", projects[1])
	}
	if projects[2].Name != "gamma" || projects[2].Mode != "" || !projects[2].Yolo {
		t.Errorf("gamma: %+v", projects[2])
	}
	if projects[3].Name != "delta" || projects[3].Mode != "" || projects[3].Yolo {
		t.Errorf("delta: %+v", projects[3])
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
	projectsDir := filepath.Join(homeDir, "projects")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write registry entry directly (avoid actual clone)
	regPath := RegistryPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
		t.Fatal(err)
	}
	entry := "- cloned-proj - https://github.com/user/repo.git (added 2026-07-01)\n"
	if err := os.WriteFile(regPath, []byte(entry), 0644); err != nil {
		t.Fatal(err)
	}

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
