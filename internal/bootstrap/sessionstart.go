// Package session provides the session management commands.
package bootstrap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// snapshotDeps builds the explicit read dependencies for fleet snapshot callers
// in this package: the canonical current-state query is always present; no
// endpoint diagnostic probe is wired here because probe output is diagnostic
// only and never affects canonical lifecycle state.
func snapshotDeps() fleet.SnapshotDependencies {
	return fleet.SnapshotDependencies{
		CurrentState: fleet.NewCanonicalCurrentState(),
	}
}

type SessionStartResult struct {
	LockAcquired    bool
	RuntimeIdentity *RuntimeIdentity
	Bootstrap       *Result
	FleetSync       *fleet.SyncResult
	Watcher         WatchEnsureResult
	CaptainLiveness *CaptainLivenessResult
}

type WatchEnsureResult struct {
	State string
	Error string
}

type WatchEnsureFunc func(home string) WatchEnsureResult

// CaptainProbe is one captain's non-mutating liveness reading surfaced at session-start.
type CaptainProbe struct {
	ID     string
	Home   string
	Status string // alive | dead | seeded | unknown
}

// CaptainRecoverSummary records the outcome of an opt-in recover sweep.
type CaptainRecoverSummary struct {
	Relaunched int
	Alive      int
	Seeded     int
	Failed     int
	Entries    []string // human-readable per-captain lines
}

// CaptainLivenessResult holds the captain liveness section of the session digest.
type CaptainLivenessResult struct {
	Probes  []CaptainProbe
	Recover *CaptainRecoverSummary // non-nil only when recover ran
	HasDead bool                   // any probe with Status == "dead"
}

// CaptainLivenessFunc probes captain liveness for the session digest. When recover is
// true it also relaunches launched-but-dead endpoints (fail-closed on unknown harness).
// Wired by the CLI layer to avoid an import cycle between session and captain.
type CaptainLivenessFunc func(home string, recover bool) CaptainLivenessResult

func printDataFile(w io.Writer, home, name string) {
	data, err := os.ReadFile(filepath.Join(home, "data", name))
	if err != nil {
		fmt.Fprintf(w, "  ABSENT (data/%s)\n", name)
		return
	}
	lines := strings.Split(string(data), "\n")
	fmt.Fprintf(w, "=== data/%s ===\n", name)
	for i, line := range lines {
		if i >= 20 {
			fmt.Fprintln(w, "  ...(truncated)")
			return
		}
		fmt.Fprintln(w, "  "+line)
	}
}

