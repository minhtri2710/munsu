package cli

import (
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Detect and manage agent harness adapters",
	}

	detectCmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect the running agent harness",
		Long:  `Detect the running agent harness using env markers first, then process ancestry.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := harness.Detect()
			if err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          contract.MessageResult{Message: h},
			})
		},
	}
	configureContractCommand(detectCmd)
	cmd.AddCommand(detectCmd)

	soldierCmd := &cobra.Command{
		Use:   "soldier",
		Short: "Resolve soldier harness",
		Long:  `Resolve the soldier harness. Fallback chain: soldier-dispatch.json default > config/soldier-harness > detected harness.`,
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			h, err := harness.Soldier(ctx.Home)
			if err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          contract.MessageResult{Message: h},
			})
		}),
	}
	configureContractCommand(soldierCmd)
	cmd.AddCommand(soldierCmd)

	captainCmd := &cobra.Command{
		Use:   "captain",
		Short: "Resolve captain harness",
		Long:  `Resolve the general harness. Fallback chain: config/captain-harness > config/soldier-harness > detected harness.`,
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			h, err := harness.Captain(ctx.Home)
			if err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          contract.MessageResult{Message: h},
			})
		}),
	}
	configureContractCommand(captainCmd)
	cmd.AddCommand(captainCmd)

	return cmd
}
