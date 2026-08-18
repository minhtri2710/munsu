// Package integrate manages opt-in harness integration.
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// Role identifies which role the doctor scan targets.
type Role string

const (
	RoleGeneral Role = "general"
	RoleCaptain Role = "captain"
	RoleSoldier Role = "soldier"
)

// SubsystemStatus describes the integration state of one subsystem.
type SubsystemStatus string

const (
	StatusAbsent  SubsystemStatus = "absent"
	StatusStale   SubsystemStatus = "stale"
	StatusCurrent SubsystemStatus = "current"
)

// StatusEntry is one row in the role-aware doctor report.
type StatusEntry struct {
	Subsystem string          `json:"subsystem"`
	Status    SubsystemStatus `json:"status"`
	Detail    string          `json:"detail,omitempty"`
	RepairCmd string          `json:"repair_cmd,omitempty"`
}

func (e StatusEntry) String() string {
	s := fmt.Sprintf("  %s: %s", e.Subsystem, e.Status)
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	return s
}

// Report holds all subsystem status entries for a role.
type Report struct {
	Role    Role          `json:"role"`
	Entries []StatusEntry `json:"entries"`
}

// Doctor runs a role-aware scan of integration readiness.
// It is read-only and never mutates the home directory.
func Doctor(homeDir string, role Role) (*Report, error) {
	r := &Report{Role: role}
	switch role {
	case RoleGeneral:
		r.Entries = scanGeneral(homeDir)
	case RoleCaptain:
		r.Entries = scanCaptain(homeDir)
	case RoleSoldier:
		r.Entries = scanSoldier(homeDir)
	default:
		return nil, fmt.Errorf("unknown role %q", role)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// General-level scan
// ---------------------------------------------------------------------------

func scanGeneral(homeDir string) []StatusEntry {
	var entries []StatusEntry

	// Harness adapter — at least one known harness binary on PATH
	entries = append(entries, checkHarnessAdapter())

	// Session backend — tmux or herdr available
	entries = append(entries, checkSessionBackend())

	// GitHub auth
	entries = append(entries, checkGitHubAuth())

	// Go toolchain
	entries = append(entries, checkGoToolchain())

	return entries
}

func checkHarnessAdapter() StatusEntry {
	found := false
	var names []string
	for _, name := range harness.KnownHarnesses {
		if _, err := exec.LookPath(name); err == nil {
			found = true
			names = append(names, name)
		}
	}
	if found {
		return StatusEntry{
			Subsystem: "harness_adapter",
			Status:    StatusCurrent,
			Detail:    fmt.Sprintf("found: %s", strings.Join(names, ", ")),
		}
	}
	return StatusEntry{
		Subsystem: "harness_adapter",
		Status:    StatusAbsent,
		Detail:    "no coding harness binary found on PATH",
		RepairCmd: "install one of: pi, claude, codex, agy, grok, opencode",
	}
}

func checkSessionBackend() StatusEntry {
	// Check tmux
	if _, err := exec.LookPath("tmux"); err == nil {
		return StatusEntry{
			Subsystem: "session_backend",
			Status:    StatusCurrent,
			Detail:    "tmux found on PATH",
		}
	}
	// Check herdr env
	if os.Getenv("HERDR_ENV") != "" {
		return StatusEntry{
			Subsystem: "session_backend",
			Status:    StatusCurrent,
			Detail:    "HERDR_ENV active",
		}
	}
	return StatusEntry{
		Subsystem: "session_backend",
		Status:    StatusAbsent,
		Detail:    "no session backend (tmux or herdr) detected",
		RepairCmd: "install tmux: brew install tmux | apt install tmux",
	}
}

func checkGitHubAuth() StatusEntry {
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err == nil {
		return StatusEntry{
			Subsystem: "gh_auth",
			Status:    StatusCurrent,
			Detail:    "authenticated",
		}
	}
	return StatusEntry{
		Subsystem: "gh_auth",
		Status:    StatusAbsent,
		Detail:    "gh auth status failed",
		RepairCmd: "gh auth login",
	}
}

func checkGoToolchain() StatusEntry {
	if _, err := exec.LookPath("go"); err != nil {
		return StatusEntry{
			Subsystem: "go_toolchain",
			Status:    StatusAbsent,
			Detail:    "go not found on PATH",
			RepairCmd: "install Go from https://go.dev/dl/",
		}
	}
	// Check go version meets minimum
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return StatusEntry{
			Subsystem: "go_toolchain",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("go version check failed: %v", err),
		}
	}
	return StatusEntry{
		Subsystem: "go_toolchain",
		Status:    StatusCurrent,
		Detail:    strings.TrimSpace(string(out)),
	}
}

// ---------------------------------------------------------------------------
// Captain-level scan
// ---------------------------------------------------------------------------

func scanCaptain(homeDir string) []StatusEntry {
	var entries []StatusEntry

	// Watcher integration
	entries = append(entries, checkWatcherIntegration(homeDir))

	// Config directory structure
	entries = append(entries, checkCaptainConfig(homeDir))

	// Captain registry — captains registered in the canonical Fleet registry
	entries = append(entries, checkCaptainRegistry(homeDir))

	// Converge readiness — captain converge lock and marker state
	entries = append(entries, checkConvergeReadiness(homeDir))

	return entries
}

func checkWatcherIntegration(homeDir string) StatusEntry {
	beatPath := filepath.Join(homeDir, "state", "watch.beat")
	if _, err := os.Stat(beatPath); err != nil {
		return StatusEntry{
			Subsystem: "watcher_integration",
			Status:    StatusAbsent,
			Detail:    "watch.beat not found — watcher not running",
			RepairCmd: "munsu watch ensure",
		}
	}
	return StatusEntry{
		Subsystem: "watcher_integration",
		Status:    StatusCurrent,
		Detail:    "watch.beat present",
	}
}

func checkCaptainConfig(homeDir string) StatusEntry {
	configDir := filepath.Join(homeDir, "config")
	required := []string{"backend", "general-pane"}
	missing := make([]string, 0)
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(configDir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return StatusEntry{
			Subsystem: "captain_config",
			Status:    StatusAbsent,
			Detail:    fmt.Sprintf("missing config files: %s", strings.Join(missing, ", ")),
			RepairCmd: "munsu init --reconfigure or create config files manually",
		}
	}
	return StatusEntry{
		Subsystem: "captain_config",
		Status:    StatusCurrent,
		Detail:    "config directory complete",
	}
}

func checkCaptainRegistry(homeDir string) StatusEntry {
	// Fleet state lives in the canonical home. home.Init would create a home
	// for an empty or missing directory, so only query the Fleet registry
	// when the home is already initialized to keep doctor read-only.
	if _, err := os.Stat(filepath.Join(homeDir, home.IdentityFileName)); err != nil {
		return StatusEntry{
			Subsystem: "captain_homes",
			Status:    StatusAbsent,
			Detail:    "no initialized munsu home found — Fleet registry unavailable",
		}
	}
	captains, err := fleet.ListCaptains(homeDir)
	if err != nil {
		return StatusEntry{
			Subsystem: "captain_homes",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("Fleet registry read error: %v", err),
		}
	}
	if len(captains) == 0 {
		return StatusEntry{
			Subsystem: "captain_homes",
			Status:    StatusAbsent,
			Detail:    "no captains registered in Fleet registry",
			RepairCmd: "munsu captain seed <id> <home>",
		}
	}
	return StatusEntry{
		Subsystem: "captain_homes",
		Status:    StatusCurrent,
		Detail:    fmt.Sprintf("%d captain(s) registered", len(captains)),
	}
}

func checkConvergeReadiness(homeDir string) StatusEntry {
	lockPath := filepath.Join(homeDir, "state", fleet.ConvergeLockName)
	// Existence is not the signal: on Windows the lock file is permanent by
	// design (see fleet.acquireExclusiveLock), and on Unix a live converge
	// holds the file just as a stale crash artifact does. Probe the lock
	// instead — free means the file is stale but harmless, held means a
	// converge is genuinely running, which is not a fault.
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		return StatusEntry{
			Subsystem: "converge_readiness",
			Status:    StatusCurrent,
			Detail:    "no converge lock — ready for converge",
		}
	}
	held, err := fleet.LockHeld(lockPath)
	if err != nil {
		return StatusEntry{
			Subsystem: "converge_readiness",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("converge lock probe failed: %v", err),
		}
	}
	if held {
		return StatusEntry{
			Subsystem: "converge_readiness",
			Status:    StatusCurrent,
			Detail:    "converge in progress — lock held",
		}
	}
	return StatusEntry{
		Subsystem: "converge_readiness",
		Status:    StatusCurrent,
		Detail:    "lock file present but not held — stale artifact, harmless",
	}
}

