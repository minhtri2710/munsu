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
	configDir := filepath.Join(home, "config")
	dispatchPath := filepath.Join(configDir, "crew-dispatch.json")
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

	// 5. Check session backend preference
	if data, err := os.ReadFile(filepath.Join(home, "config", "backend")); err == nil {
		backend := strings.TrimSpace(string(data))
		if backend != "" && backend != "auto" {
			res.ConfigDetails = append(res.ConfigDetails, fmt.Sprintf("BACKEND: %s (config)", backend))
		}
	} else if herdrEnv := os.Getenv("HERDR_ENV"); herdrEnv != "" {
		res.ConfigDetails = append(res.ConfigDetails, "BACKEND: herdr (HERDR_ENV)")
	} else {
		res.ConfigDetails = append(res.ConfigDetails, "BACKEND: auto (default)")
	}

	// 6. Install tools if requested and lock held
	if lockHeld && len(installTools) > 0 {
		for _, tool := range installTools {
			if err := installTool(tool); err != nil {
				res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("INSTALL_FAILED: %s — %v", tool, err))
			} else {
				res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("INSTALLED: %s", tool))
			}
		}
	}

	// 7. GC orphan data dirs (only when lock held)
	if lockHeld {
		if cleaned := gcOrphanDataDirs(home); len(cleaned) > 0 {
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("GC: removed %d orphan data dir(s): %v", len(cleaned), cleaned))
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

// gcOrphanDataDirs scans data/<id>/ directories and removes those that have no
// corresponding state/<id>.meta file and only contain a brief.md (orphan briefs
// created by `munsu brief --force` that were never spawned).
// Returns the list of removed directory names.
func gcOrphanDataDirs(home string) []string {
	dataDir := filepath.Join(home, "data")
	stateDir := filepath.Join(home, "state")

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}

	var cleaned []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()

		// Skip if corresponding meta file exists
		metaPath := filepath.Join(stateDir, id+".meta")
		if _, err := os.Stat(metaPath); err == nil {
			continue
		}

		// Skip if dir has more than just brief.md
		dataEntries, err := os.ReadDir(filepath.Join(dataDir, id))
		if err != nil {
			continue
		}
		// Only remove if the only file is brief.md
		hasOnlyBrief := len(dataEntries) == 1 && !dataEntries[0].IsDir() && dataEntries[0].Name() == "brief.md"
		// Also remove empty dirs
		hasNoFiles := len(dataEntries) == 0

		if hasOnlyBrief || hasNoFiles {
			if err := os.RemoveAll(filepath.Join(dataDir, id)); err == nil {
				cleaned = append(cleaned, id)
			}
		}
	}
	return cleaned
}
