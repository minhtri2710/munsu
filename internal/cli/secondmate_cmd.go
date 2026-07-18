package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
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
		Short: "Launch a secondmate in its home (session-backed)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return secondmate.Launch(args[0], ctx.Home)
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "retire <secondmate-home>",
		Short: "Retire a secondmate (session-backed)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return secondmate.Retire(args[0], ctx.Home, false)
		}),
	})

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered secondmates",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mates, err := secondmate.List(ctx.Home)
			if err != nil {
				return err
			}
			if len(mates) == 0 {
				return writeContract(cmd, contract.Response[contract.EmptyResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "secondmate.list",
					Status:        "success",
					Data:          contract.EmptyResult{Count: 0, Context: "No secondmates registered."},
				})
			}
			var b strings.Builder
			for _, m := range mates {
				b.WriteString(fmt.Sprintf("- %s (%s; scope: %s; projects: %s; added: %s)\n", m.ID, m.Home, m.Scope, m.Project, m.Added))
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "secondmate.list",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(listCmd)
	cmd.AddCommand(listCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "handoff <secondmate-home> <item-key...>",
		Short: "Hand off backlog items to a secondmate",
		Long: `Hand off queued backlog items from the parent home to a secondmate.
All keys must be in queued state. Uses tasks-axi mv atomically.`,
		Args: MinimumNArgs(2),
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

	cmd.AddCommand(&cobra.Command{
		Use:   "validate <secondmate-home>",
		Short: "Validate a secondmate home structure and provenance",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := secondmate.Validate(args[0], ctx.Home); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			fmt.Println("valid")
			return nil
		}),
	})

	migrateCmd := &cobra.Command{
		Use:   "migrate <secondmate-home> <id>",
		Short: "Migrate a seeded home (write provenance marker)",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return secondmate.Migrate(args[0], args[1])
		},
	}
	cmd.AddCommand(migrateCmd)

	convergeCmd := &cobra.Command{
		Use:   "converge",
		Short: "Converge all registered secondmates",
		Long: `Locked convergence sweep: validate registry/provenance, retry pending sends,
safe local fast-forward, inheritance push, liveness check, and instruction
surface tracking. State changes tracked in parent state/.secondmate-converge.lock.`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			registered, err := secondmate.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered secondmates: %w", err)
			}
			return secondmate.Converge(ctx.Home, registered)
		}),
	}
	cmd.AddCommand(convergeCmd)

	return cmd
}
