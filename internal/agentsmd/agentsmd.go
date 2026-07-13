// Package agentsmd provides project AGENTS.md management.
package agentsmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureResult describes what was done.
type EnsureResult struct {
	AGENTSMD      string
	CLAUDEMDSym   string
	SelfGovernSec bool
}

// Ensure creates or updates a project's AGENTS.md and CLAUDE.md symlink.
func Ensure(projectDir string) (*EnsureResult, error) {
	res := &EnsureResult{}
	agentsPath := filepath.Join(projectDir, "AGENTS.md")

	existing, err := os.ReadFile(agentsPath)
	hasFile := err == nil

	var content string
	if !hasFile {
		base := filepath.Base(projectDir)
		content = fmt.Sprintf("# %s -- project agent memory\n\n", base)
		content += "This file is the conventions file for crewmates working on this project.\n\n"
		content += "## Build / test / lint\n\n```sh\n# TODO: add build commands\n```\n\n"
		content += "Delivery mode: no-mistakes (push through the gate, never to origin directly).\n\n"
	} else {
		content = string(existing)
	}

	// Add self-governance section if missing
	selfGov := "## Maintaining this file\n\nKeep this file for knowledge useful to almost every future agent session in this project.\nDo not repeat what the codebase already shows; point to the authoritative file or command instead.\nPrefer rewriting or pruning existing entries over appending new ones.\nWhen updating this file, preserve this bar for all agents and keep entries concise.\n"

	if !hasFile {
		content += selfGov
		res.SelfGovernSec = true
	} else if !contains(content, "## Maintaining this file") {
		content += "\n" + selfGov
		res.SelfGovernSec = true
	}

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		return res, fmt.Errorf("writing AGENTS.md: %w", err)
	}
	res.AGENTSMD = agentsPath

	// Create CLAUDE.md symlink
	claudePath := filepath.Join(projectDir, "CLAUDE.md")
	rel, err := filepath.Rel(projectDir, agentsPath)
	if err != nil {
		return res, err
	}

	if fi, err := os.Lstat(claudePath); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		os.Remove(claudePath)
	}

	if err := os.Symlink(rel, claudePath); err != nil && !os.IsExist(err) {
		return res, fmt.Errorf("creating CLAUDE.md symlink: %w", err)
	}
	res.CLAUDEMDSym = claudePath

	for _, p := range []string{agentsPath, claudePath} {
		exec.Command("git", "add", p).Run()
	}

	return res, nil
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
