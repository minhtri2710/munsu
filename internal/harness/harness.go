// Package harness detects the running agent harness and resolves soldier/captain
// harness assignments from configuration.
package harness

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

// Known harness identifiers.
const (
	Claude   = "claude"
	Codex    = "codex"
	Opencode = "opencode"
	Pi       = "pi"
	Grok     = "grok"
	Agy      = "agy"
)

// KnownHarnesses lists all supported harness names.
var KnownHarnesses = []string{Claude, Codex, Opencode, Pi, Grok, Agy}

// IsKnownHarness reports whether name is a supported harness.
func IsKnownHarness(name string) bool {
	for _, h := range KnownHarnesses {
		if h == name {
			return true
		}
	}
	return false
}

// CanonicalHarness returns the canonical registry identifier for name,
// normalizing whitespace and case. "default" is treated as unset, matching
// ValidateHarness. Returns ok=false for unknown names, including aliases or
// prefixes that are not exact registry identifiers, so they can never match an
// allowlist entry.
func CanonicalHarness(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "default" {
		return "", false
	}
	if IsKnownHarness(n) {
		return n, true
	}
	return "", false
}

// ValidateHarness returns an error if name is not empty and not "default"
// but is not in KnownHarnesses. Empty or "default" are treated as unset.
func ValidateHarness(name string) error {
	if name == "" || name == "default" {
		return nil
	}
	if !IsKnownHarness(name) {
		return fmt.Errorf("unknown harness %q: must be one of %v (or empty/default)", name, KnownHarnesses)
	}
	return nil
}

// Detect identifies the running agent harness.
//
// Detection order:
//  1. Environment variable markers (fast path)
//  2. Process ancestry detection (fallback)
func Detect() (string, error) {
	if h := detectFromEnv(); h != "" {
		return h, nil
	}
	return detectFromProcess()
}

// detectFromEnv checks well-known environment variable markers.
func detectFromEnv() string {
	return detectEnvFromAdapter()
}

// detectFromProcess walks the process tree upward looking for a known agent process.
func detectFromProcess() (string, error) {
	pid := os.Getppid()
	seen := make(map[int]bool)
	for i := 0; i < 10 && pid > 0; i++ {
		if seen[pid] {
			break
		}
		seen[pid] = true

		name, ppid, err := processInfo(pid)
		if err != nil {
			return "", fmt.Errorf("process ancestry: %w", err)
		}
		if h := matchProcessName(name); h != "" {
			return h, nil
		}
		pid = ppid
	}
	return "", fmt.Errorf("harness: unable to detect agent harness from environment or process ancestry")
}

// processInfo returns the process name and parent PID for the given PID.
func processInfo(pid int) (name string, ppid int, err error) {
	// Try /proc first (Linux)
	if data, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm")); e == nil {
		comm := strings.TrimSpace(string(data))

		// Also read status for PPid
		ppidData, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if e == nil {
			scanner := bufio.NewScanner(strings.NewReader(string(ppidData)))
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "PPid:") {
					ppidStr := strings.TrimSpace(strings.TrimPrefix(line, "PPid:"))
					if p, parseErr := strconv.Atoi(ppidStr); parseErr == nil {
						return comm, p, nil
					}
				}
			}
		}
		return comm, 0, nil
	}

	// Fallback to `ps` (macOS/BSD)
	cmd := exec.Command("ps", "-o", "comm=,ppid=", "-p", strconv.Itoa(pid))
	out, e := cmd.Output()
	if e != nil {
		return "", 0, fmt.Errorf("ps -p %d: %w", pid, e)
	}
	parts := strings.Fields(string(out))
	if len(parts) >= 2 {
		comm := parts[0]
		if p, parseErr := strconv.Atoi(parts[1]); parseErr == nil {
			return comm, p, nil
		}
	}
	return "", 0, fmt.Errorf("unexpected ps output for pid %d: %q", pid, string(out))
}

