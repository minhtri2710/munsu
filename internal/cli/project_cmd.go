package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/project"
	"github.com/spf13/cobra"
)

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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mode, _ := cmd.Flags().GetString("mode")
			yolo, _ := cmd.Flags().GetBool("yolo")
			return project.Add(ctx.Home, args[0], args[1], mode, yolo)
		}),
	}
	addCmd.Flags().String("mode", "", "Delivery mode (feat, fix, refactor, etc.)")
	addCmd.Flags().Bool("yolo", false, "Skip pre-flight checks")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projects, err := project.List(ctx.Home)
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
		}),
	}

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show project details",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			p, err := project.Find(ctx.Home, args[0])
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
			projDir := filepath.Join(project.ProjectsDir(ctx.Home), p.Name)
			if fi, statErr := os.Stat(projDir); statErr == nil && fi.IsDir() {
				fmt.Printf("Directory:   %s\n", projDir)
			}
			return nil
		}),
	}

	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a registered project",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return project.Rm(ctx.Home, args[0])
		}),
	}

	modeCmd := &cobra.Command{
		Use:   "mode <name>",
		Short: "Resolve delivery mode for a project",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mode, yolo, err := project.Mode(ctx.Home, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s", mode)
			if yolo {
				fmt.Print(" +yolo")
			}
			fmt.Println()
			return nil
		}),
	}

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(rmCmd)
	cmd.AddCommand(modeCmd)
	return cmd
}
