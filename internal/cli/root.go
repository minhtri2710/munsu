package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/agentsmd"
	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/secondmate"
	"github.com/minhtri2710/munsu/internal/selfupdate"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/stow"
	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/teardown"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/minhtri2710/munsu/internal/worktree"
	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

var (
	homeOverride string
)


// checkTangle checks if the project's primary checkout at projectDir is on a
// non-default branch. Returns nil if HEAD is detached or on the default branch.
func checkTangle(projectDir, projectName string) error {
	// Check current HEAD state
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		// Can't determine branch state; skip tangle check
		return nil
	}
	branch := strings.TrimSpace(string(out))

	// Detached HEAD is the normal/expected state for worktree usage
	if branch == "HEAD" {
		return nil
	}

	// Get the default branch from origin/HEAD, with main/master fallback
	cmd = exec.Command("git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = projectDir
	out, err = cmd.Output()
	if err == nil {
		defaultRef := strings.TrimSpace(string(out))
		defaultBranch := strings.TrimPrefix(defaultRef, "origin/")

		// On the default branch = no tangle
		if branch == defaultBranch {
			return nil
		}
	} else {
		// Fall back to common default branch names when origin/HEAD unavailable
		foundDefault := false
		for _, candidate := range []string{"main", "master"} {
			chk := exec.Command("git", "rev-parse", "--verify", candidate)
			chk.Dir = projectDir
			if err := chk.Run(); err == nil {
				foundDefault = true
				// On the default branch = no tangle
				if branch == candidate {
					return nil
				}
				break
			}
		}
		if !foundDefault {
			// Can't determine default branch; skip tangle check
			return nil
		}
	}
	// Tangle detected: on a non-default branch in the primary checkout
	return fmt.Errorf("cannot spawn: %s is on branch %s, not an isolated worktree. Use a detached HEAD or a worktree",
		projectName, branch)
}
// buildHarnessLaunch builds the shell command to launch a harness agent.
func buildHarnessLaunch(h string, tmpl harness.Template) string {
	parts := []string{strings.ToLower(h)}
	parts = append(parts, tmpl.ExtraArgs...)
	if tmpl.ModelFlag != "" && tmpl.DefaultModel != "" {
		parts = append(parts, tmpl.ModelFlag, shellQuote(tmpl.DefaultModel))
	}
	if tmpl.EffortFlag != "" && tmpl.DefaultEffort != "" {
		parts = append(parts, tmpl.EffortFlag, tmpl.DefaultEffort)
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps a string in double quotes if it contains shell-special characters.
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\r()\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// validDeliveryModes lists the accepted delivery mode values.
var validDeliveryModes = map[string]bool{
	"no-mistakes": true,
	"direct-PR":   true,
	"local-only":  true,
}

// validateDeliveryMode returns an error if the mode is not a known value.
func validateDeliveryMode(mode string) error {
	if mode == "" {
		return nil // empty is allowed (will use registry default)
	}
	if !validDeliveryModes[mode] {
		return fmt.Errorf("invalid delivery mode %q: must be one of: no-mistakes, direct-PR, local-only", mode)
	}
	return nil
}


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
		Args:  ExactArgs(1),
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
		Args:  ExactArgs(2),
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
		Args: ExactArgs(2),
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
		Args:  NoArgs,
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
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			p, err := project.Find(homeDir, args[0])
			if err != nil {
				return err
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
		Args:  ExactArgs(1),
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
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			mode, yolo, err := project.Mode(homeDir, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s", mode)
			if yolo {
				fmt.Print(" +yolo")
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

	getCmd := &cobra.Command{
		Use:   "get <repo-path>",
		Short: "Acquire a pooled worktree",
		Long:  `Acquire a pooled worktree via treehouse. With --lease, pass through to treehouse for durable holds.`,
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, _ := cmd.Flags().GetBool("lease")
			path, err := worktree.Get(args[0], lease)
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
	getCmd.Flags().Bool("lease", false, "Acquire a durable lease hold")

	returnCmd := &cobra.Command{
		Use:   "return <path>",
		Short: "Return a worktree to the pool",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := worktree.Return(args[0]); err != nil {
				return err
			}
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show worktree pool status",
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := worktree.Status()
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}

	reclaimCmd := &cobra.Command{
		Use:   "reclaim",
		Short: "Reclaim orphaned worktrees not referenced by any task meta",
		Long: `List all treehouse-visible worktrees and return those not
referenced by any active task meta file. Use after crash recovery or
manual cleanup to release stale leases.

Leases should always be returned via "worktree return <path>" when a
crewmate finishes. This command is a safety net for orphaned leases.`,
		Args: NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Get all active worktree paths from task meta
			entries, err := task.ListMeta(homeDir)
			if err != nil {
				return fmt.Errorf("listing task meta: %w", err)
			}
			active := make(map[string]bool)
			for _, e := range entries {
				meta, err := task.ReadMeta(homeDir, e.ID)
				if err != nil {
					continue
				}
				if wt := meta["worktree"]; wt != "" {
					active[wt] = true
				}
			}

			// Get treehouse status and parse worktree list
			out, err := worktree.Status()
			if err != nil {
				return fmt.Errorf("getting treehouse status: %w", err)
			}

			// Return worktrees not in active set
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) == 0 {
					continue
				}
				wtPath := parts[len(parts)-1]
				if !active[wtPath] {
					fmt.Printf("returning orphaned worktree: %s\n", wtPath)
					if err := worktree.Return(wtPath); err != nil {
						fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					} else {
						count++
					}
				}
			}

			fmt.Printf("Reclaimed %d orphaned worktrees\n", count)
			return nil
		},
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(returnCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(reclaimCmd)
	return cmd
}

func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Detect and manage agent harness adapters",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "detect",
		Short: "Detect the running agent harness",
		Long:  `Detect the running agent harness using env markers first, then process ancestry.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := harness.Detect()
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "crew",
		Short: "Resolve crewmate harness",
		Long:  `Resolve the crewmate harness. Fallback chain: crew-dispatch.json default > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			h, err := harness.Crew(homeDir)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "secondmate",
		Short: "Resolve secondmate harness",
		Long:  `Resolve the secondmate harness. Fallback chain: config/secondmate-harness > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			h, err := harness.Secondmate(homeDir)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	return cmd
}

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage task lifecycle",
	}

	addCmd := &cobra.Command{
		Use:   "add <id> <description>",
		Short: "Add a new task to the backlog",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			desc := args[1]
			kind, _ := cmd.Flags().GetString("kind")
			repo, _ := cmd.Flags().GetString("repo")

			meta := map[string]string{
				"description": desc,
				"kind":        kind,
			}
			if repo != "" {
				meta["repo"] = repo
				meta["project"] = repo // --repo maps directly to the project name
			}


			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			if err := task.WriteMeta(homeDir, id, meta); err != nil {
				return err
			}
			fmt.Printf("task %s added\n", id)
			return nil
		},
	}
	addCmd.Flags().String("kind", "ship", "Task kind (ship|scout)")
	addCmd.Flags().String("repo", "", "Project repository name")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateFilter, _ := cmd.Flags().GetString("state")

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			entries, err := task.ListMeta(homeDir)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("no tasks found")
				return nil
			}

			fmt.Printf("%-20s %-8s %-16s %s\n", "ID", "KIND", "PROJECT", "STATUS")
			for _, e := range entries {
				if stateFilter != "" && !strings.Contains(e.LastStatus, stateFilter) {
					continue
				}
				project := e.Project
				if project == "" {
					project = "-"
				}
				status := e.LastStatus
				if status == "" {
					status = "registered"
				}
				fmt.Printf("%-20s %-8s %-16s %s\n", e.ID, e.Kind, project, status)
			}
			return nil
		},
	}
	listCmd.Flags().String("state", "", "Filter by state (in-flight|queued|done)")

	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			full, _ := cmd.Flags().GetBool("full")

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return err
			}

			fmt.Printf("Task: %s\n", id)
			fmt.Printf("---\n")
			for k, v := range meta {
				fmt.Printf("%s: %s\n", k, v)
			}

			if full {
				statusLines, err := task.ReadStatus(homeDir, id)
				if err == nil && len(statusLines) > 0 {
					fmt.Printf("---\nStatus:\n")
					for _, line := range statusLines {
						fmt.Printf("  %s\n", line)
					}
				}
			}
			return nil
		},
	}
	showCmd.Flags().Bool("full", false, "Show full details including status")

	statusCmd := &cobra.Command{
		Use:   "status <id> <state> <message>",
		Short: "Append a status line to a task",
		Args:  ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			state := args[1]
			msg := args[2]
			line := fmt.Sprintf("%s: %s", state, msg)

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			if err := task.AppendStatus(homeDir, id, line); err != nil {
				return err
			}
			fmt.Printf("status appended: %s\n", line)
			return nil
		},
	}

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(statusCmd)
	return cmd
}

func newBriefCmd() *cobra.Command {
	var scout bool
	var force bool
	var modeFlag string

	cmd := &cobra.Command{
		Use:   "brief <id> <repo>",
		Short: "Scaffold a task brief",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			repo := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Resolve delivery mode from project registry, or use --mode override
			var mode string
			var yolo bool
			if modeFlag != "" {
				mode = modeFlag
				if err := validateDeliveryMode(mode); err != nil {
					return err
				}
			} else if m, y, err := project.Mode(homeDir, repo); err == nil {
				mode = m
				yolo = y
			}


			// Require existing task meta unless --force
			if !force {
				if _, err := task.ReadMeta(homeDir, id); err != nil {
					return fmt.Errorf("task %q not found: create it with 'munsu task add %s ...' or use --force", id, id)
				}
			}
			opts := brief.ScaffoldOptions{
				HomeDir: homeDir,
				ID:      id,
				Repo:    repo,
				Scout:   scout,
				Mode:    mode,
				Yolo:    yolo,
			}

			if err := brief.Scaffold(opts); err != nil {
				return err
			}

			kind := "ship"
			if scout {
				kind = "scout"
			}
			fmt.Printf("Brief scaffolded at %s\n", brief.Path(homeDir, id))
			fmt.Printf("  id:    %s\n", id)
			fmt.Printf("  repo:  %s\n", repo)
			fmt.Printf("  kind:  %s\n", kind)
			if mode != "" {
				fmt.Printf("  mode:  %s\n", mode)
			}
			if yolo {
				fmt.Println("  yolo:  true")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&scout, "scout", false, "Generate a scout brief instead of ship brief")
	cmd.Flags().BoolVar(&force, "force", false, "Scaffold brief without requiring existing task meta")
	cmd.Flags().StringVar(&modeFlag, "mode", "", "Delivery mode override (no-mistakes|direct-PR|local-only)")

	return cmd
}

func newSpawnCmd() *cobra.Command {
	var (
		kind    string
		mode    string
		yolo    bool
		backend string
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a crewmate agent",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			projectName := args[1]

			// 1. Resolve home
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			// Validate delivery mode
			if err := validateDeliveryMode(mode); err != nil {
				return err
			}


			// Preflight: require brief to exist before spawning
			if !brief.Exists(homeDir, id) {
				return fmt.Errorf("no brief found for task %s: scaffold it with 'munsu brief %s %s' before spawning", id, id, projectName)
			}

			// Warn if tasks-axi available but no backlog row for this id
			if _, err := exec.LookPath("tasks-axi"); err == nil {
				// tasks-axi available, check for backlog row
				chk := exec.Command("tasks-axi", "show", id)
				if out, err := chk.CombinedOutput(); err != nil || strings.Contains(string(out), "not found") {
					fmt.Fprintf(os.Stderr, "warning: task %s has no backlog row; register it with 'backlog add %s --kind %s' to track lifecycle\n", id, id, kind)
				}
			}

			// 2. Resolve project repo path from registry
			projPath, err := project.ResolveRepoPath(homeDir, projectName)
			if err != nil {
				return fmt.Errorf("resolving project %q: %w", projectName, err)
			}

			// 3. Check for worktree tangle (unless yolo)
			if !yolo {
				if err := checkTangle(projPath, projectName); err != nil {
					return err
				}
			}

			// 4. Acquire leased worktree
			wtPath, err := worktree.Get(projPath, true)
			if err != nil {
				return fmt.Errorf("acquiring worktree: %w", err)
			}

			// 5. Resolve crewmate harness
			h, err := harness.Crew(homeDir)
			if err != nil {
				// Cleanup: return worktree
				_ = worktree.Return(wtPath)
				return fmt.Errorf("resolving harness: %w", err)
			}

			// 6. Resolve model/effort from template
			var model, effort string
			var launchCmd string
			if tmpl, ok := harness.Templates[h]; ok {
				model = tmpl.DefaultModel
				if tmpl.DefaultEffort != "" {
					effort = tmpl.DefaultEffort
				}
				launchCmd = buildHarnessLaunch(h, tmpl)
			}

			// 7. Create session window
			bk, bkName, err := session.Resolve(homeDir, backend)
			if err != nil {
				_ = worktree.Return(wtPath)
				return err
			}
			windowID, err := bk.NewWindow("munsu", id)
			if err != nil {
				_ = worktree.Return(wtPath)
				return fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", bkName, err)
			}

			// 8. Bootstrap window: cd to worktree and launch harness
			if launchCmd != "" {
				// Send cd + harness launch
				fullCmd := fmt.Sprintf("cd %s && %s", wtPath, launchCmd)
				if sendErr := bk.SendKeys(windowID, fullCmd); sendErr != nil {
					// Non-fatal: log but don't fail the spawn
					fmt.Fprintf(os.Stderr, "warning: sending harness launch command: %v\n", sendErr)
				}
			}

			// 9. Inject brief content
			briefPath := brief.Path(homeDir, id)
			if briefData, readErr := os.ReadFile(briefPath); readErr == nil {
				// Write brief into worktree for agent file access
				briefWorktreePath := filepath.Join(wtPath, ".crew-brief.md")
				if writeErr := os.WriteFile(briefWorktreePath, briefData, 0644); writeErr != nil {
					fmt.Fprintf(os.Stderr, "warning: writing brief to worktree: %v\n", writeErr)
				}
				// Inject full brief as single paste (one SendKeys call instead of N)
				_ = bk.SendKeys(windowID, string(briefData))
			} else if !os.IsNotExist(readErr) {
				fmt.Fprintf(os.Stderr, "warning: reading brief: %v\n", readErr)
			}

			// 10. Write task meta
			yoloVal := "off"
			if yolo {
				yoloVal = "on"
			}
			meta := map[string]string{
				"window":   windowID,
				"worktree": wtPath,
				"project":  projectName,
				"projpath": projPath,
				"harness":  h,
				"backend":  bkName,
				"kind":     kind,
				"mode":     mode,
				"yolo":     yoloVal,
			}
			if model != "" {
				meta["model"] = model
			}
			if effort != "" {
				meta["effort"] = effort
			}
			if err := task.WriteMeta(homeDir, id, meta); err != nil {
				// Best-effort: print error but don't fail the spawn
				fmt.Fprintf(os.Stderr, "warning: writing task meta: %v\n", err)
			}

			// 11. Append working: spawned status
			_ = task.AppendStatus(homeDir, id, "working: spawned")

			// 12. Print endpoint info
			fmt.Printf("Spawned crewmate %s\n", id)
			fmt.Printf("  window:   %s\n", windowID)
			fmt.Printf("  worktree: %s\n", wtPath)
			fmt.Printf("  projpath: %s\n", projPath)
			fmt.Printf("  project:  %s\n", projectName)
			fmt.Printf("  harness:  %s\n", h)
			if model != "" {
				fmt.Printf("  model:    %s\n", model)
			}
			if effort != "" {
				fmt.Printf("  effort:   %s\n", effort)
			}
			fmt.Printf("  kind:     %s\n", kind)
			fmt.Printf("  mode:     %s\n", mode)
			fmt.Printf("  yolo:     %s\n", yoloVal)
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout)")
	cmd.Flags().StringVar(&mode, "mode", "no-mistakes", "Delivery mode (no-mistakes|direct-PR|local-only)")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "Skip pre-flight checks")
	cmd.Flags().StringVar(&backend, "backend", "", "Session backend (tmux|herdr)")

	return cmd
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <id> <line>",
		Short: "Send a line to a crewmate endpoint",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			line := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Read meta to resolve window
			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk, _, err := session.Resolve(homeDir, "")
			if err != nil {
				return err
			}
			if err := bk.SendKeys(windowID, line); err != nil {
				return fmt.Errorf("sending to %s: %w", id, err)
			}
			fmt.Printf("sent to %s: %s\n", id, line)
			return nil
		},
	}
	return cmd
}

func newPeekCmd() *cobra.Command {
	var lines int

	cmd := &cobra.Command{
		Use:   "peek <id>",
		Short: "Peek at crewmate output",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Read meta to resolve window
			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk, _, err := session.Resolve(homeDir, "")
			if err != nil {
				return err
			}
			out, err := bk.Capture(windowID, lines)
			if err != nil {
				return fmt.Errorf("capturing from %s: %w", id, err)
			}

			// Print with a header showing lines count
			count := strings.Count(out, "\n")
			if out != "" && out[len(out)-1] == '\n' && count > 0 {
				// tmux output often ends with newline; don't overcount
				count--
			}
			if count == 0 && out != "" {
				count = 1
			}
			fmt.Printf("--- %s (captured %d lines) ---\n", id, count)
			fmt.Print(out)
			if out != "" && out[len(out)-1] != '\n' {
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&lines, "lines", 40, "Number of lines to capture")

	return cmd
}

func newCrewStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "crew-state <id>",
		Short: "Read crewmate current state",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			state, err := crewstate.Read(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading crew state: %w", err)
			}

			fmt.Printf("Task:  %s\n", state.TaskID)
			fmt.Printf("State: %s\n", state.Status)
			fmt.Printf("Info:  %s\n", state.Description)
			fmt.Printf("Pane:  ")
			if state.PaneAlive {
				fmt.Println("alive")
			} else {
				fmt.Println("gone")
			}
			if state.StatusLines > 0 {
				fmt.Printf("Log:   %d status lines\n", state.StatusLines)
			}

			if state.NoMistakesRunStep != "" {
				fmt.Printf("Run:   no-mistakes: %s\n", state.NoMistakesRunStep)
			}
			if state.StatusLogSuperseded {
				fmt.Println("Note:  status log superseded by no-mistakes run-step")
			}
			return nil
		},
	}
}

func newPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a scout task to ship",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Preflight: verify task meta exists with kind=scout
			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "scout" {
				return fmt.Errorf("task %s has kind=%q, can only promote kind=scout", id, meta["kind"])
			}

			// Preflight: require report.md to exist
			if !brief.ReportExists(homeDir, id) {
				return fmt.Errorf("no report found for scout task %s: write report at %s before promoting", id, brief.ReportPath(homeDir, id))
			}

			// Preflight: require last status to be done or resolved
			if statusLines, err := task.ReadStatus(homeDir, id); err == nil && len(statusLines) > 0 {
				lastLine := statusLines[len(statusLines)-1]
				lastStatus, _ := task.ParseStatusKey(lastLine)
				if !strings.HasPrefix(lastStatus, "done") && !strings.HasPrefix(lastStatus, "resolved") {
					return fmt.Errorf("task %s has last status %q, need 'done' or 'resolved' before promote", id, lastStatus)
				}
			} else {
				return fmt.Errorf("task %s has no status: report done or resolved before promoting", id)
			}

			if err := task.PromoteMeta(homeDir, id); err != nil {
				return fmt.Errorf("promote %s: %w", id, err)
			}

			fmt.Printf("Task %s promoted from scout to ship\n", id)
			return nil
		},
	}
}

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Tear down a crewmate",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			opts := teardown.Options{
				HomeDir: homeDir,
				ID:      id,
				Force:   force,
			}

			result, err := teardown.Run(opts)
			if err != nil {
				return err
			}

			fmt.Printf("Teardown %s completed:\n", id)
			for _, step := range result.Steps {
				fmt.Printf("  - %s\n", step)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip safety checks")

	return cmd
}

func newReviewDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review-diff <id>",
		Short: "Review diff between crewmate branch and base",
		Long: `Compare the crewmate branch against the authoritative base and print
a Markdown diff summary.

For registered projects with a remote, compares against the default branch.
For PR tasks (where meta has pr=), fetches the PR head and compares.
Warns if local default branch is stale vs origin.`,
		Args: ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return delivery.ReviewDiff(homeDir, args[0])
		},
	}
}

func newPRCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-check <id> <pr-url>",
		Short: "Record PR URL and arm merge poll",
		Long: `Parse a full GitHub PR URL, record the PR and head SHA in task meta,
and write a check.sh script to poll the PR merge status via gh CLI.

PR URL format: https://github.com/<owner>/<repo>/pull/<n>`,
		Args: ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			prURL := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Preflight: verify task meta exists with kind=ship
			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "ship" {
				return fmt.Errorf("task %s has kind=%q, pr-check requires kind=ship (promote scout tasks before checking PRs)", id, meta["kind"])
			}

			return delivery.PRCheck(homeDir, id, prURL)
		},
	}
}

func newPRMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr-merge <id> <pr-url> [-- --merge|--rebase]",
		Short: "Merge a PR via gh-axi",
		Long: `Merge a PR via gh-axi CLI. Repository is derived from the PR URL.
Default merge method is squash.

Use -- --merge or -- --rebase to override the merge method.
The --repo/-R flag is not allowed (repository comes from the URL).

PR URL format: https://github.com/<owner>/<repo>/pull/<n>`,
		Args: MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			prURL := args[1]
			extra := args[2:]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Preflight: verify task meta exists with kind=ship
			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "ship" {
				return fmt.Errorf("task %s has kind=%q, pr-merge requires kind=ship (promote scout tasks before merging)", id, meta["kind"])
			}

			return delivery.PRMerge(homeDir, id, prURL, extra)
		},
}
	return cmd
}

func newMergeLocalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge-local <id>",
		Short: "Fast-forward merge to local default branch",
		Long: `Fast-forward merge the crewmate branch into the local default branch.
Only works for local-only mode projects (no remote).
Refuses if the merge is not a clean fast-forward.`,
		Args: ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return delivery.MergeLocal(homeDir, args[0])
		},
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

func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Manage the task backlog",
		Long: `Manage the task backlog via the configured backlog backend.

Subcommands: add, list, show, start, done, block, ready, unblock.

Uses tasks-axi CLI when available (>= 0.1.1), falling back to
hand-editing $MUNSU_HOME/data/backlog.md.

Use --home to scope backlog data to a specific home directory.
When --home is a non-default path, the manual backend is forced to prevent data leaks.`,
	}

	cmd.AddCommand(newBacklogAddCmd())
	cmd.AddCommand(newBacklogListCmd())
	cmd.AddCommand(newBacklogShowCmd())
	cmd.AddCommand(newBacklogStartCmd())
	cmd.AddCommand(newBacklogDoneCmd())
	cmd.AddCommand(newBacklogBlockCmd())
	cmd.AddCommand(newBacklogReadyCmd())
	cmd.AddCommand(newBacklogUnblockCmd())

	return cmd
}

func newBacklogAddCmd() *cobra.Command {
	var kind string
	var repo string
	var start bool

	cmd := &cobra.Command{
		Use:   `add <id> "<description>"`,
		Short: "Add a task to the backlog",
		Long: `Add a task to the backlog.

The description must be quoted if it contains multiple words.
Example:
  munsu backlog add flow-r2 "Flow retest scout"
`,
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			desc := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			if err := backlog.AddItemDispatch(homeDir, id, desc, kind, repo, start); err != nil {
				return err
			}
			// When --start is set, also register the task meta so it appears in fleet state.
			if start {
				meta := map[string]string{
					"description": desc,
					"kind":        kind,
				}
				if repo != "" {
					meta["repo"] = repo
					meta["project"] = repo
				}
				if err := task.WriteMeta(homeDir, id, meta); err != nil {
					// Non-fatal: log but don't fail the backlog add
					fmt.Fprintf(os.Stderr, "warning: writing task meta for %s: %v\n", id, err)
				}
			}
			return nil

		},
	}

	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout|task)")
	cmd.Flags().StringVar(&repo, "repo", "", "Project repository name")
	cmd.Flags().BoolVar(&start, "start", false, "Start task immediately (set state to in-flight)")

	return cmd
}

func newBacklogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [state-filter]",
		Short: "List backlog items",
		Args:  MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "list", args)
			}
			return backlog.RunManual(homeDir, "list", args)
		},
	}
}

func newBacklogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show backlog item details",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "show", args)
			}
			return backlog.RunManual(homeDir, "show", args)
		},
	}
}

func newBacklogStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a backlog item (mark in-flight)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "start", args)
			}
			return backlog.RunManual(homeDir, "start", args)
		},
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "done", args)
			}
			return backlog.RunManual(homeDir, "done", args)
		},
	}
}

func newBacklogBlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "block <id>",
		Short: "Block a backlog item",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "block", args)
			}
			return backlog.RunManual(homeDir, "block", args)
		},
	}
}

func newBacklogReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready <id>",
		Short: "Unblock a backlog item (mark ready)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "ready", args)
			}
			return backlog.RunManual(homeDir, "ready", args)
		},
	}
}

func newBacklogUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Alias for ready (unblock a backlog item)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "unblock", args)
			}
			return backlog.RunManual(homeDir, "unblock", args)
		},
	}
}

func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Lock, bootstrap, and print session-start digest (Context, Fleet State, Supervision)",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			_, err = session.RunSessionStart(homeDir)
			return err
		},
	}
}

func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap [install <tools>...]",
		Short: "Detect toolchain and run setup sweeps",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			locked := lifecycle.IsSessionLocked(homeDir)
			var installTools []string
			if len(args) > 1 && args[0] == "install" {
				installTools = args[1:]
			}
			result, err := bootstrap.Run(homeDir, !locked, installTools)
			if err != nil {
				return err
			}
			for _, d := range result.Diagnostics {
				fmt.Println(d)
			}
			for _, c := range result.ConfigDetails {
				fmt.Println(c)
			}
			return nil
		},
	}
}

func newFleetSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-sync [<project>]",
		Short: "Fast-forward refresh project clones",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}
			result, err := fleet.Sync(homeDir, projectName)
			if err != nil {
				return err
			}
			for _, s := range result.Synced {
				fmt.Printf("synced: %s\n", s)
			}
			for _, s := range result.Stuck {
				fmt.Printf("STUCK: %s\n", s)
			}
			for _, e := range result.Errors {
				fmt.Printf("error: %s\n", e)
			}
			return nil
		},
	}
}

func newFleetSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-snapshot",
		Short: "Emit fleet snapshot JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			snap, err := fleet.Snapshot(homeDir)
			if err != nil {
				return err
			}
			j, err := snap.JSON()
			if err != nil {
				return err
			}
			fmt.Println(j)
			return nil
		},
	}
}

func newFleetViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-view",
		Short: "Render fleet view from snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return fleet.View(homeDir)
		},
	}
}

func newBearingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bearings",
		Short: "Compact resume report",
		Args:  MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			projectDir := ""
			if len(args) > 0 {
				projectDir = args[0]
			}
			return fleet.Bearings(homeDir, projectDir)
		},
	}
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Run the event-driven watcher",
		Long:  `Run the event-driven watcher loop. Exits with a wake reason when an actionable event is found. Singleton-safe (home-scoped lock).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			reason, err := supervision.Run(homeDir)
			if err != nil {
				return err
			}
			if reason != nil {
				fmt.Printf("wake: %s — %s\n", reason.Kind, reason.Message)
			}
			return nil
		},
	}
}

func newWatchArmCmd() *cobra.Command {
	var restart bool
	cmd := &cobra.Command{
		Use:   "watch-arm",
		Short: "Arm the watcher (home-scoped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return supervision.ArmBackground(homeDir, restart)
		},
	}
	cmd.Flags().BoolVar(&restart, "restart", false, "Restart existing watcher before arming")
	return cmd
}

func newWakeDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake-drain",
		Short: "Drain queued wakes",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			records, err := waker.Drain(homeDir)
			if err != nil {
				return err
			}
			waker.PrintRecords(records)
			return nil
		},
	}
}

func newGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Warn on tangle or stale watcher",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			waker.CheckGuard(homeDir)

			// Check all registered projects for tangles using resolved paths
			projects, err := project.List(homeDir)
			if err == nil {
				for _, p := range projects {
					projDir, resolveErr := project.ResolveRepoPath(homeDir, p.Name)
					if resolveErr != nil {
						continue // skip unresolvable projects
					}
					if err := checkTangle(projDir, p.Name); err != nil {
						w := err.Error()
						border := strings.Repeat("●", len(w)+4)
						fmt.Println(border)
						fmt.Println("● " + w + " ●")
						fmt.Println(border)
					}
				}
			}

			return nil
		},
	}
}

func newStowCmd() *cobra.Command {
	var kind string
	var captain bool

	cmd := &cobra.Command{
		Use:   "stow [text...]",
		Short: "Sweep session for durable knowledge",
		Long: `Capture durable learnings or captain preferences from the current session.