// matchProcessName checks if a process name corresponds to a known harness.
// It uses the adapter registry for all verified harnesses.
func matchProcessName(name string) string {
	return matchProcessNameFromAdapter(name)
}

// Template describes the CLI flags and defaults for spawning a harness.
type Template struct {
	ModelFlag     string
	EffortFlag    string
	DefaultModel  string
	DefaultEffort string
	ExtraArgs     []string // Additional CLI arguments for launch (autonomy, interactive, etc.)
}

// Templates maps each known harness to its launch template (CLI flag conventions).
// The adapter registry (Adapters) is the authoritative source; Templates is a
var Templates = map[string]Template{
	Claude: {
		ModelFlag:    "--model",
		DefaultModel: "claude-sonnet-4-20250515",
	},
	Codex: {
		ModelFlag:     "--model",
		EffortFlag:    "--effort",
		DefaultModel:  "gpt-5.2-codex",
		DefaultEffort: "80",
	},
	Opencode: {
		ModelFlag:     "--model",
		EffortFlag:    "--effort",
		DefaultModel:  "gpt-5.2-codex",
		DefaultEffort: "80",
	},
	Pi: {
		ModelFlag:  "--model",
		EffortFlag: "--thinking",
	},
	Grok: {
		ModelFlag:  "--model",
		EffortFlag: "--reasoning-effort",
	},
	Agy: {
		ModelFlag: "--model",
		// DefaultModel omitted — let agy use its runtime default (Claude Sonnet 4.6)
		ExtraArgs: []string{"--dangerously-skip-permissions"},
	},
}

// ErrNoSoldierHarnessInSnapshot is returned by ResolveSoldierFromSnapshot when
// the resolved project snapshot carries no soldier harness identity.
var ErrNoSoldierHarnessInSnapshot = errors.New("no soldier harness identity resolved from snapshot")

// ResolveSoldierFromSnapshot resolves the soldier harness from a resolved
// project snapshot only. It fails closed when the snapshot carries no soldier
// harness identity, and rejects unknown harness names via ValidateHarness.
// Snapshot-only: no flat-file lookup, no Detect, no env/PATH input.
// Operations consume this function; Soldier remains the diagnostics/legacy
// surface with its fallback chain.
func ResolveSoldierFromSnapshot(cfg config.ResolvedProjectConfig) (string, error) {
	if cfg.SoldierHarness == "" {
		return "", fmt.Errorf("%w: project %q", ErrNoSoldierHarnessInSnapshot, cfg.Project)
	}
	if err := ValidateHarness(cfg.SoldierHarness); err != nil {
		return "", fmt.Errorf("snapshot soldier harness: %w", err)
	}
	return cfg.SoldierHarness, nil
}

// Soldier resolves the soldier harness following the legacy fallback chain
// (diagnostics surface):
//
//  1. Published snapshot (captain context)
//  2. Fleet base document (general context)
//  3. Detected harness from Detect()
//
// Operations resolve the soldier harness from the snapshot only via
// ResolveSoldierFromSnapshot; Soldier remains for diagnostics.
func Soldier(homeDir string) (string, error) {
	// 1. Try published snapshot (captain context)
	snapshot, err := config.LoadPublishedSnapshot(homeDir)
	if err == nil {
		cfg := snapshot.Config()
		if cfg.SoldierHarness != "" {
			if err := ValidateHarness(cfg.SoldierHarness); err != nil {
				return "", fmt.Errorf("published snapshot soldier harness: %w", err)
			}
			return cfg.SoldierHarness, nil
		}
	}

	// 2. Try fleet base document (general context)
	base, err := config.LoadFleetBase(homeDir)
	if err == nil && base.Config.SoldierHarness != "" {
		if err := ValidateHarness(base.Config.SoldierHarness); err != nil {
			return "", fmt.Errorf("fleet base soldier harness: %w", err)
		}
		return base.Config.SoldierHarness, nil
	}

	// 3. Fall back to detected harness
	return Detect()
}

