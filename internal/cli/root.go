package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

// CommitSHA holds the verified commit SHA, set via ldflags at build time.
// It is propagated to orchestrator.CommitSHA for watcher identity comparison.
var CommitSHA = ""

func init() {
	// Propagate version and commit SHA to supervision for watcher identity.
	// Only propagate CommitSHA if the CLI explicitly provides one, to avoid
	// clobbering the linker-injected orchestrator.CommitSHA with an empty value.
	orchestrator.BuildVersion = Version
	if CommitSHA != "" {
		orchestrator.CommitSHA = CommitSHA
	}
}

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
			view, err := loadRootSummary(homeDir)
			if err != nil {
				return fmt.Errorf("rendering fleet summary: %w", err)
			}
			renderRootSummary(cmd.OutOrStdout(), view)
			return nil
		},
	}

	// Global persistent flags
	root.PersistentFlags().StringVar(&homeOverride, "home", "", "munsu home directory (overrides MUNSU_HOME)")

	// All commands
	root.AddCommand(newHomeCmd())
	root.AddCommand(newGitGuardCmd())
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
	root.AddCommand(newBriefCmd())
	root.AddCommand(newSpawnCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newReadyCmd())
	root.AddCommand(newConsumeReadyCmd())
	root.AddCommand(newNotifyCmd())
	root.AddCommand(newPeekCmd())
	root.AddCommand(newSoldierStateCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newSoldierFlushCmd())
	root.AddCommand(newDeliveryCmd())
	root.AddCommand(newFleetCmd())
	root.AddCommand(newHerdrCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newWatchArmCmd())
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
	root.AddCommand(newManualCmd())
	root.AddCommand(newInboxCmd())
	root.AddCommand(newTurnendCmd())
	root.AddCommand(newContextCmd())

	return root
}
