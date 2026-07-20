package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/turnend"
	"github.com/spf13/cobra"
)

func newTurnendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "turnend",
		Short: "Manage turn-end obligations (report relay, cleanup, etc.)",
		Long: `Turn-end obligations are role-specific actions that must be performed
at the end of a turn. They survive process restarts via durable state
under the munsu home.

Subcommands:
  obligations   List pending turn-end obligations for the current role
  complete      Mark an obligation as complete
  clear         Clear completed obligations`,
	}

	cmd.AddCommand(newTurnendObligationsCmd())
	cmd.AddCommand(newTurnendCompleteCmd())
	cmd.AddCommand(newTurnendClearCmd())

	return cmd
}

func newTurnendObligationsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "obligations",
		Short: "List pending turn-end obligations for the current role",
		Args:  cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			role := resolveRole()
			if role == "" {
				fmt.Println("MUNSU_ROLE not set — defaulting to soldier obligations")
				role = "soldier"
			}

			obligations, err := turnend.LoadObligations(ctx.Home, turnend.Role(role))
			if err != nil {
				return fmt.Errorf("loading obligations: %w", err)
			}

			if len(obligations) == 0 {
				fmt.Printf("No open turn-end obligations for role=%s\n", role)
				return nil
			}

			fmt.Printf("Turn-end obligations for role=%s:\n", role)
			for _, o := range obligations {
				state := "OPEN"
				if o.State == turnend.StateClosed {
					state = "closed"
				}
				fmt.Printf("  %s [%s] %s\n", o.Kind, state, o.Detail)
			}
			return nil
		}),
	}
}

func newTurnendCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "complete <kind>",
		Short: "Mark a turn-end obligation as complete",
		Long: `Mark a turn-end obligation as complete.

Valid kinds: report-relay, cleanup`,
		Args: cobra.ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			role := resolveRole()
			if role == "" {
				role = "soldier"
			}

			kind := turnend.ObligationKind(args[0])
			found, err := turnend.CompleteObligation(ctx.Home, turnend.Role(role), kind)
			if err != nil {
				return fmt.Errorf("completing obligation: %w", err)
			}
			if !found {
				fmt.Printf("No open obligation of kind %q found for role=%s\n", kind, role)
				return nil
			}
			fmt.Printf("Obligation %q marked as complete for role=%s\n", kind, role)
			return nil
		}),
	}
}

func newTurnendClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Clear completed turn-end obligations",
		Args:  cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			role := resolveRole()
			if role == "" {
				role = "soldier"
			}

			if err := turnend.ClearCompleted(ctx.Home, turnend.Role(role)); err != nil {
				return fmt.Errorf("clearing completed obligations: %w", err)
			}
			fmt.Printf("Completed obligations cleared for role=%s\n", role)
			return nil
		}),
	}
}

// resolveRole reads MUNSU_ROLE from the environment.
func resolveRole() string {
	return os.Getenv("MUNSU_ROLE")
}
