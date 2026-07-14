// Package harness detects the running agent harness and resolves crewmate/secondmate
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
)

// KnownHarnesses lists all supported harness names.
var KnownHarnesses = []string{Claude, Codex, Opencode, Pi, Grok}

// IsKnownHarness reports whether name is a supported harness.
func IsKnownHarness(name string) bool {
	for _, h := range KnownHarnesses {
		if h == name {
			return true
		}
	}
	return false
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
	markers := map[string]string{
		"CLAUDE_CODE":         Claude,
		"CODECLIMB":           Codex,
		"OPENCODE":            Opencode,
		"PI_CODING_AGENT_DIR": Pi,
		"GROK_VM_ID":          Grok,
	}
	for env, harness := range markers {
		if os.Getenv(env) != "" {
			return harness
		}
	}
	return ""
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
func matchProcessName(name string) string {
	name = strings.ToLower(filepath.Base(name))
	switch {
	case name == "claude" || name == "claude-code" || name == "claude code":
		return Claude
	case name == "codex" || name == "codeclimb" || strings.Contains(name, "codex"):
		return Codex
	case name == "opencode" || strings.Contains(name, "opencode"):
		return Opencode
	case name == "pi" || name == "pi-coding-agent" || strings.Contains(name, "pi-coding"):
		return Pi
	case strings.Contains(name, "grok"):
		return Grok
	}
	return ""
}

// Template describes the CLI flags and defaults for spawning a harness.
type Template struct {
	ModelFlag     string
	EffortFlag    string
	DefaultModel  string
	DefaultEffort string
}

// Templates maps each known harness to its launch template (CLI flag conventions).
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
		EffortFlag: "",
	},
	Grok: {},
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

// Crew resolves the crewmate harness following the fallback chain:
//
//  1. Default harness from config/crew-dispatch.json
//  2. config/crew-harness file value
//  3. Detected harness from Detect()
//
// The config/crew-harness value "default" is treated as unset.
func Crew(homeDir string) (string, error) {
	// 1. Try dispatch config default
	dp, err := LoadDispatch(filepath.Join(config.ConfigDir(homeDir), "crew-dispatch.json"))
	if err == nil && dp.DefaultHarness != "" {
		return dp.DefaultHarness, nil
	}

	// 2. Try config/crew-harness
	if v, ok := lookupConfig(homeDir, "crew-harness"); ok {
		return v, nil
	}

	// 3. Fall back to detected harness
	return Detect()
}

// Secondmate resolves the secondmate harness following:
//
//  1. config/secondmate-harness file value
//  2. config/crew-harness file value
//  3. Detected harness from Detect()
//
// A value of "default" in config/secondmate-harness or config/crew-harness is treated as unset.
func Secondmate(homeDir string) (string, error) {
	// 1. Try config/secondmate-harness
	if v, ok := lookupConfig(homeDir, "secondmate-harness"); ok {
		return v, nil
	}

	// 2. Try config/crew-harness
	if v, ok := lookupConfig(homeDir, "crew-harness"); ok {
		return v, nil
	}

	// 3. Fall back to detected harness
	return Detect()
}
