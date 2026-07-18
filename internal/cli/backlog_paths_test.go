package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBacklogPathsCommandSeparatesDevelopmentAndRuntime(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte("backend = \"markdown\"\n\n[markdown]\npath = \"backlog.md\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), ".munsu")

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--home", home, "backlog", "paths"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedHome, err := filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"development: " + filepath.Join(resolvedRepo, "backlog.md"),
		"runtime: " + filepath.Join(resolvedHome, "data", "backlog.md"),
		"config: " + filepath.Join(resolvedRepo, ".tasks.toml"),
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("output missing %q:\n%s", expected, text)
		}
	}
}

func TestBacklogAddWithNonDefaultHomeUsesRuntimeFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte("backend = \"markdown\"\n\n[markdown]\npath = \"backlog.md\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "isolated-home")

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--home", home, "backlog", "add", "runtime-only", "isolated task"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	runtimeData, err := os.ReadFile(filepath.Join(home, "data", "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtimeData), "runtime-only") {
		t.Fatalf("runtime backlog missing task:\n%s", runtimeData)
	}
	if _, err := os.Stat(filepath.Join(repo, "backlog.md")); !os.IsNotExist(err) {
		t.Fatalf("development backlog should remain absent, stat error = %v", err)
	}
}
