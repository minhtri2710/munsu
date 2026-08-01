// Package harness detects the running agent harness and resolves soldier/captain
// harness assignments from configuration.
package harness

import (
	"bufio"
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

// lookupConfig reports a config key's value when it is set to a concrete value.
// Empty values and the "default" sentinel are treated as not set.
func lookupConfig(homeDir, key string) (string, bool) {
	v, err := config.Get(homeDir, key)
	if err == nil && v != "" && v != "default" {
		return v, true
	}
	return "", false
}

// Soldier resolves the soldier harness following the fallback chain:
//
//  1. Default harness from config/soldier-dispatch.json
//  2. config/soldier-harness file value
//  3. Detected harness from Detect()
//
// The config/soldier-harness value "default" is treated as unset.
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

	// 3. Try config/soldier-harness
	if v, ok := lookupConfig(homeDir, "soldier-harness"); ok {
		if err := ValidateHarness(v); err != nil {
			return "", err
		}
		return v, nil
	}

	// 4. Fall back to detected harness
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

// Captain resolves the general harness following:
//
//  1. config/captain-harness file value (first token)
//  2. config/soldier-harness file value
//  3. Detected harness from Detect()
//
// A value of "default" in any pin file is treated as unset.
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

// CaptainProfileFromHome resolves harness + optional model/effort for captain launch.
//
// Precedence for harness (first token):
//  1. config/captain-harness (multi-token: "<harness> [<model>] [<effort>]")
//  2. config/soldier-harness (bare harness only; model/effort ignored)
//
// Model/effort tokens:
//  1. Tokens 2–3 on the winning multi-token captain-harness pin
//  2. Else config/model (single-key model pin; effort not available here)
//
// Model/effort come only from the captain pin line, not from soldier-harness
// (which stays a bare adapter name).
func CaptainProfileFromHome(homeDir string) (CaptainProfile, error) {
	// 1. captain-harness multi-token
	if raw, ok := lookupConfig(homeDir, "captain-harness"); ok {
		p := ParseHarnessLine(raw)
		if p.Harness != "" {
			if err := ValidateHarness(p.Harness); err != nil {
				return CaptainProfile{}, err
			}
			return fillCaptainModelFallback(homeDir, p), nil
		}
	}

	// 2. soldier-harness bare name only
	if v, ok := lookupConfig(homeDir, "soldier-harness"); ok {
		// Only first token counts; ignore accidental model tokens on soldier pin.
		p := ParseHarnessLine(v)
		if p.Harness != "" {
			if err := ValidateHarness(p.Harness); err != nil {
				return CaptainProfile{}, err
			}
			// soldier pin never supplies model/effort — only config/model fallback.
			return fillCaptainModelFallback(homeDir, CaptainProfile{Harness: p.Harness}), nil
		}
	}

	// No harness pin — still surface config/model for callers that only need model.
	return fillCaptainModelFallback(homeDir, CaptainProfile{}), nil
}

// fillCaptainModelFallback applies config/model when the profile has no model token.
func fillCaptainModelFallback(homeDir string, p CaptainProfile) CaptainProfile {
	if p.Model != "" {
		return p
	}
	if m, err := config.Get(homeDir, "model"); err == nil {
		m = strings.TrimSpace(m)
		if m != "" && m != "default" {
			// config/model may itself be multi-token historically; take first field only.
			fields := strings.Fields(m)
			if len(fields) > 0 {
				p.Model = fields[0]
			}
		}
	}
	return p
}
