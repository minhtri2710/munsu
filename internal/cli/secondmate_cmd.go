package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/secondmate"
	"github.com/spf13/cobra"
)

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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return secondmate.Launch(args[0], ctx.Home)
		}),
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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mates, err := secondmate.List(ctx.Home)
			if err != nil {
				return err
			}
			if len(mates) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No secondmates registered.")
				return nil
			}
			for _, m := range mates {
				fmt.Fprintf(cmd.OutOrStdout(), "- %s (%s)\n", m.ID, m.Home)
			}
			return nil
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "handoff <secondmate-home> <item-key...>",
		Short: "Hand off backlog items to a secondmate",
		Args:  MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return secondmate.Handoff(ctx.Home, args[0], args[1:])
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config-push <secondmate-home>",
		Short: "Push inheritable config to a secondmate",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return secondmate.ConfigPush(ctx.Home, args[0])
		}),
	})

	return cmd
}
