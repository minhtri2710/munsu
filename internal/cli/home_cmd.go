package cli

import (
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

// newHomeCmd implements `munsu home [--mkdir]`.
func newHomeCmd() *cobra.Command {
	var mkdir bool
	cmd := &cobra.Command{
		Use:   "home [--mkdir]",
		Short: "Print the munsu home directory",
		Long: `Resolve and print the munsu home directory.

Resolution order: --home flag > MUNSU_HOME env > ~/.munsu.
With --mkdir, create the home directory tree {state,data,config,projects}.`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if mkdir {
				if err := home.EnsureDirTree(ctx.Home); err != nil {
					return err
				}
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          contract.MessageResult{Message: ctx.Home},
			})
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().BoolVar(&mkdir, "mkdir", false, "create the home directory tree")
	return cmd
}
