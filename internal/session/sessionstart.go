// Package session provides the session management commands.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
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
	bk, _, err := Resolve(home, "")
	if err != nil {
		bk = nil
	}
	stateDir := filepath.Join(home, "state")
	entries, readErr := os.ReadDir(stateDir)
	if readErr != nil {
		return
	}
	fmt.Println("")
	fmt.Println("--- Fleet State ---")
	hasTasks := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".meta") {
			continue
		}
		id := name[:len(name)-5]
		hasTasks = true
		var lastStatus string
		if data, err := os.ReadFile(filepath.Join(stateDir, id+".status")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 {
				lastStatus = lines[len(lines)-1]
			}
		}
		metaData, _ := os.ReadFile(filepath.Join(stateDir, name))
		windowID := ""
		for _, line := range strings.Split(string(metaData), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "window" {
				windowID = strings.TrimSpace(v)
			}
		}
		alive := windowID != "" && bk != nil && bk.Alive(windowID)
		statusDisplay := lastStatus
		if statusDisplay == "" {
			statusDisplay = "no status"
		}
		aliveDisplay := "dead"
		if alive {
			aliveDisplay = "alive"
		}
		fmt.Printf("  %s: %s (%s)\n", id, statusDisplay, aliveDisplay)
	}
	if !hasTasks {
		fmt.Println("  (no in-flight tasks)")
	}
}

// RunSessionStart executes the full session-start sequence:
// lock -> bootstrap -> context/fleet digest.
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

	// 7. Supervision block
	fmt.Println("")
	fmt.Println("--- Supervision ---")
	fmt.Println("Wake handling: wake-drain → crew-state <id> → watch-arm if tasks still in flight.")

	// 8. Session start complete
	fmt.Println("")
	fmt.Println("--- Session Start Complete ---")
	fmt.Println("Lock:", map[bool]string{true: "acquired", false: "refused (read-only)"}[acquired])

	return res, nil
}
