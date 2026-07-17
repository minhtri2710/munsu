// Package bootstrap detects toolchain availability and runs setup sweeps.
package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)
// ToolStatus represents whether a tool was found during bootstrap.
type ToolStatus int

const (
	ToolFound ToolStatus = iota
	ToolMissing
	ToolInstallFailed
	ToolInstalled
)

// AuthStatus represents whether GitHub CLI authentication is valid.
type AuthStatus int

const (
	AuthAuthenticated AuthStatus = iota
	AuthFailed
)

// ToolDiagnostic describes the result of checking a single tool.
type ToolDiagnostic struct {
	Tool   string
	Status ToolStatus
	Path   string
	Err    error
}

// AuthDiagnostic describes the result of checking GitHub auth.
type AuthDiagnostic struct {
	Status AuthStatus
	Err    error
}

// ConfigDiagnostic describes a resolved configuration key-value pair.
type ConfigDiagnostic struct {
	Key    string
	Value  string
	Source string
}

// GCDiagnostic describes the result of garbage-collecting orphan data dirs.
type GCDiagnostic struct {
	Removed       int
	Dirs          []string
	SkippedReason string // set when GC was skipped (e.g., lock not held)
}

// Result holds the full bootstrap diagnostic output.
type Result struct {
	LockAcquired  bool
	Tools         []ToolDiagnostic
	Auth          *AuthDiagnostic
	Configs       []ConfigDiagnostic
	GC            *GCDiagnostic
	MissingTools  []string
}

// Run executes bootstrap diagnostics for the given munsu home.
// If lockHeld is true, mutating sweeps (fleet-sync) may run.
func Run(home string, lockHeld bool, installTools []string) (*Result, error) {
	res := &Result{
		LockAcquired: lockHeld,
	}

	// 1. Check required tools (uses shared ToolSpec registry)
	for _, spec := range checkedTools {
		path, err := exec.LookPath(spec.Name)
		if err != nil {
			res.Tools = append(res.Tools, ToolDiagnostic{Tool: spec.Name, Status: ToolMissing})
			res.MissingTools = append(res.MissingTools, spec.Name)
		} else {
			res.Tools = append(res.Tools, ToolDiagnostic{Tool: spec.Name, Status: ToolFound, Path: path})
		}
	}

	// 2. Check GitHub auth
	if ghAuth() {
		res.Auth = &AuthDiagnostic{Status: AuthAuthenticated}
	} else {
		res.Auth = &AuthDiagnostic{Status: AuthFailed}
	}

	// 3. Check crew-harness override
	harnessPath := filepath.Join(home, "config", "crew-harness")
	if data, err := os.ReadFile(harnessPath); err == nil {
		harness := strings.TrimSpace(string(data))
		if harness != "" && harness != "default" {
			res.Configs = append(res.Configs, ConfigDiagnostic{Key: "CREW_HARNESS", Value: harness})
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
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "CREW_DISPATCH", Value: fmt.Sprintf("active (%d rules)", ruleCount)})
	}

	// 4b. Check require-no-mistakes config
	requireNoMistakesPath := filepath.Join(home, "config", "require-no-mistakes")
	if _, err := os.Stat(requireNoMistakesPath); err == nil {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "REQUIRE_NO_MISTAKES", Value: "strict"})
	}

	// 5. Check session backend preference — distinguish config pin from runtime resolution
	configBackendPath := filepath.Join(home, "config", "backend")
	var configuredPin string
	if data, err := os.ReadFile(configBackendPath); err == nil {
		configuredPin = strings.TrimSpace(string(data))
	}

	// BACKEND_CONFIG: shows what is pinned/configured (auto or explicit name)
	configDisplay := "auto"
	if configuredPin != "" && configuredPin != "auto" {
		configDisplay = configuredPin
	}
	res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_CONFIG", Value: configDisplay})

	// BACKEND_RESOLVED: shows the currently resolved runtime backend and its source
	var resolved, source string
	if configuredPin != "" && configuredPin != "auto" {
		resolved = configuredPin
		source = "config pin"
	} else if tmuxEnv := os.Getenv("TMUX"); tmuxEnv != "" {
		resolved = "tmux"
		source = "active TMUX"
	} else if herdrEnv := os.Getenv("HERDR_ENV"); herdrEnv != "" {
		resolved = "herdr"
		source = "active HERDR_ENV"
	} else if _, err := exec.LookPath("tmux"); err == nil {
		resolved = "tmux"
		source = "cold-start"
	}

	if resolved != "" {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_RESOLVED", Value: resolved, Source: source})
	} else {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_RESOLVED", Value: "none", Source: "no backend available"})
	}

	// 6. Install tools if requested and lock held
	if lockHeld && len(installTools) > 0 {
		for _, tool := range installTools {
			if err := installTool(tool); err != nil {
				res.Tools = append(res.Tools, ToolDiagnostic{Tool: tool, Status: ToolInstallFailed, Err: err})
			} else {
				res.Tools = append(res.Tools, ToolDiagnostic{Tool: tool, Status: ToolInstalled})
			}
		}
	}

	// 7. GC orphan data dirs (only when lock held)
	if lockHeld {
		if cleaned := gcOrphanDataDirs(home); len(cleaned) > 0 {
			res.GC = &GCDiagnostic{Removed: len(cleaned), Dirs: cleaned}
		}
	} else {
		res.GC = &GCDiagnostic{SkippedReason: "session lock not held"}
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
		cmd := exec.Command("go", "install", "github.com/kunchenguid/treehouse@latest")
		return cmd.Run()
	case "no-mistakes":
		cmd := exec.Command("go", "install", "github.com/kunchenguid/no-mistakes@latest")
		return cmd.Run()
	default:
		return fmt.Errorf("no automatic install for %s", tool)
	}
}

// gcOrphanDataDirs scans data/<id>/ directories and removes orphan dirs
// that have no meta, status, brief, or report AND are older than the grace
// period (24h mtime). Returns the list of removed directory names.
func gcOrphanDataDirs(home string) []string {
	dataDir := filepath.Join(home, "data")
	stateDir := filepath.Join(home, "state")

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}

	const gracePeriod = 24 * time.Hour

	var cleaned []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()

		dirPath := filepath.Join(dataDir, id)

		// Check directory mtime — skip if younger than grace period
		info, err := os.Stat(dirPath)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < gracePeriod {
			continue
		}

		// Skip if corresponding meta file exists
		metaPath := filepath.Join(stateDir, id+".meta")
		if _, err := os.Stat(metaPath); err == nil {
			continue
		}

		// Skip if corresponding status file exists
		statusPath := filepath.Join(stateDir, id+".status")
		if _, err := os.Stat(statusPath); err == nil {
			continue
		}

		// Skip if brief.md exists in data dir
		briefPath := filepath.Join(dirPath, "brief.md")
		if _, err := os.Stat(briefPath); err == nil {
			continue
		}

		// Skip if report.md exists in data dir
		reportPath := filepath.Join(dirPath, "report.md")
		if _, err := os.Stat(reportPath); err == nil {
			continue
		}

		// Dir is truly orphan and past grace period — remove
		if err := os.RemoveAll(dirPath); err == nil {
			cleaned = append(cleaned, id)
		}
	}
	return cleaned
}
