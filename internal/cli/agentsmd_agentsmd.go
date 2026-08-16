// Package agentsmd provides project AGENTS.md management.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureResult describes what was done.
type EnsureResult struct {
	AGENTSMD      string
	CLAUDEMDSym   string
	SelfGovernSec bool
}

func Ensure(projectDir string, stage bool) (*EnsureResult, error) {
	res := &EnsureResult{}
	agentsPath := filepath.Join(projectDir, "AGENTS.md")

	existing, err := os.ReadFile(agentsPath)
	hasFile := err == nil

	var content string
	if !hasFile {
		base := filepath.Base(projectDir)
		content = fmt.Sprintf("# %s -- project agent memory\n\n", base)
		content += "This file is the conventions file for soldiers working on this project.\n\n"
		content += "## Build / test / lint\n\n" + probeBuildCommands(projectDir) + "\n\n"
		content += "Delivery mode: no-mistakes (push through the gate, never to origin directly).\n\n"
	} else {
		content = string(existing)
	}

	// Add self-governance section if missing
	selfGov := "## Maintaining this file\n\nKeep this file for knowledge useful to almost every future agent session in this project.\nDo not repeat what the codebase already shows; point to the authoritative file or command instead.\nPrefer rewriting or pruning existing entries over appending new ones.\nWhen updating this file, preserve this bar for all agents and keep entries concise.\n"

	if !hasFile {
		content += selfGov
		res.SelfGovernSec = true
	} else if !agentsMDContains(content, "## Maintaining this file") {
		content += "\n" + selfGov
		res.SelfGovernSec = true
	}

	// Only write if content actually changed
	changed := !hasFile || content != string(existing)
	if changed {
		if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
			return res, fmt.Errorf("writing AGENTS.md: %w", err)
		}
	}
	res.AGENTSMD = agentsPath

	// Create CLAUDE.md symlink
	if err := ensureSymlink(projectDir, agentsPath); err != nil {
		return res, err
	}

	// git add only when --stage flag is set
	if stage && changed {
		for _, p := range []string{agentsPath, claudePath(projectDir, agentsPath)} {
			// The index this stages into must come from the binding
			// (projectDir), never from the process cwd: without cmd.Dir a
			// munsu run started inside another checkout stages the paths
			// into THAT repository's index.
			cmd := exec.Command("git", "add", "--", p)
			cmd.Dir = projectDir
			cmd.Run()
		}
	}

	return res, nil
}

func claudePath(projectDir, agentsPath string) string {
	return filepath.Join(projectDir, "CLAUDE.md")
}

func ensureSymlink(projectDir, agentsPath string) error {
	claudePathStr := claudePath(projectDir, agentsPath)
	rel, err := filepath.Rel(projectDir, agentsPath)
	if err != nil {
		return err
	}

	if fi, err := os.Lstat(claudePathStr); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		os.Remove(claudePathStr)
	}

	if err := os.Symlink(rel, claudePathStr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating CLAUDE.md symlink: %w", err)
	}

	return nil
}

func agentsMDContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// probeBuildCommands probes a project directory and returns build/test commands.
func probeBuildCommands(projectDir string) string {
	// Go project
	if _, err := os.Stat(filepath.Join(projectDir, "go.mod")); err == nil {
		return "```sh\ngo build ./...\ngo test ./...\ngo vet ./...\n```"
	}

	// Node project
	pkgJSON := filepath.Join(projectDir, "package.json")
	if data, err := os.ReadFile(pkgJSON); err == nil {
		if strings.Contains(string(data), `"build"`) {
			return "```sh\nnpm test\nnpm run build\n```"
		}
		return "```sh\nnpm test\n```"
	}

	// Fallback: unknown project type
	return "# Add build/test commands here\n# Run `munsu doctor` for toolchain diagnostics\n"
}
