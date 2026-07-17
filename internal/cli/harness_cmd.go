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

	crewCmd := &cobra.Command{
		Use:   "crew",
		Short: "Resolve crewmate harness",
		Long:  `Resolve the crewmate harness. Fallback chain: crew-dispatch.json default > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			h, err := harness.Crew(ctx.Home)
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
	configureContractCommand(crewCmd)
	cmd.AddCommand(crewCmd)

	secondmateCmd := &cobra.Command{
		Use:   "secondmate",
		Short: "Resolve secondmate harness",
		Long:  `Resolve the secondmate harness. Fallback chain: config/secondmate-harness > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			h, err := harness.Secondmate(ctx.Home)
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
	configureContractCommand(secondmateCmd)
	cmd.AddCommand(secondmateCmd)

	return cmd
}