// CaptainProfile is the resolved captain launch profile: harness plus optional model/effort tokens.
type CaptainProfile struct {
	Harness string
	Model   string
	Effort  string
}

// ParseHarnessLine parses multi-token harness config lines:
//
//	"<harness> [<model>] [<effort>]"
//
// Blank/comment lines are ignored by callers; this parses one concrete line.
// The harness token "default" is treated as unset (empty Harness).
func ParseHarnessLine(line string) CaptainProfile {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return CaptainProfile{}
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return CaptainProfile{}
	}
	p := CaptainProfile{Harness: fields[0]}
	if p.Harness == "default" {
		p.Harness = ""
		return p
	}
	if len(fields) >= 2 {
		p.Model = fields[1]
	}
	if len(fields) >= 3 {
		p.Effort = fields[2]
	}
	return p
}

// ErrNoCaptainHarnessInSnapshot is returned by ResolveCaptainFromSnapshot when
// the resolved project snapshot carries no captain profile harness identity.
var ErrNoCaptainHarnessInSnapshot = errors.New("no captain harness identity resolved from snapshot")

// ResolveCaptainFromSnapshot resolves the captain harness from a resolved
// project snapshot only. It fails closed when the snapshot carries no captain
// harness identity, and rejects unknown harness names via ValidateHarness.
// Snapshot-only: no flat-file lookup, no Detect, no env/PATH input.
// Operations consume this function; Captain remains the diagnostics/legacy
// surface with its fallback chain.
func ResolveCaptainFromSnapshot(cfg config.ResolvedProjectConfig) (string, error) {
	if cfg.CaptainProfile.Harness == "" {
		return "", fmt.Errorf("%w: project %q", ErrNoCaptainHarnessInSnapshot, cfg.Project)
	}
	if err := ValidateHarness(cfg.CaptainProfile.Harness); err != nil {
		return "", fmt.Errorf("snapshot captain harness: %w", err)
	}
	return cfg.CaptainProfile.Harness, nil
}

// Captain resolves the general harness following:
//
//  1. base.CaptainProfile.Harness (config/base.json)
//  2. base.Config.SoldierHarness (config/base.json)
//  3. Detected harness from Detect()
//
// For model/effort tokens use CaptainProfileFromHome.
func Captain(homeDir string) (string, error) {
	prof, err := CaptainProfileFromHome(homeDir)
	if err != nil {
		return "", err
	}
	if prof.Harness != "" {
		return prof.Harness, nil
	}
	return Detect()
}

// CaptainProfileFromHome resolves harness + optional model/effort for captain
// launch from the fleet base document (config/base.json), the single authoring
// authority. It runs in the General home; a captain home without a base
// document degrades to an empty profile (caller falls back to Detect), and a
// malformed/invalid document fails closed.
//
// Precedence for harness:
//  1. base.CaptainProfile.Harness (from `config set captain-harness`)
//  2. base.Config.SoldierHarness (bare harness fallback; model/effort ignored)
//
// Model:
//  1. base.CaptainProfile.Model (token 2 of the captain pin)
//  2. Else base.Config.Model
//
// Effort comes only from the captain profile, never from the soldier harness.
func CaptainProfileFromHome(homeDir string) (CaptainProfile, error) {
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CaptainProfile{}, nil
		}
		return CaptainProfile{}, err
	}

	p := CaptainProfile{
		Harness: base.CaptainProfile.Harness,
		Model:   base.CaptainProfile.Model,
		Effort:  base.CaptainProfile.Effort,
	}
	if p.Harness == "" && base.Config.SoldierHarness != "" {
		// Soldier-harness fallback supplies only the bare adapter name.
		p = CaptainProfile{Harness: base.Config.SoldierHarness}
	}
	if p.Harness != "" {
		if err := ValidateHarness(p.Harness); err != nil {
			return CaptainProfile{}, err
		}
	}
	if p.Model == "" {
		p.Model = base.Config.Model
	}
	return p, nil
}
