package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/spf13/cobra"
)

// guardWatcherPreRunE returns a PersistentPreRunE that warns on stderr when
// tasks are in flight but the watcher beat is stale or missing.
// It is fail-open: never blocks commands, never returns an error.
// Respects MUNSU_GUARD_SKIP=1 escape hatch.
func guardWatcherPreRunE() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		// Skip for bare root (no subcommand) — fleetSummary already shows status.
		if cmd == cmd.Root() && (len(args) == 0 || args[0] == "--help" || args[0] == "-h") {
			return nil
		}
		guardWarnWatcher()
		return nil
	}
}

// guardWarnWatcher checks for in-flight tasks with a stale/missing watcher
// and emits a warning to stderr. It is fail-open.
func guardWarnWatcher() {
	if os.Getenv("MUNSU_GUARD_SKIP") == "1" {
		return
	}

	homeDir, err := home.Resolve(homeOverride)
	if err != nil {
		return // fail open
	}

	snap, err := fleet.Snapshot(homeDir)
	if err != nil || snap == nil || len(snap.Tasks) == 0 {
		return // fail open
	}

	inFlight := 0
	for _, ts := range snap.Tasks {
		if ts.Kind == "ship" || ts.Kind == "scout" {
			inFlight++
		}
	}
	if inFlight == 0 {
		return
	}

	result := waker.EvaluateGuard(homeDir, inFlight, time.Now())
	beat := result.BeatStatus
	if !beat.Exists || beat.Stale {
		status := "alive"
		ageStr := beat.Age.Round(time.Second).String()
		if !beat.Exists {
			status = "missing"
			ageStr = "never"
		} else if beat.Stale {
			status = "stale"
		}
		fmt.Fprintf(os.Stderr, "\nWARNING: %d task(s) in flight but watcher is %s (last beat: %s)\n",
			inFlight, status, ageStr)
		fmt.Fprintf(os.Stderr, "  Start the watcher with 'munsu watch-arm' or set MUNSU_GUARD_SKIP=1 to silence.\n\n")
	}
}
