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
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/scope"
)

// SessionStartResult holds the full session-start output digest.
type SessionStartResult struct {
	LockAcquired bool
	Bootstrap    *bootstrap.Result
	FleetSync    *fleet.SyncResult
	Watcher      WatchEnsureResult
}

// WatchEnsureResult is the session-start view of watcher ensure state.
type WatchEnsureResult struct {
	State string
	Error string
}

type WatchEnsureFunc func(home string) WatchEnsureResult

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

// supervisionMode returns the normal persistent watcher mode.
func supervisionMode(string) string { return "persistent daemon" }

// printSupervisionBlock prints the watcher operating contract.
func printSupervisionBlock(h string, acquired bool) {
	fmt.Printf("primary harness: %s\n", h)
	fmt.Printf("supervision mode: %s\n", supervisionMode(h))
	if acquired {
		fmt.Println("lock: acquired — this session owns normal supervision.")
	} else {
		fmt.Println("lock: read-only — do not drain, arm, or repair fleet state here.")
	}
	fmt.Println("")
	fmt.Println("Daemon:  munsu watch ensure (idempotent start or attach)")
	fmt.Println("Inspect: munsu watch run (one poll cycle)")
	fmt.Println("Drain:   munsu wake-drain")
	fmt.Println("Repair:  munsu watch ensure")
	fmt.Println("Guard:   munsu guard")
}

func ensureWatcherForSession(home string, acquired bool, ensure WatchEnsureFunc) WatchEnsureResult {
	if !acquired {
		return WatchEnsureResult{State: "read-only"}
	}
	snap, err := fleet.Snapshot(home)
	if err != nil {
		return WatchEnsureResult{State: "failed", Error: err.Error()}
	}
	inFlight := false
	for _, ts := range snap.Tasks {
		if ts.Kind == "ship" || ts.Kind == "scout" {
			inFlight = true
			break
		}
	}
	if !inFlight {
		return WatchEnsureResult{State: "idle"}
	}
	if ensure == nil {
		return WatchEnsureResult{State: "failed", Error: "watcher ensure unavailable"}
	}
	return ensure(home)
}

// RunSessionStart executes session-start without an injected watcher starter.
func RunSessionStart(home string) (*SessionStartResult, error) {
	return RunSessionStartWithWatcher(home, nil)
}

func checkSessionScope(home string) error {
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		return fmt.Errorf("no-mistakes gate agent must not drive the fleet")
	}
	projects, err := project.List(home)
	if err != nil {
		return fmt.Errorf("scope projects: %w", err)
	}
	for _, registered := range projects {
		path, err := project.ResolveRepoPath(home, registered.Name)
		if err != nil {
			return fmt.Errorf("scope project %s: %w", registered.Name, err)
		}
		if err := scope.GateRefusalError(path); err != nil {
			return fmt.Errorf("project %s: %w", registered.Name, err)
		}
	}
	return nil
}

// RunSessionStartWithWatcher executes the full session-start sequence.
func RunSessionStartWithWatcher(home string, ensure WatchEnsureFunc) (*SessionStartResult, error) {
	res := &SessionStartResult{}
	if err := checkSessionScope(home); err != nil {
		return res, fmt.Errorf("session-start refused: %w", err)
	}

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
	for _, d := range bootRes.Tools {
		fmt.Println("  " + d.String())
	}
	if bootRes.Auth != nil {
		fmt.Println("  " + bootRes.Auth.String())
	}
	for _, c := range bootRes.Configs {
		fmt.Println("  " + c.String())
	}
	if bootRes.GC != nil {
		fmt.Println("  " + bootRes.GC.String())
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

	res.Watcher = ensureWatcherForSession(home, acquired, ensure)
	fmt.Println("")
	fmt.Println("--- Watcher Ensure ---")
	if res.Watcher.Error != "" {
		fmt.Printf("  %s: %s\n", res.Watcher.State, res.Watcher.Error)
	} else {
		if res.Watcher.State == "idle" {
			fmt.Println("  idle: no in-flight tasks; watcher not started")
		} else {
			fmt.Printf("  %s\n", res.Watcher.State)
		}
	}
	if res.Watcher.State == "failed" {
		fmt.Println("  Repair: munsu watch ensure")
	} else if res.Watcher.State != "idle" && res.Watcher.State != "read-only" {
		fmt.Println("  Repair: munsu watch ensure")
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