Flags:
  --kind learning|captain   which file to stow (default: learning)
  --captain                  shorthand for --kind captain

Entries are inspect-then-update: if a new entry matches an existing
entry (by substring), the existing entry is replaced in place rather
than appended. Non-matching entries are appended as usual.

Examples:
  munsu stow "Go 1.26 uses range-over-func"
  munsu stow --captain "Prefer simple project layouts"
  munsu stow --kind captain "Prefer simple layouts"
`,
		Args: MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if captain {
				kind = stow.KindCaptain
			}
			if kind == "" {
				kind = stow.KindLearning
			}

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			res, err := stow.RunKinded(homeDir, kind, args)
			if err != nil {
				return err
			}

			switch {
			case res.DataLearnings != "":
				fmt.Printf("Stowed learnings to %s\n", res.DataLearnings)
			case res.DataCaptain != "":
				fmt.Printf("Stowed captain preferences to %s\n", res.DataCaptain)
			default:
				fmt.Println("Nothing to stow (no text provided)")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Kind of stow entry (learning|captain)")
	cmd.Flags().BoolVar(&captain, "captain", false, "Shorthand for --kind captain")
	return cmd
}

func newEnsureAgentsMdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure-agents-md <project>",
		Short: "Ensure project AGENTS.md and CLAUDE.md symlink",
		Long: `Create or update AGENTS.md and CLAUDE.md symlink for a project.
