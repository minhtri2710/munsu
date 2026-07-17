package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeBuildCommands_GoProject(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmds := probeBuildCommands(tmp)
	if !strings.Contains(cmds, "go build ./...") {
		t.Errorf("expected 'go build ./...' in output, got: %s", cmds)
	}
	if !strings.Contains(cmds, "go test ./...") {
		t.Errorf("expected 'go test ./...' in output, got: %s", cmds)
	}
	if !strings.Contains(cmds, "go vet ./...") {
		t.Errorf("expected 'go vet ./...' in output, got: %s", cmds)
	}
}

func TestProbeBuildCommands_NodeProjectWithBuild(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{"scripts": {"build": "echo build", "test": "echo test"}}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}

	cmds := probeBuildCommands(tmp)
	if !strings.Contains(cmds, "npm test") {
		t.Errorf("expected 'npm test' in output, got: %s", cmds)
	}
	if !strings.Contains(cmds, "npm run build") {
		t.Errorf("expected 'npm run build' in output, got: %s", cmds)
	}
}

func TestProbeBuildCommands_NodeProjectNoBuild(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{"scripts": {"test": "echo test"}}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}

	cmds := probeBuildCommands(tmp)
	if !strings.Contains(cmds, "npm test") {
		t.Errorf("expected 'npm test' in output, got: %s", cmds)
	}
	if strings.Contains(cmds, "npm run build") {
		t.Errorf("unexpected 'npm run build' in output, got: %s", cmds)
	}
}

func TestProbeBuildCommands_Fallback(t *testing.T) {
	tmp := t.TempDir()

	cmds := probeBuildCommands(tmp)
	if !strings.Contains(cmds, "Add build/test commands here") {
		t.Errorf("expected fallback message in output, got: %s", cmds)
	}
	if !strings.Contains(cmds, "munsu doctor") {
		t.Errorf("expected 'munsu doctor' pointer in output, got: %s", cmds)
	}
}

func TestProbeBuildCommands_GoTakesPrecedence(t *testing.T) {
	tmp := t.TempDir()
	// Both go.mod and package.json present — Go should take precedence
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmds := probeBuildCommands(tmp)
	if !strings.Contains(cmds, "go build") {
		t.Errorf("expected Go commands when go.mod present, got: %s", cmds)
	}
}
