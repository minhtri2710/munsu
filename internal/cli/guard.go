package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/spf13/cobra"
)

var guardCooldown = 5 * time.Minute

func guardCooldownPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".guard-cooldown")
}

func guardStateKey(beat lifecycle.BeatStatus, inFlight int) string {
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

func guardWatcherPreRunE() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if cmd == cmd.Root() && (len(args) == 0 || args[0] == "--help" || args[0] == "-h") {
			return nil
		}
		guardWarnWatcher()
		return nil
	}
}

func guardWarnWatcher() {
	if os.Getenv("MUNSU_GUARD_SKIP") == "1" {
		return
	}

	homeDir, err := home.Resolve(homeOverride)
	if err != nil {
		return
	}

	snap, err := fleet.Snapshot(homeDir)
	if err != nil || snap == nil || len(snap.Tasks) == 0 {
		return
	}

	inFlight := 0
	for _, ts := range snap.Tasks {
		if ts.Kind == "ship" || ts.Kind == "scout" {
			inFlight++
		}
	}

	result := waker.EvaluateGuard(homeDir, inFlight, time.Now())
	beat := result.BeatStatus

	cdPath := guardCooldownPath(homeDir)
	if beat.Exists && !beat.Stale {
		guardClearCooldown(cdPath)
		return
	}

	if inFlight == 0 {
		guardClearCooldown(cdPath)
		return
	}

	key := guardStateKey(beat, inFlight)
	if guardCheckCooldown(cdPath, key) {
		return
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
}
