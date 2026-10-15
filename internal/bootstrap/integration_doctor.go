package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

	entries = append(entries, checkSessionBackend(homeDir))

	// GitHub auth
	entries = append(entries, checkGitHubAuth())

	// Go toolchain
	entries = append(entries, checkGoToolchain())

	// Per project: the refusal a soldier launch would hit, probed without spawning
	entries = append(entries, checkProjectReadiness(homeDir)...)

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

func checkSessionBackend(homeDir string) StatusEntry {
	backend, err := fleet.ResolveGeneralHomeBackend(homeDir)
	if err != nil {
		return StatusEntry{
			Subsystem: "session_backend",
			Status:    StatusAbsent,
			Detail:    fmt.Sprintf("no session backend configured: %v", err),
			RepairCmd: "munsu config set backend herdr|tmux",
		}
	}
	if _, err := exec.LookPath(backend); err != nil {
		return StatusEntry{
			Subsystem: "session_backend",
			Status:    StatusAbsent,
			Detail:    fmt.Sprintf("backend %s configured, %s not found on PATH", backend, backend),
			RepairCmd: FixCommand(backend),
		}
	}
	return StatusEntry{
		Subsystem: "session_backend",
		Status:    StatusCurrent,
		Detail:    fmt.Sprintf("backend %s configured, found on PATH", backend),
	}
}

func checkGitHubAuth() StatusEntry {
	if _, err := runCapabilityCommand("gh", []string{"auth", "status"}, "", capabilityProbeTimeout); err == nil {
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

// checkProjectReadiness predicts, for each registered project, whether a
// General-direct soldier launch would pass spawn's delivery and harness
// preflights, and whether each harness a dispatch profile can select would
// pass. It probes and reports; it refuses nothing.
func checkProjectReadiness(homeDir string) []StatusEntry {
	// Same guard as checkCaptainRegistry: opening the registry creates a home
	// for a missing directory, and doctor must stay read-only.
	if _, err := os.Stat(filepath.Join(homeDir, home.IdentityFileName)); err != nil {
		return []StatusEntry{{Subsystem: "project_readiness", Status: StatusCurrent, Detail: "no projects configured"}}
	}
	projects, err := fleet.List(homeDir)
	if err != nil {
		return []StatusEntry{{Subsystem: "project_readiness", Status: StatusStale, Detail: fmt.Sprintf("cannot read project registry: %v", err)}}
	}
	if len(projects) == 0 {
		return []StatusEntry{{Subsystem: "project_readiness", Status: StatusCurrent, Detail: "no projects configured"}}
	}
	var entries []StatusEntry
	for _, project := range projects {
		subsystem := "project " + project.Name
		pr, err := fleet.ProbeProjectReadiness(homeDir, project.Name)
		if err != nil {
			entries = append(entries, StatusEntry{Subsystem: subsystem, Status: StatusStale, Detail: fmt.Sprintf("cannot resolve project: %v", err)})
			continue
		}
		entries = append(entries, projectDefaultReadinessEntry(subsystem, pr))
		for _, profile := range pr.Profiles {
			profileSubsystem := subsystem + " harness " + profile.Harness
			if profile.Err == nil {
				entries = append(entries, StatusEntry{Subsystem: profileSubsystem, Status: StatusCurrent,
					Detail: "profile harness would pass"})
				continue
			}
			entries = append(entries, StatusEntry{Subsystem: profileSubsystem, Status: StatusAbsent,
				Detail: fmt.Sprintf("profile harness would be refused: %v", profile.Err)})
		}
	}
	return entries
}

func projectDefaultReadinessEntry(subsystem string, pr fleet.ProjectReadiness) StatusEntry {
	if pr.Err == nil {
		return StatusEntry{Subsystem: subsystem, Status: StatusCurrent,
			Detail: fmt.Sprintf("launch would pass: delivery %s, harness %s", pr.Mode, pr.Harness)}
	}
	return StatusEntry{Subsystem: subsystem, Status: StatusAbsent,
		Detail: fmt.Sprintf("launch would be refused: %v", pr.Err)}
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
	// Existence is not the signal: a live converge holds the file just as a
	// stale crash artifact does. Probe the lock
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