// ---------------------------------------------------------------------------
// Soldier-level scan
// ---------------------------------------------------------------------------

func scanSoldier(homeDir string) []StatusEntry {
	var entries []StatusEntry

	// Worktree state
	entries = append(entries, checkWorktreeState(homeDir))

	// No-mistakes or direct-PR readiness
	entries = append(entries, checkPipelineReadiness(homeDir))

	// Brief existence
	entries = append(entries, checkSoldierBrief(homeDir))

	return entries
}

func checkWorktreeState(homeDir string) StatusEntry {
	cwd, err := os.Getwd()
	if err != nil {
		return StatusEntry{
			Subsystem: "worktree_state",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("cannot get cwd: %v", err),
		}
	}
	// Check if we are inside a git worktree
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	if out, err := cmd.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		return StatusEntry{
			Subsystem: "worktree_state",
			Status:    StatusCurrent,
			Detail:    "inside a git worktree",
		}
	}
	return StatusEntry{
		Subsystem: "worktree_state",
		Status:    StatusAbsent,
		Detail:    "not inside a git worktree",
		RepairCmd: "change to a project directory or run munsu worktree get",
	}
}

func checkPipelineReadiness(homeDir string) StatusEntry {
	// require-no-mistakes is a typed fleet base document field; the legacy flat
	// config file is never read. A malformed document is surfaced (fail
	// closed) rather than silently reported as direct-PR.
	base, err := config.LoadFleetBase(homeDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return StatusEntry{
			Subsystem: "pipeline_readiness",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("cannot read fleet base config: %v", err),
		}
	}
	if err == nil && base.Config.RequireNoMistakes != nil && *base.Config.RequireNoMistakes {
		if _, err := exec.LookPath("no-mistakes"); err != nil {
			return StatusEntry{
				Subsystem: "pipeline_readiness",
				Status:    StatusAbsent,
				Detail:    "require-no-mistakes set but no-mistakes not on PATH",
				RepairCmd: "go install github.com/kunchenguid/no-mistakes@latest",
			}
		}
		return StatusEntry{
			Subsystem: "pipeline_readiness",
			Status:    StatusCurrent,
			Detail:    "no-mistakes pipeline configured and available",
		}
	}
	return StatusEntry{
		Subsystem: "pipeline_readiness",
		Status:    StatusCurrent,
		Detail:    "direct-PR mode (no require-no-mistakes)",
	}
}

func checkSoldierBrief(homeDir string) StatusEntry {
	cwd, err := os.Getwd()
	if err != nil {
		return StatusEntry{
			Subsystem: "soldier_brief",
			Status:    StatusStale,
			Detail:    fmt.Sprintf("cannot get cwd: %v", err),
		}
	}
	briefPath := filepath.Join(cwd, ".soldier-brief.md")
	if _, err := os.Stat(briefPath); err != nil {
		return StatusEntry{
			Subsystem: "soldier_brief",
			Status:    StatusAbsent,
			Detail:    ".soldier-brief.md not found in cwd",
			RepairCmd: "create .soldier-brief.md or change to a soldier directory",
		}
	}
	return StatusEntry{
		Subsystem: "soldier_brief",
		Status:    StatusCurrent,
		Detail:    ".soldier-brief.md present",
	}
}
