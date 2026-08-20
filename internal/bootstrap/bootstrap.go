// Package bootstrap detects toolchain availability and runs setup sweeps.
package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
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
	LockAcquired    bool
	RuntimeIdentity *RuntimeIdentity
	Tools           []ToolDiagnostic
	Auth            *AuthDiagnostic
	Configs         []ConfigDiagnostic
	GC              *GCDiagnostic
	MissingTools    []string
}

// Run executes bootstrap diagnostics for the given munsu home.
// If lockHeld is true, mutating sweeps (fleet-sync) may run.
func Run(home string, lockHeld bool, installTools []string) (*Result, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = home
	}
	runtimeIdentity := CollectRuntimeIdentity(home, cwd, "")
	return runWithRuntimeIdentity(home, lockHeld, installTools, &runtimeIdentity)
}

func runWithRuntimeIdentity(home string, lockHeld bool, installTools []string, runtimeIdentity *RuntimeIdentity) (*Result, error) {
	res := &Result{
		LockAcquired:    lockHeld,
		RuntimeIdentity: runtimeIdentity,
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

	// 3. Check soldier-harness override
	harnessPath := filepath.Join(home, "config", "soldier-harness")
	if data, err := os.ReadFile(harnessPath); err == nil {
		harness := strings.TrimSpace(string(data))
		if harness != "" && harness != "default" {
			res.Configs = append(res.Configs, ConfigDiagnostic{Key: "SOLDIER_HARNESS", Value: harness})
		}
	}

	// 4. Check soldier-dispatch profiles from fleet base document
	base, baseErr := config.LoadFleetBase(home)
	if baseErr == nil && len(base.Config.DispatchProfiles) > 0 {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "SOLDIER_DISPATCH", Value: fmt.Sprintf("active (%d rules)", len(base.Config.DispatchProfiles))})
	}

	// 4b. Check require-no-mistakes from the typed fleet base document (the
	// single operational authority; the legacy flat file is never read).
	if baseErr == nil && base.Config.RequireNoMistakes != nil && *base.Config.RequireNoMistakes {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "REQUIRE_NO_MISTAKES", Value: "strict"})
	}

	// 5. Check session backend preference — distinguish the legacy config file
	// pin from the persisted typed snapshot identity.
	configBackendPath := filepath.Join(home, "config", "backend")
	var configuredPin string
	if data, err := os.ReadFile(configBackendPath); err == nil {
		configuredPin = strings.TrimSpace(string(data))
	}

	// BACKEND_CONFIG: shows the legacy config file pin (auto or explicit name).
	configDisplay := "auto"
	if configuredPin != "" && configuredPin != "auto" {
		configDisplay = configuredPin
	}
	res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_CONFIG", Value: configDisplay})

	// BACKEND_RESOLVED: reports the persisted typed snapshot Backend (the
	// published config snapshot or the fleet base document's typed Backend) —
	// never an env/PATH probe. An absent identity is reported as typed
	// missing-input, never auto-selected.
	backendIdentity, err := fleet.ResolveGeneralHomeBackend(home)
	if err == nil && backendIdentity != "" {
		backendSource := "fleet base document"
		if config.PublishedSnapshotAvailable(home) {
			backendSource = "published snapshot"
		}
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_RESOLVED", Value: backendIdentity, Source: backendSource})
	} else {
		res.Configs = append(res.Configs, ConfigDiagnostic{Key: "BACKEND_RESOLVED", Value: "none", Source: "no persisted backend identity (set backend in the fleet base config)"})
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
func gcOrphanDataDirs(homeDir string) []string {
	dataDir := filepath.Join(homeDir, "data")

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
		metaPath, err := home.MetaFilePath(homeDir, id)
		if err != nil {
			continue
		}
		if _, err := os.Stat(metaPath); err == nil {
			continue
		}

		// Skip if corresponding status file exists
		statusPath, err := home.StatusFilePath(homeDir, id)
		if err != nil {
			continue
		}
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
