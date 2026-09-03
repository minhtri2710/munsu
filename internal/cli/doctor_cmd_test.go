package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestDoctor_ConfigReadFailureSurfaces(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)

	// Provide git and tmux stubs so the hard-required scan does not
	// short-circuit before the config-driven no-mistakes check runs.
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"git", "tmux"} {
		stub := filepath.Join(bin, name)
		testutil.WriteFakeExecutable(t, stub, "#!/bin/sh\nexit 0\n")
	}
	t.Setenv("PATH", bin)

	// Malformed base config: unreadable JSON must not be treated as
	// "not required" by the doctor.
	if err := os.MkdirAll(filepath.Join(tmp, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "config", "base.json"), []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected doctor to fail on unreadable base config, got nil:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "fleet base config") {
		t.Fatalf("expected config-read error in command seam, got: %v", err)
	}
}

func TestCheckInstructions_ValidCommands(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content := `# Manual

Run ` + "`" + `munsu doctor` + "`" + ` for diagnostics.
Use ` + "`" + `munsu fleet snapshot` + "`" + ` for fleet state.
Use ` + "`" + `munsu fleet bearings` + "`" + ` for a resume report.
Spawn with ` + "`" + `munsu spawn` + "`" + `.
`

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	realCommands := buildCommandIndex(root)

	mismatches, err := checkFileInstructions(agentsPath, realCommands)
	if err != nil {
		t.Fatalf("checkFileInstructions error: %v", err)
	}
	if mismatches > 0 {
		t.Errorf("expected 0 mismatches for valid commands, got %d", mismatches)
	}
}

func TestCheckInstructions_InvalidCommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content := `Use ` + "`" + `munsu spawn --timeout 300` + "`" + ` for spawning.
`

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	realCommands := buildCommandIndex(root)

	mismatches, err := checkFileInstructions(agentsPath, realCommands)
	if err != nil {
		t.Fatalf("checkFileInstructions error: %v", err)
	}
	if mismatches > 0 {
		t.Errorf("expected 0 mismatches (spawn exists, --timeout is just a flag), got %d", mismatches)
	}
}

func TestCheckInstructions_NonexistentCommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content := `Use ` + "`" + `munsu nonexistent-command` + "`" + ` for something.
`

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	realCommands := buildCommandIndex(root)

	mismatches, err := checkFileInstructions(agentsPath, realCommands)
	if err != nil {
		t.Fatalf("checkFileInstructions error: %v", err)
	}
	if mismatches != 1 {
		t.Errorf("expected 1 mismatch for nonexistent command, got %d", mismatches)
	}
}

func TestCheckInstructions_NonexistentSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content := `Use ` + "`" + `munsu fleet sync --all-projects` + "`" + ` to sync everything.
`

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	realCommands := buildCommandIndex(root)

	mismatches, err := checkFileInstructions(agentsPath, realCommands)
	if err != nil {
		t.Fatalf("checkFileInstructions error: %v", err)
	}
	if mismatches > 0 {
		t.Errorf("expected 0 mismatches (fleet sync exists, --all-projects is just a flag), got %d", mismatches)
	}
}

func TestCheckInstructions_FlagAliasInRef(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content := `Use ` + "`" + `munsu fleet snapshot --output json` + "`" + ` for fleet state.
`

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	realCommands := buildCommandIndex(root)

	mismatches, err := checkFileInstructions(agentsPath, realCommands)
	if err != nil {
		t.Fatalf("checkFileInstructions error: %v", err)
	}
	if mismatches > 0 {
		t.Errorf("expected 0 mismatches (fleet snapshot --output is a real flag), got %d", mismatches)
	}
}

func TestBuildCommandIndex_ContainsExpected(t *testing.T) {
	root := NewRootCommand()
	index := buildCommandIndex(root)

	expected := []string{
		"doctor",
		"fleet",
		"fleet snapshot",
		"fleet bearings",
		"fleet view",
		"fleet sync",
		"spawn",
		"task",
		"help",
	}

	for _, cmd := range expected {
		if !index[cmd] {
			t.Errorf("buildCommandIndex missing %q", cmd)
		}
	}
}
