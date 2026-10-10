package cli

import (
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/fleet"
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
			yolo, _ := cmd.Flags().GetBool("yolo")
			return fleet.Add(ctx.Home, args[0], args[1], yolo)
		}),
	}
	addCmd.Flags().Bool("yolo", false, "Skip pre-flight checks")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projects, err := fleet.List(ctx.Home)
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				return writeContract(cmd, Response[EmptyResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "project.list",
					Status:        "success",
					Data:          EmptyResult{Count: 0, Context: "No projects registered."},
				})
			}
			entries := make([]ProjectEntry, len(projects))
			for i, p := range projects {
				entries[i] = ProjectEntry{
					Name:        p.Name,
					Yolo:        p.Yolo,
					Description: p.Description,
					Added:       p.Added,
				}
			}
			return writeContract(cmd, Response[[]ProjectEntry]{
				SchemaVersion: SchemaVersion,
				Kind:          "project.list",
				Status:        "success",
				Data:          entries,
				Help:          []string{"Run `munsu project show <name>` for details"},
			})
		}),
	}
	configureContractCommand(listCmd)

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show project details",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			p, err := fleet.Find(ctx.Home, args[0])
			if err != nil {
				return err
			}
			entry := ProjectEntry{
				Name:        p.Name,
				Yolo:        p.Yolo,
				Description: p.Description,
				Added:       p.Added,
			}
			// Show project dir if it exists
			projDir := filepath.Join(fleet.ProjectsDir(ctx.Home), p.Name)
			if fi, statErr := os.Stat(projDir); statErr == nil && fi.IsDir() {
				entry.Directory = projDir
			}
			return writeContract(cmd, Response[ProjectEntry]{
				SchemaVersion: SchemaVersion,
				Kind:          "project.show",
				Status:        "success",
				Data:          entry,
			})
		}),
	}
	configureContractCommand(showCmd)

	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a registered project",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.Rm(ctx.Home, args[0])
		}),
	}

	modeCmd := &cobra.Command{
		Use:   "mode <name>",
		Short: "Show the delivery mode derived from a project's configured tools",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			p, err := fleet.Find(ctx.Home, args[0])
			if err != nil {
				return err
			}
			mode, _, err := fleet.ResolveBriefProject(ctx.Home, args[0], true)
			if err != nil {
				return err
			}
			msg := mode
			if p.Yolo {
				msg += " +yolo"
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "project.mode",
				Status:        "success",
				Data:          MessageResult{Message: msg},
			})
		}),
	}
	configureContractCommand(modeCmd)

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(rmCmd)
	cmd.AddCommand(modeCmd)
	cmd.AddCommand(newProjectConfigCmd())
	return cmd
}
