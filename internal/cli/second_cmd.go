package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/second"
	"github.com/spf13/cobra"
)

func newSecondCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "second",
		Short: "Manage persistent domain supervisors (seconds)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "seed <id> <home-path>",
		Short: "Seed a second home with charter",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return second.Seed(args[0], args[1], "# Second charter\n\nPersistent domain supervisor.\n")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "launch <second-home>",
		Short: "Launch a second in its home (session-backed)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return second.Launch(args[0], ctx.Home)
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "retire <second-home>",
		Short: "Retire a second (session-backed)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return second.Retire(args[0], ctx.Home, false)
		}),
	})

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered seconds",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mates, err := second.List(ctx.Home)
			if err != nil {
				return err
			}
			if len(mates) == 0 {
				return writeContract(cmd, contract.Response[contract.EmptyResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "second.list",
					Status:        "success",
					Data:          contract.EmptyResult{Count: 0, Context: "No seconds registered."},
				})
			}
			var b strings.Builder
			for _, m := range mates {
				b.WriteString(fmt.Sprintf("- %s (%s; scope: %s; projects: %s; added: %s)\n", m.ID, m.Home, m.Scope, m.Project, m.Added))
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "second.list",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(listCmd)
	cmd.AddCommand(listCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "handoff <second-home> <item-key...>",
		Short: "Hand off backlog items to a second",
		Long: `Hand off queued backlog items from the parent home to a second.
All keys must be in queued state. Uses tasks-axi mv atomically.`,
		Args: MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return second.Handoff(ctx.Home, args[0], args[1:])
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config-push <second-home>",
		Short: "Push inheritable config to a second",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return second.ConfigPush(ctx.Home, args[0])
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "validate <second-home>",
		Short: "Validate a second home structure and provenance",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := second.Validate(args[0], ctx.Home); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			fmt.Println("valid")
			return nil
		}),
	})

	migrateCmd := &cobra.Command{
		Use:   "migrate <second-home> <id>",
		Short: "Migrate a seeded home (write provenance marker)",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return second.Migrate(args[0], args[1])
		},
	}
	cmd.AddCommand(migrateCmd)

	convergeCmd := &cobra.Command{
		Use:   "converge",
		Short: "Converge all registered seconds",
		Long: `Locked convergence sweep: validate registry/provenance, retry pending sends,
safe local fast-forward, inheritance push, liveness check, and instruction
surface tracking. State changes tracked in parent state/.second-converge.lock.`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			registered, err := second.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered seconds: %w", err)
			}
			return second.Converge(ctx.Home, registered)
		}),
	}
	cmd.AddCommand(convergeCmd)

	return cmd
}
