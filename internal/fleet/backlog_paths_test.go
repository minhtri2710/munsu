package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsSeparatesDevelopmentAndRuntimeBacklogs(t *testing.T) {
	repo := t.TempDir()
	nested := filepath.Join(repo, "internal", "pkg")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	config := "backend = \"markdown\"\n\n[markdown]\npath = \"md\"\narchive = \".tasks/done-archive.md\"\ndone_keep = 10\n"
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(t.TempDir(), ".munsu")
	paths, err := ResolvePaths(nested, home)
	if err != nil {
		t.Fatal(err)
	}

	if paths.Development != filepath.Join(repo, "md") {
		t.Errorf("development backlog = %q, want %q", paths.Development, filepath.Join(repo, "md"))
	}
	if paths.Runtime != filepath.Join(home, "data", "md") {
		t.Errorf("runtime backlog = %q, want %q", paths.Runtime, filepath.Join(home, "data", "md"))
	}
	if paths.Config != filepath.Join(repo, ".tasks.toml") {
		t.Errorf("config = %q, want %q", paths.Config, filepath.Join(repo, ".tasks.toml"))
	}
}

func TestResolvePathsDefaultsDevelopmentBacklogToWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	paths, err := ResolvePaths(cwd, home)
	if err != nil {
		t.Fatal(err)
	}

	if paths.Development != filepath.Join(cwd, "md") {
		t.Errorf("development backlog = %q, want %q", paths.Development, filepath.Join(cwd, "md"))
	}
	if paths.Config != "" {
		t.Errorf("config = %q, want empty", paths.Config)
	}
}

func TestResolvePathsConfigWithoutMarkdownPathFallsBackToRepoBacklog(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte("backend = \"sqlite\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	paths, err := ResolvePaths(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if paths.Development != filepath.Join(repo, "md") {
		t.Errorf("development backlog = %q, want %q", paths.Development, filepath.Join(repo, "md"))
	}
	if paths.Config != filepath.Join(repo, ".tasks.toml") {
		t.Errorf("config = %q, want %q", paths.Config, filepath.Join(repo, ".tasks.toml"))
	}
}

func TestResolvePathsRejectsInvalidMarkdownPath(t *testing.T) {
	repo := t.TempDir()
	config := "backend = \"markdown\"\n\n[markdown]\npath = not-quoted\n"
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolvePaths(repo, t.TempDir()); err == nil {
		t.Fatal("expected invalid markdown.path error")
	}
}
