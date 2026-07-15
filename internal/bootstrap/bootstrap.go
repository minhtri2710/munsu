// Package bootstrap detects toolchain availability and runs setup sweeps.
package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result holds the full bootstrap diagnostic output.
type Result struct {
	LockAcquired  bool
	Diagnostics   []string
	MissingTools  []string
	ConfigDetails []string
}

// Run executes bootstrap diagnostics for the given munsu home.
// If lockHeld is true, mutating sweeps (fleet-sync) may run.
func Run(home string, lockHeld bool, installTools []string) (*Result, error) {
	res := &Result{
		LockAcquired: lockHeld,
	}

	// 1. Check required tools
	tools := []string{"git", "treehouse", "no-mistakes", "tasks-axi", "gh-axi", "gh", "tmux"}
	for _, tool := range tools {
		path, err := exec.LookPath(tool)
		if err != nil {
			res.MissingTools = append(res.MissingTools, tool)
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("MISSING: %s (install instructions vary)", tool))
		} else {
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("FOUND: %s at %s", tool, path))
		}
	}

	// 2. Check GitHub auth
	if ghAuth() {
		res.Diagnostics = append(res.Diagnostics, "GH_AUTH: authenticated")
	} else {
		res.Diagnostics = append(res.Diagnostics, "NEEDS_GH_AUTH: gh auth status failed (run gh auth login)")
	}

	// 3. Check crew-harness override
	harnessPath := filepath.Join(home, "config", "crew-harness")
	if data, err := os.ReadFile(harnessPath); err == nil {
		harness := strings.TrimSpace(string(data))
		if harness != "" && harness != "default" {
			res.ConfigDetails = append(res.ConfigDetails, fmt.Sprintf("CREW_HARNESS: %s", harness))
		}
	}

	// 4. Check crew-dispatch profiles
	dispatchPath := filepath.Join(home, "config", "crew-dispatch.json")
	if data, err := os.ReadFile(dispatchPath); err == nil {
		dispatch := strings.TrimSpace(string(data))
		lines := strings.Split(dispatch, "\n")
		ruleCount := 0
		for _, line := range lines {
			if strings.Contains(line, `"when"`) {
				ruleCount++
			}
		}
		res.ConfigDetails = append(res.ConfigDetails, fmt.Sprintf("CREW_DISPATCH: active (%d rules)", ruleCount))
	}

	// 5. Install tools if requested and lock held
	if lockHeld && len(installTools) > 0 {
		for _, tool := range installTools {
			if err := installTool(tool); err != nil {
				res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("INSTALL_FAILED: %s — %v", tool, err))
			} else {
				res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("INSTALLED: %s", tool))
			}
		}
	}

	return res, nil
}

// ghAuth checks whether gh is authenticated.
// Uses exit code only: gh auth status exits 0 when authenticated,
// regardless of whether it prints to stdout or stderr.
func ghAuth() bool {
	cmd := exec.Command("gh", "auth", "status")
	// Capture both stdout and stderr to suppress output
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// installTool runs the appropriate install command for a tool.
func installTool(tool string) error {
	switch tool {
	case "treehouse":
		cmd := exec.Command("go", "install", "github.com/elewis787/treehouse@latest")
		return cmd.Run()
	case "no-mistakes":
		cmd := exec.Command("go", "install", "github.com/minhtri2710/no-mistakes@latest")
		return cmd.Run()
	default:
		return fmt.Errorf("no automatic install for %s", tool)
	}
}
