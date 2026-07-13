package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/project"
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

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and write munsu configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			val, err := config.Get(homeDir, args[0])
			if err != nil {
				return err
			}
			fmt.Println(val)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return config.Set(homeDir, args[0], args[1])
		},
	})
	return cmd
}

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project registry",
	}

	addCmd := &cobra.Command{
		Use:   "add <name> <path-or-url>",
		Short: "Register a project",
		Long: `Register a project in the registry.

If path-or-url is a git URL (http://, https://, git@, ssh://),
the repository is cloned into the projects directory first.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, _ := cmd.Flags().GetString("mode")
			yolo, _ := cmd.Flags().GetBool("yolo")
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return project.Add(homeDir, args[0], args[1], mode, yolo)
		},
	}
	addCmd.Flags().String("mode", "", "Delivery mode (feat, fix, refactor, etc.)")
	addCmd.Flags().Bool("yolo", false, "Skip pre-flight checks")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			projects, err := project.List(homeDir)
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Println("No projects registered.")
				return nil
			}
			for _, p := range projects {
				fmt.Printf("- %s", p.Name)
				if p.Mode != "" {
					fmt.Printf(" [%s]", p.Mode)
				}
				if p.Yolo {
					fmt.Print(" +yolo")
				}
				fmt.Printf(" - %s (added %s)\n", p.Description, p.Added)
			}
			return nil
		},
	}

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show project details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			p, err := project.Find(homeDir, args[0])
			if err != nil {
				// Fall back to ad-hoc resolution
				adhoc, aerr := project.ResolveAdhoc()
				if aerr != nil {
					return err // return original not-found error
				}
				p = adhoc
				fmt.Printf("Name:        %s (ad-hoc)\n", p.Name)
				fmt.Printf("Repository:  %s\n", p.Description)
				return nil
			}
			fmt.Printf("Name:        %s\n", p.Name)
			if p.Mode != "" {
				fmt.Printf("Mode:        %s\n", p.Mode)
			}
			if p.Yolo {
				fmt.Println("Yolo:        true")
			}
			fmt.Printf("Description: %s\n", p.Description)
			fmt.Printf("Added:       %s\n", p.Added)

			// Show project dir if it exists
			projDir := filepath.Join(project.ProjectsDir(homeDir), p.Name)
			if fi, statErr := os.Stat(projDir); statErr == nil && fi.IsDir() {
				fmt.Printf("Directory:   %s\n", projDir)
			}
			return nil
		},
	}

	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a registered project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return project.Rm(homeDir, args[0])
		},
	}

	modeCmd := &cobra.Command{
		Use:   "mode <name>",
		Short: "Resolve delivery mode for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			mode, yolo, err := project.Mode(homeDir, args[0])
			if err != nil {
				// Fall back to ad-hoc
				adhoc, aerr := project.ResolveAdhoc()
				if aerr != nil {
					return err
				}
				mode = adhoc.Mode
				yolo = adhoc.Yolo
			}
			if mode != "" {
				fmt.Printf("%s", mode)
			}
			if yolo {
				if mode != "" {
					fmt.Print(" ")
				}
				fmt.Print("+yolo")
			}
			fmt.Println()
			return nil
		},
	}

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(rmCmd)
	cmd.AddCommand(modeCmd)
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