Adds the self-governance section if missing.

The <project> argument can be a project name (resolved from the registry)
or an absolute path to a project directory.`,
		Args: ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectArg := args[0]

			// Resolve project name to path if not already an absolute path
			projectDir := projectArg
			if !filepath.IsAbs(projectArg) {
				homeDir, err := home.Resolve(homeOverride)
				if err != nil {
					return fmt.Errorf("resolving home: %w", err)
				}
				resolved, err := project.ResolveRepoPath(homeDir, projectArg)
				if err != nil {
					return fmt.Errorf("resolving project %q: %w", projectArg, err)
				}
				projectDir = resolved
			}

			res, err := agentsmd.Ensure(projectDir, false)
			if err != nil {
				return err
			}
			fmt.Printf("Ensured AGENTS.md at %s\n", res.AGENTSMD)
			if res.CLAUDEMDSym != "" {
				fmt.Printf("Created CLAUDE.md symlink at %s\n", res.CLAUDEMDSym)
			}
			if res.SelfGovernSec {
				fmt.Println("Added '## Maintaining this file' section")
			}
			return nil
		},
	}
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Self-update munsu",
		RunE: func(cmd *cobra.Command, args []string) error {
			return selfupdate.Update()
		},
	}
}

func newSecondmateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secondmate",
		Short: "Manage persistent domain supervisors (secondmates)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "seed <id> <home-path>",
		Short: "Seed a secondmate home with charter",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return secondmate.Seed(args[0], args[1], "# Secondmate charter\n\nPersistent domain supervisor.\n")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "launch <secondmate-home>",
		Short: "Launch a secondmate in its home",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return secondmate.Launch(args[0], homeDir)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "retire <secondmate-home>",
		Short: "Retire a secondmate",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return secondmate.Retire(args[0], false)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered secondmates",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			mates, err := secondmate.List(homeDir)
			if err != nil {
				return err
			}
			for _, m := range mates {
				fmt.Printf("- %s (%s)\n", m.ID, m.Home)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "handoff <secondmate-home> <item-key...>",
		Short: "Hand off backlog items to a secondmate",
		Args:  MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return secondmate.Handoff(homeDir, args[0], args[1:])
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config-push <secondmate-home>",
		Short: "Push inheritable config to a secondmate",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return secondmate.ConfigPush(homeDir, args[0])
		},
	})

	return cmd
}

func newAfkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "afk",
		Short: "Enter away-mode supervision",
		Long: `Start the away-mode sub-supervisor daemon.

The daemon sets the AFK flag and polls the fleet at a reduced cadence.
Captain-relevant events (done/failed/needs-decision) are printed.

Stop with SIGTERM/SIGINT. The flag is cleared on stop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return afk.Start(homeDir)
		},
}
}
