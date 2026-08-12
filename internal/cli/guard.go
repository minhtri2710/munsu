package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

var guardCooldown = 5 * time.Minute

func guardCooldownPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".guard-cooldown")
}

func guardStateKey(beat orchestrator.BeatStatus, inFlight int) string {
	if !beat.Exists {
		return "missing:" + strconv.Itoa(inFlight)
	}
	if beat.Stale {
		return "stale:" + strconv.Itoa(inFlight)
	}
	return "fresh:" + strconv.Itoa(inFlight)
}

func guardCheckCooldown(path, key string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	parts := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(parts) < 2 {
		return false
	}
	if parts[0] != key {
		return false
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < guardCooldown
}

func guardWriteCooldown(path, key string) {
	content := key + "\n" + strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	_ = os.WriteFile(path, []byte(content), 0644)
}

func guardClearCooldown(path string) {
	_ = os.Remove(path)
}

// guardInFlight counts task-facing (ship/scout) tasks using the single
// canonical current-state snapshot. It fails closed on unreadable Task truth:
// a corrupt/legacy/meta-only fleet home returns an error and is never treated
// as an empty fleet. A home that is simply not initialized has no fleet to
// guard and yields zero in-flight tasks (not an error).
func guardInFlight(homeDir string) (int, error) {
	snap, err := fleet.Snapshot(homeDir, snapshotDeps())
	if err != nil {
		if errors.Is(err, home.ErrNotInitialized) {
			return 0, nil
		}
		return 0, err
	}
	inFlight := 0
	for _, ts := range snap.Tasks {
		if ts.Kind == "ship" || ts.Kind == "scout" {
			inFlight++
		}
	}
	return inFlight, nil
}

func guardWatcherPreRunE() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if cmd == cmd.Root() && (len(args) == 0 || args[0] == "--help" || args[0] == "-h") {
			return nil
		}
		return guardWarnWatcher()
	}
}

// guardWarnWatcher evaluates guard state and emits a stderr warning when tasks
// are in flight but the watcher is absent/stale. It is an advisory pre-run hook:
// it never blocks the invoking command. When there is no initialized fleet home
// to guard it is a no-op; when an initialized home has unreadable Task truth
// (a snapshot/current-state failure) it fails loud to stderr rather than
// silently treating the fleet as empty.
func guardWarnWatcher() error {
	if os.Getenv("MUNSU_GUARD_SKIP") == "1" {
		return nil
	}

	homeDir, err := home.Resolve(homeOverride)
	if err != nil {
		return nil
	}

	// No initialized fleet home to guard: not a guardable situation.
	if _, err := home.Open(homeDir); err != nil {
		if errors.Is(err, home.ErrNotInitialized) {
			return nil
		}
		// An initialized-but-unreadable home should fail loud below; surface
		// only genuine absence here.
		return nil
	}

	inFlight, err := guardInFlight(homeDir)
	if err != nil {
		// Fail loud, never silent: surface the unreadable Task truth without
		// blocking the invoking command.
		fmt.Fprintf(os.Stderr, "\nWARNING: cannot read authoritative fleet state: %v\n", err)
		return nil
	}

	result := orchestrator.EvaluateGuard(homeDir, inFlight, time.Now())
	beat := result.BeatStatus

	cdPath := guardCooldownPath(homeDir)
	if beat.Exists && !beat.Stale {
		guardClearCooldown(cdPath)
		return nil
	}

	if inFlight == 0 {
		guardClearCooldown(cdPath)
		return nil
	}

	key := guardStateKey(beat, inFlight)
	if guardCheckCooldown(cdPath, key) {
		return nil
	}

	guardWriteCooldown(cdPath, key)

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
	fmt.Fprintf(os.Stderr, "  Repair with 'munsu watch ensure' or set MUNSU_GUARD_SKIP=1 to silence.\n\n")
	return nil
}