func printFleetState(w io.Writer, home string) {
	snap, err := fleet.Snapshot(home, snapshotDeps())
	if err != nil {
		fmt.Fprintf(w, "  error scanning fleet state: %v\n", err)
		return
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--- Fleet State ---")
	if len(snap.Tasks) == 0 {
		fmt.Fprintln(w, "  (no in-flight tasks)")
		return
	}
	for _, ts := range snap.Tasks {
		phase := fleet.PhaseFromProjection(ts)
		statusDisplay := ts.CurrentState
		if statusDisplay == "" {
			statusDisplay = "no status"
		}
		fmt.Fprintf(w, "  %s: %s (%s)\n", ts.ID, statusDisplay, phase)
	}
}

func supervisionMode(string) string { return "persistent daemon" }

func printSupervisionBlock(w io.Writer, h string, acquired bool) {
	fmt.Fprintf(w, "primary harness: %s\n", h)
	fmt.Fprintf(w, "supervision mode: %s\n", supervisionMode(h))
	if acquired {
		fmt.Fprintln(w, "lock: acquired \u2014 this session owns normal supervision.")
	} else {
		fmt.Fprintln(w, "lock: read-only \u2014 do not drain, arm, or repair fleet state here.")
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Daemon:  munsu watch ensure (idempotent start or attach)")
	fmt.Fprintln(w, "Inspect: munsu watch run (one poll cycle)")
	fmt.Fprintln(w, "Claim:   munsu wake claim --consumer <id>  (lease signal wakes from return channel)")
	fmt.Fprintln(w, "Claim:   munsu wake claim --consumer <id>  (lease signal wakes from return channel)")
	fmt.Fprintln(w, "Repair:  munsu watch ensure")
	fmt.Fprintln(w, "Guard:   munsu guard")
}

func ensureWatcherForSession(home string, acquired bool, ensure WatchEnsureFunc) WatchEnsureResult {
	if !acquired {
		return WatchEnsureResult{State: "read-only"}
	}
	snap, err := fleet.Snapshot(home, snapshotDeps())
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

// printCaptainLiveness surfaces the captain liveness section of the digest. When the
// seam is unwired (nil) it is a no-op. Otherwise it always probes and prints; when
// recover is true it also relaunches launched-but-dead endpoints via the seam.
// Read-only sessions probe but never recover.
func printCaptainLiveness(w io.Writer, home string, acquired bool, fn CaptainLivenessFunc) *CaptainLivenessResult {
	if fn == nil {
		return nil
	}
	res := fn(home, acquired)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--- Captain Liveness ---")
	if len(res.Probes) == 0 {
		fmt.Fprintln(w, "  (no captains registered)")
		return &res
	}
	for _, p := range res.Probes {
		fmt.Fprintf(w, "  %s: %s\n", p.ID, p.Status)
	}
	if res.HasDead {
		fmt.Fprintln(w, "SECOND_LIVENESS: dead captain endpoint(s) detected")
		if !acquired {
			fmt.Fprintln(w, "  read-only: run 'munsu captain recover' or 'munsu session-start --recover'")
		} else if res.Recover == nil {
			fmt.Fprintln(w, "  relaunch with: munsu captain recover  (or 'munsu session-start --recover')")
		}
	}
	if res.Recover != nil {
		r := res.Recover
		fmt.Fprintf(w, "  recover: relaunched=%d alive=%d seeded=%d failed=%d\n", r.Relaunched, r.Alive, r.Seeded, r.Failed)
		for _, line := range r.Entries {
			fmt.Fprintln(w, "  "+line)
		}
		if r.Failed > 0 {
			fmt.Fprintln(w, "SECOND_LIVENESS: recovery completed with failures (see above)")
		}
	}
	return &res
}

func RunSessionStart(w io.Writer, home string) (*SessionStartResult, error) {
	return RunSessionStartWithWatcher(w, home, nil, nil)
}

func currentWorkingDirOrHome(home string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return home
	}
	return cwd
}

type ScopeCheckResult struct {
	IsPrimary    bool
	IsGateAgent  bool
	ErrorMessage string
}

func CheckSessionScope(home string) ScopeCheckResult {
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		return ScopeCheckResult{IsGateAgent: true}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ScopeCheckResult{ErrorMessage: err.Error()}
	}
	cls := fleet.Classify(cwd)
	if cls.Identity != fleet.Primary {
		return ScopeCheckResult{}
	}
	return ScopeCheckResult{IsPrimary: true}
}

func checkSessionScope(home string) error {
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		return fmt.Errorf("no-mistakes gate agent must not drive the fleet")
	}
	projects, err := fleet.List(home)
	if err != nil {
		return fmt.Errorf("scope projects: %w", err)
	}
	for _, registered := range projects {
		path, err := fleet.ResolveRepoPath(home, registered.Name)
		if err != nil {
			return fmt.Errorf("scope project %s: %w", registered.Name, err)
		}
		if err := fleet.GateRefusalError(path); err != nil {
			return fmt.Errorf("project %s: %w", registered.Name, err)
		}
	}
	return nil
}

// printIntegrationMatrix prints the role-aware integration summary for the
// current role (determined by MUNSU_ROLE env, defaulting to "general").
func printIntegrationMatrix(w io.Writer, home string) {
	role := os.Getenv("MUNSU_ROLE")
	if role == "" {
		role = "general"
	}
	r, err := Doctor(home, Role(role))
	if err != nil {
		return
	}
	fmt.Fprintf(w, "\n--- Integration Matrix (role: %s) ---\n", r.Role)
	for _, e := range r.Entries {
		fmt.Fprintln(w, "  "+e.String())
	}
}

