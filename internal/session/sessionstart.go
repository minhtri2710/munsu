// Package session provides the session management commands.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// SessionStartResult holds the full session-start output digest.
type SessionStartResult struct {
	LockAcquired bool
	Bootstrap    *bootstrap.Result
	FleetSync    *fleet.SyncResult
}

func printDataFile(home, name string) {
	data, err := os.ReadFile(filepath.Join(home, "data", name))
	if err != nil {
		fmt.Printf("  ABSENT (data/%s)\n", name)
		return
	}
	lines := strings.Split(string(data), "\n")
	fmt.Printf("=== data/%s ===\n", name)
	for i, line := range lines {
		if i >= 20 {
			fmt.Println("  ...(truncated)")
			return
		}
		fmt.Println("  " + line)
	}
}

func printFleetState(home string) {
	snap, err := fleet.Snapshot(home)
	if err != nil {
		fmt.Printf("  error scanning fleet state: %v\n", err)
		return
	}
	fmt.Println("")
	fmt.Println("--- Fleet State ---")
	if len(snap.Tasks) == 0 {
		fmt.Println("  (no in-flight tasks)")
		return
	}
	for _, ts := range snap.Tasks {
		phase := fleet.PhaseFromMeta(ts.Window, ts.PaneAlive)
		statusDisplay := ts.LastStatus
		if statusDisplay == "" {
			statusDisplay = "no status"
		}
		fmt.Printf("  %s: %s (%s)\n", ts.ID, statusDisplay, phase)
	}
}

// supervisionModes maps each harness to its supervision mode label.
var supervisionModes = map[string]string{
	"claude":   "background-notify",
	"codex":    "foreground checkpoint",
	"grok":     "background-notify",
	"opencode": "TUI plugin background wake",
	"pi":       "extension background wake",
}

func supervisionMode(h string) string {
	if m, ok := supervisionModes[h]; ok {
		return m
	}
	return "generic fallback"
}

// printSupervisionBlock prints a per-harness supervision operating block.
func printSupervisionBlock(h string, acquired bool) {
	mode := supervisionMode(h)
	fmt.Printf("primary harness: %s\n", h)
	fmt.Printf("supervision mode: %s\n", mode)
	if acquired {
		fmt.Println("lock: acquired — this session owns normal supervision.")
	} else {
		fmt.Println("lock: read-only — do not drain, arm, or repair fleet state here.")
	}
	fmt.Println("")
	fmt.Println("Drain:   munsu wake-drain")

	switch h {
	case "claude":
		fmt.Println("Arm:     munsu watch-arm (as own background tool call)")
		fmt.Println("         Never use shell '&' for watcher supervision.")
		fmt.Println("Re-arm:  munsu watch-arm --restart on signal/stale/check/heartbeat")
		fmt.Println("Repair:  'watcher: FAILED - no live watcher' — fix and re-arm")

	case "codex":
		fmt.Println("Checkpoint: munsu watch run (one poll cycle — no timeout flag)")
		fmt.Println("Re-arm:  drain, handle wake, then next checkpoint (munsu watch run)")
		fmt.Println("Repair:  Re-run checkpoint after fixing any watcher issues")

	case "grok":
		fmt.Println("Arm:     munsu watch-arm (tracked background tool call)")
		fmt.Println("         In Grok: run_terminal_command with background: true.")
		fmt.Println("         Never use shell '&' for watcher supervision.")
		fmt.Println("Re-arm:  munsu watch-arm --restart on signal/stale/check/heartbeat")
		fmt.Println("Repair:  'watcher: FAILED ...' — fix and re-arm")

	case "pi":
		fmt.Println("Arm:     fm_watch_arm_pi tool (or 'munsu watch-arm --restart' as human fallback)")
		fmt.Println("         Do NOT run 'munsu watch-arm' through Pi's bash tool.")
		fmt.Println("Re-arm:  Pi extension re-arms automatically on watcher exit.")
		fmt.Println("Repair:  Drain, inspect extension status, restart Pi with extensions loaded.")

	case "opencode":
		fmt.Println("Arm:     OpenCode TUI plugin arms after session goes idle.")
		fmt.Println("         (.opencode/plugins/fm-primary-watch-arm.js)")
		fmt.Println("Re-arm:  Plugin re-arms automatically on watcher exit.")
		fmt.Println("Repair:  Drain, inspect, use 'munsu watch-arm' manually as recovery probe.")

	default:
		fmt.Println("Arm:     munsu watch ensure (idempotent start) or munsu watch run (checkpoint)")
		fmt.Println("         Use tracked background mechanism when available.")
		fmt.Println("         Never use shell '&' for watcher supervision.")
		fmt.Println("Repair:  Run 'munsu guard' to diagnose, then arm with one of the above.")
	}

	fmt.Println("Guard:   munsu guard")
}

// RunSessionStart executes the full session-start sequence:
func RunSessionStart(home string) (*SessionStartResult, error) {
	res := &SessionStartResult{}

	// 1. Acquire lock
	acquired, err := lifecycle.AcquireSession(home)
	if err != nil {
		return res, fmt.Errorf("lock acquire: %w", err)
	}
	res.LockAcquired = acquired

	if !acquired {
		fmt.Println("WARNING: Another session holds the lock. Operating read-only.")
	}

	// 2. Run bootstrap
	bootRes, err := bootstrap.Run(home, acquired, nil)
	if err != nil {
		return res, fmt.Errorf("bootstrap: %w", err)
	}
	res.Bootstrap = bootRes

	// 3. Print diagnostics
	fmt.Println("--- Bootstrap Diagnostics ---")
	if !acquired {
		fmt.Println("(read-only mode -- mutating sweeps skipped)")
	}
	for _, d := range bootRes.Diagnostics {
		fmt.Println("  " + d)
	}
	for _, c := range bootRes.ConfigDetails {
		fmt.Println("  " + c)
	}
	if len(bootRes.MissingTools) > 0 {
		fmt.Println("")
		fmt.Println("Missing tools -- install with: munsu bootstrap install <tool>")
	}

	// 4. Fleet sync (mutating sweep, only when locked)
	if acquired {
		syncRes, err := fleet.Sync(home, "")
		if err != nil {
			fmt.Printf("fleet-sync error: %v\n", err)
		} else {
			res.FleetSync = syncRes
			fmt.Println("")
			fmt.Println("--- Fleet Sync ---")
			for _, s := range syncRes.Synced {
				fmt.Printf("  synced: %s\n", s)
			}
			for _, s := range syncRes.Stuck {
				fmt.Printf("  STUCK: %s\n", s)
			}
			if len(syncRes.Errors) > 0 {
				for _, e := range syncRes.Errors {
					fmt.Printf("  error: %s\n", e)
				}
			}
		}
	}

	// 5. Context section
	fmt.Println("")
	fmt.Println("--- Context ---")
	printDataFile(home, "captain.md")
	printDataFile(home, "learnings.md")
	printDataFile(home, "projects.md")
	printDataFile(home, "secondmates.md")

	// 6. Fleet state section
	printFleetState(home)

	// 7. Supervision block — per-harness operating instructions
	fmt.Println("")
	fmt.Println("--- Supervision ---")
	h, err := harness.Crew(home)
	if err != nil {
		h = "unknown"
	}
	printSupervisionBlock(h, acquired)

	// 8. Session start complete
	fmt.Println("")
	fmt.Println("--- Session Start Complete ---")
	fmt.Println("Lock:", map[bool]string{true: "acquired", false: "refused (read-only)"}[acquired])

	return res, nil
}
