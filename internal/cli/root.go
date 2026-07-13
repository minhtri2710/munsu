package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/teardown"
	"github.com/minhtri2710/munsu/internal/worktree"
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

	getCmd := &cobra.Command{
		Use:   "get <repo-path>",
		Short: "Acquire a pooled worktree",
		Long:  `Acquire a pooled worktree via treehouse. With --lease, pass through to treehouse for durable holds.`,
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := worktree.Status()
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(returnCmd)
	cmd.AddCommand(statusCmd)
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
		Args:  cobra.NoArgs,
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
		Args:  cobra.NoArgs,
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
		Args:  cobra.NoArgs,
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
		Args:  cobra.ExactArgs(2),
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
			}

			if err := task.WriteMeta(id, meta); err != nil {
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, _ := cmd.Flags().GetString("state")
			_ = state // filter not yet implemented for listing
			fmt.Println("task: list not yet implemented (use tasks-axi)")
			return nil
		},
	}
	listCmd.Flags().String("state", "", "Filter by state (in-flight|queued|done)")

	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			full, _ := cmd.Flags().GetBool("full")

			meta, err := task.ReadMeta(id)
			if err != nil {
				return err
			}

			fmt.Printf("Task: %s\n", id)
			fmt.Printf("---\n")
			for k, v := range meta {
				fmt.Printf("%s: %s\n", k, v)
			}

			if full {
				statusLines, err := task.ReadStatus(id)
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
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			state := args[1]
			msg := args[2]
			line := fmt.Sprintf("%s: %s", state, msg)
			if err := task.AppendStatus(id, line); err != nil {
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

	cmd := &cobra.Command{
		Use:   "brief <id> <repo>",
		Short: "Scaffold a task brief",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			repo := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Resolve delivery mode from project registry
			var mode string
			var yolo bool
			if m, y, err := project.Mode(homeDir, repo); err == nil {
				mode = m
				yolo = y
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

	return cmd
}

func newSpawnCmd() *cobra.Command {
	var (
		kind string
		mode string
		yolo bool
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a crewmate agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			projectName := args[1]

			// 1. Resolve home (used by task.WriteMeta internally)
			_, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			// 2. Acquire worktree
			wtPath, err := worktree.Get(projectName, false)
			if err != nil {
				return fmt.Errorf("acquiring worktree: %w", err)
			}

			// 3. Detect harness
			h, err := harness.Detect()
			if err != nil {
				return fmt.Errorf("detecting harness: %w", err)
			}

			// 4. Resolve model/effort from template
			var model, effort string
			if tmpl, ok := harness.Templates[h]; ok {
				model = tmpl.DefaultModel
				if tmpl.DefaultEffort != "" {
					effort = tmpl.DefaultEffort
				}
			}

			// 5. Create tmux window
			bk := session.Default()
			windowID, err := bk.NewWindow(h, id)
			if err != nil {
				return fmt.Errorf("creating session window: %w", err)
			}

			// 6. Write task meta
			yoloVal := "off"
			if yolo {
				yoloVal = "on"
			}
			meta := map[string]string{
				"window":   windowID,
				"worktree": wtPath,
				"project":  projectName,
				"harness":  h,
				"model":    model,
				"effort":   effort,
				"kind":     kind,
				"mode":     mode,
				"yolo":     yoloVal,
			}
			if err := task.WriteMeta(id, meta); err != nil {
				// Best-effort: print error but don't fail the spawn
				fmt.Fprintf(os.Stderr, "warning: writing task meta: %v\n", err)
			}

			// 7. Print endpoint info
			fmt.Printf("Spawned crewmate %s\n", id)
			fmt.Printf("  window:   %s\n", windowID)
			fmt.Printf("  worktree: %s\n", wtPath)
			fmt.Printf("  project:  %s\n", projectName)
			fmt.Printf("  harness:  %s\n", h)
			fmt.Printf("  model:    %s\n", model)
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

	return cmd
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <id> <line>",
		Short: "Send a line to a crewmate endpoint",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			line := args[1]

			// Read meta to resolve window
			meta, err := task.ReadMeta(id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk := session.Default()
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Read meta to resolve window
			meta, err := task.ReadMeta(id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk := session.Default()
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			state, err := crewstate.Read(id)
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
			return nil
		},
	}
}

func newPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a scout task to ship",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			if err := task.PromoteMeta(id); err != nil {
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
		Args:  cobra.ExactArgs(1),
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

Verbs: add, start, done, list, show, block, unblock, ready, hold, update, render.

Uses tasks-axi CLI when available (>= 0.1.1), falling back to
hand-editing $MUNSU_HOME/data/backlog.md.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verb := args[0]
			rest := args[1:]
			return runBacklog(verb, rest)
		},
	}
}

// runBacklog dispatches to tasks-axi if compatible, or fallback.
func runBacklog(verb string, args []string) error {
	// Probe for compatible tasks-axi
	if tasksAxiAvailable() {
		return runTasksAxi(verb, args)
	}

	return fmt.Errorf("backlog: tasks-axi not available and fallback backlog.md editing not yet implemented")
}

// tasksAxiAvailable checks if tasks-axi >= 0.1.1 is on PATH.
func tasksAxiAvailable() bool {
	path, err := exec.LookPath("tasks-axi")
	if err != nil {
		return false
	}

	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	version := strings.TrimSpace(string(out))
	return isCompatibleVersion(version, "0.1.1")
}

// isCompatibleVersion checks if the installed version is >= minimum.
// Simple semver comparison (major.minor.patch).
func isCompatibleVersion(installed, minimum string) bool {
	installParts := parseVersion(installed)
	minParts := parseVersion(minimum)

	for i := 0; i < 3; i++ {
		if installParts[i] > minParts[i] {
			return true
		}
		if installParts[i] < minParts[i] {
			return false
		}
	}
	return true
}

// parseVersion splits "x.y.z" into [major, minor, patch] ints.
func parseVersion(v string) [3]int {
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i, p := range parts {
		result[i] = atoi(p)
	}
	return result
}

// atoi parses an integer from a string, returning 0 on error.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// runTasksAxi runs tasks-axi with the given verb and args.
func runTasksAxi(verb string, args []string) error {
	path, err := exec.LookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}

	cliArgs := []string{verb}
	cliArgs = append(cliArgs, args...)

	cmd := exec.Command(path, cliArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
