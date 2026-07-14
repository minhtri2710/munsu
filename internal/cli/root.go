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

const Version = "0.1.0-dev"

var (
	homeOverride string
)

func notImplementedE(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("%s: not yet implemented", cmd.Name())
}

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
		Args:  cobra.ExactArgs(3),
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
		kind    string
		mode    string
		yolo    bool
		backend string
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a crewmate agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			projectName := args[1]

			// 1. Resolve home (used by task.WriteMeta internally)
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			// 1b. Check for worktree tangle (unless yolo)
			if !yolo {
				projDir := filepath.Join(homeDir, "projects", projectName)
				if err := checkTangle(projDir, projectName); err != nil {
					return err
				}
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

			// 5. Create session window
			bk, bkName, err := session.Resolve(homeDir, backend)
			if err != nil {
				return err
			}
			windowID, err := bk.NewWindow(h, id)
			if err != nil {
				return fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", bkName, err)
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
			if err := task.WriteMeta(homeDir, id, meta); err != nil {
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
	cmd.Flags().StringVar(&backend, "backend", "", "Session backend (tmux|herdr)")

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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
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
		Long: `Compare the crewmate branch against the authoritative base and print
a Markdown diff summary.

For registered projects with a remote, compares against the default branch.
For PR tasks (where meta has pr=), fetches the PR head and compares.
Warns if local default branch is stale vs origin.`,
		Args: cobra.ExactArgs(1),
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
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			return delivery.PRCheck(homeDir, args[0], args[1])
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
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			extra := args[2:]
			return delivery.PRMerge(homeDir, args[0], args[1], extra)
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
		Args: cobra.ExactArgs(1),
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
		Use:   "add <id> <description>",
		Short: "Add a task to the backlog",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			desc := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			return backlog.AddItem(homeDir, id, desc, kind, repo, start)
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
		Args:  cobra.MaximumNArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.MaximumNArgs(1),
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

			// Check all registered projects for tangles
			projects, err := project.List(homeDir)
			if err == nil {
				for _, p := range projects {
					projDir := filepath.Join(homeDir, "projects", p.Name)
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
		Args: cobra.MinimumNArgs(0),
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
Adds the self-governance section if missing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir := args[0]
			res, err := agentsmd.Ensure(projectDir)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return secondmate.Seed(args[0], args[1], "# Secondmate charter\n\nPersistent domain supervisor.\n")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "launch <secondmate-home>",
		Short: "Launch a secondmate in its home",
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.MinimumNArgs(2),
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
		Args:  cobra.ExactArgs(1),
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