func RunSessionStartWithWatcher(w io.Writer, home string, ensure WatchEnsureFunc, captainLiveness CaptainLivenessFunc) (*SessionStartResult, error) {
	res := &SessionStartResult{}
	runtimeIdentity := CollectRuntimeIdentity(home, currentWorkingDirOrHome(home), "")
	res.RuntimeIdentity = &runtimeIdentity
	fmt.Fprintln(w, "--- Runtime Identity ---")
	for _, line := range RuntimeIdentityLines(res.RuntimeIdentity) {
		fmt.Fprintln(w, "  "+line)
	}
	fmt.Fprintln(w, "")

	if err := checkSessionScope(home); err != nil {
		return res, fmt.Errorf("session-start refused: %w", err)
	}

	acquired, err := orchestrator.AcquireSession(home)
	if err != nil {
		return res, fmt.Errorf("lock acquire: %w", err)
	}
	res.LockAcquired = acquired

	if !acquired {
		fmt.Fprintln(w, "WARNING: Another session holds the lock. Operating read-only.")
	}

	bootRes, err := runWithRuntimeIdentity(home, acquired, nil, res.RuntimeIdentity)
	if err != nil {
		// The session did not start, so nothing should go on holding its lock:
		// the process-lifetime hold below is earned by a session that ran.
		if acquired {
			_ = orchestrator.ReleaseSession(home)
			res.LockAcquired = false
		}
		return res, fmt.Errorf("bootstrap: %w", err)
	}
	res.Bootstrap = bootRes

	fmt.Fprintln(w, "--- Bootstrap Diagnostics ---")
	if !acquired {
		fmt.Fprintln(w, "(read-only mode -- mutating sweeps skipped)")
	}
	for _, d := range bootRes.Tools {
		fmt.Fprintln(w, "  "+d.String())
	}
	if bootRes.Auth != nil {
		fmt.Fprintln(w, "  "+bootRes.Auth.String())
	}
	for _, c := range bootRes.Configs {
		fmt.Fprintln(w, "  "+c.String())
	}
	if bootRes.GC != nil {
		fmt.Fprintln(w, "  "+bootRes.GC.String())
	}
	if len(bootRes.MissingTools) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Missing tools -- install with: munsu bootstrap install <tool>")
	}

	printIntegrationMatrix(w, home)

	if acquired {
		syncRes, err := fleet.Sync(home, "")
		if err != nil {
			fmt.Fprintf(w, "fleet-sync error: %v\n", err)
		} else {
			res.FleetSync = syncRes
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "--- Fleet Sync ---")
			for _, s := range syncRes.Synced {
				fmt.Fprintf(w, "  synced: %s\n", s)
			}
			for _, s := range syncRes.Stuck {
				fmt.Fprintf(w, "  STUCK: %s\n", s)
			}
			if len(syncRes.Errors) > 0 {
				for _, e := range syncRes.Errors {
					fmt.Fprintf(w, "  error: %s\n", e)
				}
			}
		}
	}

	res.CaptainLiveness = printCaptainLiveness(w, home, acquired, captainLiveness)

	res.Watcher = ensureWatcherForSession(home, acquired, ensure)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--- Watcher Ensure ---")
	if res.Watcher.Error != "" {
		fmt.Fprintf(w, "  %s: %s\n", res.Watcher.State, res.Watcher.Error)
	} else {
		if res.Watcher.State == "idle" {
			fmt.Fprintln(w, "  idle: no in-flight tasks; watcher not started")
		} else {
			fmt.Fprintf(w, "  %s\n", res.Watcher.State)
		}
	}
	if res.Watcher.State == "failed" {
		fmt.Fprintln(w, "  Repair: munsu watch ensure")
	} else if res.Watcher.State != "idle" && res.Watcher.State != "read-only" {
		fmt.Fprintln(w, "  Repair: munsu watch ensure")
	}

	fmt.Fprintln(w, "")
	printDataFile(w, home, "general.md")
	printDataFile(w, home, "learnings.md")
	printFleetState(w, home)

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--- Supervision ---")
	h, err := harness.Soldier(home)
	if err != nil {
		h = "unknown"
	}
	printSupervisionBlock(w, h, acquired)

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--- Session Start Complete ---")
	fmt.Fprintf(w, "Lock: %s\n", map[bool]string{true: "acquired", false: "refused (read-only)"}[acquired])

	return res, nil
}
