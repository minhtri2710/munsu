package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/teardown"
	"github.com/spf13/cobra"
)

func newSpawnCmd() *cobra.Command {
	var (
		kind        string
		mode        string
		yolo        bool
		backend     string
		harnessFlag string
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a crewmate agent",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := spawn.Run(spawn.Args{
				ID:           args[0],
				ProjectName:  args[1],
				Kind:         kind,
				Mode:         mode,
				Yolo:         yolo,
				Backend:      backend,
				HarnessFlag:  harnessFlag,
				HomeDir:      homeOverride,
			})
			return err
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout)")
	cmd.Flags().StringVar(&mode, "mode", "no-mistakes", "Delivery mode (no-mistakes|direct-PR|local-only)")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "Skip pre-flight checks")
	cmd.Flags().StringVar(&backend, "backend", "", "Session backend (tmux|herdr)")
	cmd.Flags().StringVar(&harnessFlag, "harness", "", "Override crewmate harness (pi, agy, etc.)")

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
