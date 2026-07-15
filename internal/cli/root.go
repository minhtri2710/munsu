package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

var (
	homeOverride string
)

// ExactArgs returns a cobra.PositionalArgs validator that wraps cobra.ExactArgs
// but includes the command's Use string in the error message so users see the
// expected format (especially important when descriptions need quoting).
func ExactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("%s accepts %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use)
		}
		return nil
	}
}

// NoArgs is a cobra.PositionalArgs validator that requires no arguments.
func NoArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s accepts no arguments, received %d: %s", cmd.Name(), len(args), cmd.Use)
	}
	return nil
}

// MinimumNArgs returns a cobra.PositionalArgs validator that requires at least n args.
func MinimumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("%s requires at least %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use)
		}
		return nil
	}
}

// MaximumNArgs returns a cobra.PositionalArgs validator that requires at most n args.
func MaximumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("%s accepts at most %d arg(s), received %d: %s", cmd.Name(), n, len(args), cmd.Use)
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

// NewRootCommand builds the munsu root cobra command with all subcommands.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "munsu",
		Short: "Standalone CLI port of firstmate crew capabilities",
		Long: `munsu is an installable CLI that gives any coding-agent harness
the firstmate crew capability, usable from any project directory,
with no requirement to live inside a firstmate checkout.`,
		Version:            Version,
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableAutoGenTag:  true,
		DisableSuggestions: true,
	}

	// Global persistent flags
	root.PersistentFlags().StringVar(&homeOverride, "home", "", "munsu home directory (overrides MUNSU_HOME)")

	// All commands
	root.AddCommand(newHomeCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newWorktreeCmd())
	root.AddCommand(newHarnessCmd())
	root.AddCommand(newTaskCmd())
	root.AddCommand(newBriefCmd())
	root.AddCommand(newSpawnCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newPeekCmd())
	root.AddCommand(newCrewStateCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newReviewDiffCmd())
	root.AddCommand(newPRCheckCmd())
	root.AddCommand(newPRMergeCmd())
	root.AddCommand(newMergeLocalCmd())
	root.AddCommand(newBacklogCmd())
	root.AddCommand(newSessionStartCmd())
	root.AddCommand(newBootstrapCmd())
	root.AddCommand(newFleetSyncCmd())
	root.AddCommand(newFleetSnapshotCmd())
	root.AddCommand(newFleetViewCmd())
	root.AddCommand(newBearingsCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newWatchArmCmd())
	root.AddCommand(newWakeDrainCmd())
	root.AddCommand(newGuardCmd())
	root.AddCommand(newStowCmd())
	root.AddCommand(newEnsureAgentsMdCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newSecondmateCmd())
	root.AddCommand(newAfkCmd())

	return root
}
