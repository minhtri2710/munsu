package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

const Version = "0.1.0-dev"

var (
	homeOverride string
)

func notImplementedE(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("%s: not yet implemented", cmd.Name())
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

	return root
}

// newHomeCmd implements `munsu home [--mkdir]`.
func newHomeCmd() *cobra.Command {
	var mkdir bool
	cmd := &cobra.Command{
		Use:   "home [--mkdir]",
		Short: "Print the munsu home directory",
		Long: `Resolve and print the munsu home directory.

Resolution order: --home flag > MUNSU_HOME env > ~/.munsu.
With --mkdir, create the home directory tree {state,data,config,projects}.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			if mkdir {
				if err := home.EnsureDirTree(path); err != nil {
					return err
				}
			}
			fmt.Println(path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&mkdir, "mkdir", false, "create the home directory tree")
	return cmd
}

// --- Stub command constructors ---

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create home and seed orchestrator operating manual",
		RunE:  notImplementedE,
	}
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and write munsu configuration",
	}
	cmd.AddCommand(&cobra.Command{Use: "get <key>", Short: "Get a configuration value", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "set <key> <value>", Short: "Set a configuration value", RunE: notImplementedE})
	return cmd
}

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project registry",
	}
	cmd.AddCommand(&cobra.Command{Use: "add <name> <path-or-url>", Short: "Register a project", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List registered projects", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "show <name>", Short: "Show project details", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "rm <name>", Short: "Remove a registered project", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "mode <name>", Short: "Resolve delivery mode for a project", RunE: notImplementedE})
	return cmd
}

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage pooled git worktrees",
	}
	cmd.AddCommand(&cobra.Command{Use: "get <repo-path>", Short: "Acquire a pooled worktree", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "return <path>", Short: "Return a worktree to the pool", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "status", Short: "Show worktree pool status", RunE: notImplementedE})
	return cmd
}

func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Detect and manage agent harness adapters",
	}
	cmd.AddCommand(&cobra.Command{Use: "detect", Short: "Detect the running agent harness", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "crew", Short: "Resolve crewmate harness", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "secondmate", Short: "Resolve secondmate harness", RunE: notImplementedE})
	return cmd
}

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage task lifecycle",
	}
	cmd.AddCommand(&cobra.Command{Use: "add <id> <description>", Short: "Add a new task", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List tasks", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "show <id>", Short: "Show task details", RunE: notImplementedE})
	cmd.AddCommand(&cobra.Command{Use: "status <id>", Short: "Append a status line to a task", RunE: notImplementedE})
	return cmd
}

func newBriefCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "brief <id> <repo>",
		Short: "Scaffold a task brief",
		RunE:  notImplementedE,
	}
}

func newSpawnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a crewmate agent",
		RunE:  notImplementedE,
	}
}

func newSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send <id> <line>",
		Short: "Send a line to a crewmate endpoint",
		RunE:  notImplementedE,
	}
}

func newPeekCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "peek <id>",
		Short: "Peek at crewmate output",
		RunE:  notImplementedE,
	}
}

func newCrewStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "crew-state <id>",
		Short: "Read crewmate current state",
		RunE:  notImplementedE,
	}
}

func newPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a scout task to ship",
		RunE:  notImplementedE,
	}
}

func newTeardownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "teardown <id>",
		Short: "Tear down a crewmate",
		RunE:  notImplementedE,
	}
}

func newReviewDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review-diff <id>",
		Short: "Review diff between crewmate branch and base",
		RunE:  notImplementedE,
	}
}

func newPRCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-check <id> <pr-url>",
		Short: "Record PR URL and arm merge poll",
		RunE:  notImplementedE,
	}
}

func newPRMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-merge <id> <pr-url>",
		Short: "Merge a PR via GitHub CLI",
		RunE:  notImplementedE,
	}
}

func newMergeLocalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge-local <id>",
		Short: "Fast-forward merge to local default branch",
		RunE:  notImplementedE,
	}
}

func newBacklogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backlog <verb> [args...]",
		Short: "Manage the task backlog",
		Long: `Manage the task backlog via the configured backlog backend.

Verbs: add, start, done, list, show, block, unblock, ready, hold, update, render.`,
		RunE: notImplementedE,
	}
}

func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Lock, bootstrap, wake-drain, and digest fleet state",
		RunE:  notImplementedE,
	}
}

func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Detect toolchain and run setup sweeps",
		RunE:  notImplementedE,
	}
}

func newFleetSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-sync [<project>]",
		Short: "Fast-forward refresh project clones",
		RunE:  notImplementedE,
	}
}

func newFleetSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-snapshot",
		Short: "Emit fleet snapshot JSON",
		RunE:  notImplementedE,
	}
}

func newFleetViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-view",
		Short: "Render fleet view from snapshot",
		RunE:  notImplementedE,
	}
}

func newBearingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bearings",
		Short: "Compact resume report",
		RunE:  notImplementedE,
	}
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Run the event-driven watcher",
		RunE:  notImplementedE,
	}
}

func newWatchArmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch-arm",
		Short: "Arm the watcher (home-scoped)",
		RunE:  notImplementedE,
	}
}

func newWakeDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake-drain",
		Short: "Drain queued wakes",
		RunE:  notImplementedE,
	}
}

func newGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Warn on tangle or stale watcher",
		RunE:  notImplementedE,
	}
}

func newStowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stow",
		Short: "Sweep session for durable knowledge",
		RunE:  notImplementedE,
	}
}

func newEnsureAgentsMdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure-agents-md <project>",
		Short: "Ensure project AGENTS.md and CLAUDE.md symlink",
		RunE:  notImplementedE,
	}
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Self-update munsu",
		RunE:  notImplementedE,
	}
}
