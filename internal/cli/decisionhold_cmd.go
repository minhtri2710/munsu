package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/spf13/cobra"
)

func newDecisionHoldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision-hold",
		Short: "Manage durable general decision holds",
		Long: `Manage the decision-hold lifecycle for unresolved general decisions
discovered during investigations or reviews.

Subcommands: hold, complete, verify, resolve, list.

Each decision gets one stable key. The hold remains open until the
general's answer is recorded and any dependent work is unblocked.`,
	}

	cmd.AddCommand(newDecisionHoldHoldCmd())
	cmd.AddCommand(newDecisionHoldCompleteCmd())
	cmd.AddCommand(newDecisionHoldVerifyCmd())
	cmd.AddCommand(newDecisionHoldResolveCmd())
	cmd.AddCommand(newDecisionHoldListCmd())

	return cmd
}

func newDecisionHoldHoldCmd() *cobra.Command {
	var reason string
	var from string

	cmd := &cobra.Command{
		Use:   "hold <key> --reason <summary> --from <task-id>",
		Short: "Record a new general decision hold",
		Long: `Record a new general decision hold.

Creates a durable hold that blocks dependent work until the general
resolves the decision. Idempotent: running with the same key and
origin task is a no-op.

Example:
  munsu decision-hold hold approach --reason "Pick the UI framework" --from scout-r2`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			if reason == "" {
				return fmt.Errorf("--reason is required")
			}
			if from == "" {
				return fmt.Errorf("--from is required")
			}

			result, err := decisionhold.Create(ctx.Home, from, key, reason)
			if err != nil {
				return fmt.Errorf("creating hold: %w", err)
			}

			if result.Created {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "decision-hold.hold",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("Hold %s created on %s", result.HoldID, from)},
				})
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "decision-hold.hold",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("Hold %s already exists on %s (idempotent)", result.HoldID, from), Noop: true},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().StringVar(&reason, "reason", "", "One-line summary of the decision needed")
	cmd.Flags().StringVar(&from, "from", "", "Originating task ID that discovered this decision")

	return cmd
}

func newDecisionHoldCompleteCmd() *cobra.Command {
	var none bool

	cmd := &cobra.Command{
		Use:   "complete <origin-id> [<key>...]",
		Short: "Mark decisions as complete",
		Long: `Mark decisions discovered during an investigation or review as complete.

Accepts one or more decision keys. Use --none to attest that the reviewed
surface has no unresolved general decisions.

Examples:
  munsu decision-hold complete scout-r2 approach db-schema
  munsu decision-hold complete scout-r2 --none`,
		Args: MinimumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]
			keys := args[1:]

			if none {
				if len(keys) > 0 {
					return fmt.Errorf("--none cannot be combined with explicit keys")
				}
				keys = []string{"--none"}
			}

			if len(keys) == 0 {
				return fmt.Errorf("specify at least one key or --none")
			}

			if err := decisionhold.Complete(ctx.Home, originID, keys); err != nil {
				return fmt.Errorf("completing decision holds: %w", err)
			}

			if len(keys) == 1 && keys[0] == "--none" {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "decision-hold.complete",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("Attested no pending decisions for %s", originID)},
				})
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "decision-hold.complete",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("Completed %d decision hold(s) for %s: %s", len(keys), originID, strings.Join(keys, ", "))},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&none, "none", false, "Attest that no unresolved decisions exist")

	return cmd
}

func newDecisionHoldVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <origin-id> [<key>...]",
		Short: "Verify no stale needs-decision holds remain",
		Long: `Verify that the originating task has no stale needs-decision status lines.

When keys are provided, only those keys are checked. Without keys, all
holds for the origin task are checked.

Exit codes: 0 = clean, 1 = unresolved decisions found, 2 = error.

Example:
  munsu decision-hold verify scout-r2`,
		Args: MinimumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]
			keys := args[1:]

			var unresolvedKeys []string
			var err error

			if len(keys) > 0 {
				unresolvedKeys, err = decisionhold.Verify(ctx.Home, originID, keys)
			} else {
				unresolvedKeys, err = decisionhold.Verify(ctx.Home, originID, nil)
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "error: verifying holds: %v\n", err)
				os.Exit(2)
				return nil
			}

			if len(unresolvedKeys) > 0 {
				fmt.Fprintf(os.Stderr, "unresolved decisions remain: %s\n", strings.Join(unresolvedKeys, ", "))
				os.Exit(1)
				return nil
			}

			fmt.Printf("No unresolved decisions for %s\n", originID)
			return nil
		}),
	}

	return cmd
}

func newDecisionHoldResolveCmd() *cobra.Command {
	var answer string
	var unblock []string
	var from string
	cmd := &cobra.Command{
		Use:   "resolve <key> --answer <text> --from <origin-id> [--unblock <dep-id>...]",
		Short: "Record the general's decision and unblock dependent work",
		Long: `Record the general's decision for a hold and unblock any dependent tasks.

The --from flag specifies the originating task ID (must match the hold's origin).
The --unblock flag may be repeated to unblock multiple dependencies.

Examples:
  munsu decision-hold resolve approach --answer "Choose React" --from scout-r2
  munsu decision-hold resolve approach --answer "Choose React" --from scout-r2 --unblock dep-task-1`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			if answer == "" {
				return fmt.Errorf("--answer is required")
			}
			if from == "" {
				return fmt.Errorf("--from is required")
			}

			if err := decisionhold.Resolve(ctx.Home, from, key, answer, unblock); err != nil {
				return fmt.Errorf("resolving hold: %w", err)
			}

			msg := fmt.Sprintf("Hold %s resolved: %s", key, answer)
			if len(unblock) > 0 {
				msg += "\nUnblocked: " + strings.Join(unblock, ", ")
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "decision-hold.resolve",
				Status:        "success",
				Data:          contract.MessageResult{Message: msg},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().StringVar(&answer, "answer", "", "The general's decision")
	cmd.Flags().StringVar(&from, "from", "", "Originating task ID that owns this decision hold")
	cmd.Flags().StringArrayVar(&unblock, "unblock", nil, "Dependent task to unblock (repeatable)")
	return cmd
}

func newDecisionHoldListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <origin-id>",
		Short: "List unresolved decisions for an origin task",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]

			holds, err := decisionhold.ListUnresolved(ctx.Home, originID)
			if err != nil {
				return fmt.Errorf("listing holds: %w", err)
			}

			if len(holds) == 0 {
				return writeContract(cmd, contract.Response[contract.EmptyResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "decision-hold.list",
					Status:        "success",
					Data:          contract.EmptyResult{Count: 0, Context: fmt.Sprintf("No unresolved decisions for %s", originID)},
				})
			}

			var holdEntries []contract.DecisionHoldInfo
			for _, h := range holds {
				holdEntries = append(holdEntries, contract.DecisionHoldInfo{
					DecisionKey: h.DecisionKey,
					Reason:      h.Reason,
				})
			}

			return writeContract(cmd, contract.Response[[]contract.DecisionHoldInfo]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "decision-hold.list",
				Status:        "success",
				Data:          holdEntries,
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}
