package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

var (
	homeOverride string
)

// ExactArgs returns a cobra.PositionalArgs validator that wraps cobra.ExactArgs
// but includes the command's Use string in the error message so users see the
// expected format (especially important when descriptions need quoting).
// Returns a usageError (exit 2) when args are wrong, per AXI contract.
func ExactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError("invalid_argument",
				fmt.Sprintf("Run `%s --help`", commandPath(cmd)),
				fmt.Sprintf("%s accepts %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use))
		}
		return nil
	}
}

// NoArgs is a cobra.PositionalArgs validator that requires no arguments.
func NoArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageError("invalid_argument",
			fmt.Sprintf("Run `%s --help`", commandPath(cmd)),
			fmt.Sprintf("%s accepts no arguments, received %d: %s", cmd.Name(), len(args), cmd.Use))
	}
	return nil
}

// MinimumNArgs returns a cobra.PositionalArgs validator that requires at least n args.
func MinimumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usageError("invalid_argument",
				fmt.Sprintf("Run `%s --help`", commandPath(cmd)),
				fmt.Sprintf("%s requires at least %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use))
		}
		return nil
	}
}

// MaximumNArgs returns a cobra.PositionalArgs validator that requires at most n args.
func MaximumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usageError("invalid_argument",
				fmt.Sprintf("Run `%s --help`", commandPath(cmd)),
				fmt.Sprintf("%s accepts at most %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use))
		}
		return nil
	}
}

// isDefaultHome returns true if the resolved homeDir is the default ~/.munsu.
// Used to force manual backlog backend for custom homes to prevent data leaks.
func isDefaultHome(homeDir string) bool {
	defaultHome, err := home.Resolve("")
	if err != nil {
		return true // conservative: assume default
	}
	return homeDir == defaultHome
}

// fleetSummary prints a compact fleet/orientation snapshot to the given writer.
func fleetSummary(w io.Writer, homeDir string) {
	snap, snapErr := fleet.Snapshot(homeDir)

	totalTasks := 0
	inFlight := 0
	if snapErr == nil && snap != nil {
		totalTasks = len(snap.Tasks)
		for _, ts := range snap.Tasks {
			if ts.Kind == "ship" || ts.Kind == "scout" {
				inFlight++
			}
		}
	}

	watcherStatus := "--"
	beat := lifecycle.ReadBeatStatus(homeDir, time.Now())
	if beat.Exists {
		if beat.Stale {
			watcherStatus = "stale"
		} else {
			watcherStatus = "alive"
		}
	}

	fmt.Fprintf(w, "munsu @ %s\n\n", homeDir)
	fmt.Fprintf(w, "fleet: %d tasks (%d in-flight) | watcher: %s | holds: --\n", totalTasks, inFlight, watcherStatus)

	if snapErr == nil && snap != nil && len(snap.Tasks) > 0 {
		fmt.Fprintln(w)
		for _, ts := range snap.Tasks {
			phase := fleet.PhaseFromProjection(ts)
			project := ts.Project
			if project == "" {
				project = "-"
			}
			status := ts.CurrentDescription
			if status == "" {
				status = ts.LastStatus
			}
			if status == "" {
				status = phase
			}
			fmt.Fprintf(w, "  %-20s [%-10s] %s\n", ts.ID, phase, project)
			if status != "" && status != phase {
				fmt.Fprintf(w, "  %-20s  %s\n", "", status)
			}
		}
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No tasks. Start with `munsu backlog add <id> \"<description>\"`.")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Next: munsu fleet bearings | munsu peek <id> | munsu --help")
}

// NewRootCommand builds the munsu root cobra command with all subcommands.
func NewRootCommand() *cobra.Command {
	cobra.EnableTraverseRunHooks = true
	root := &cobra.Command{
		Use:   "munsu",
		Short: "Standalone CLI for coding-agent fleet orchestration",
		Long: `munsu is an installable CLI that gives any coding-agent harness
soldier lifecycle capabilities — spawn, supervise, deliver — usable from any project directory,
with no requirement to live inside a specific project checkout.`,
		Version:            Version,
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableAutoGenTag:  true,
		DisableSuggestions: true,
		PersistentPreRunE:  guardWatcherPreRunE(),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			fleetSummary(cmd.OutOrStdout(), homeDir)
			return nil
		},
	}

	// Global persistent flags
	root.PersistentFlags().StringVar(&homeOverride, "home", "", "munsu home directory (overrides MUNSU_HOME)")

	// All commands
	root.AddCommand(newHomeCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newBootstrapCmd())
	root.AddCommand(newSkillCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newWorktreeCmd())
	root.AddCommand(newHarnessCmd())
	root.AddCommand(newSessionStartCmd())
	root.AddCommand(newTaskCmd())
	root.AddCommand(newCapabilitiesCmd())
	root.AddCommand(newBackendCmd())
	root.AddCommand(newBacklogCmd())
	root.AddCommand(newBriefCmd())
	root.AddCommand(newSpawnCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newNotifyCmd())
	root.AddCommand(newPeekCmd())
	root.AddCommand(newSoldierStateCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newDeliveryCmd())
	root.AddCommand(newFleetCmd())
	root.AddCommand(newHerdrCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newWatchArmCmd())
	root.AddCommand(newWakeDrainCmd())
	root.AddCommand(newWakeCmd())
	root.AddCommand(newEventCmd())
	root.AddCommand(newContractGuardCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newStowCmd())
	root.AddCommand(newEnsureAgentsMdCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newDecisionHoldCmd())
	root.AddCommand(newCaptainCmd())
	root.AddCommand(newAfkCmd())
	root.AddCommand(newIntegrateCmd())
	root.AddCommand(newInboxCmd())

	return root
}
