package cli

import (
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
				// --mkdir initializes a canonical Home (identity + layout), not
				// only directories, so canonical commands work immediately.
				if _, err := home.Init(ctx.Home); err != nil {
					return err
				}
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          MessageResult{Message: ctx.Home},
			})
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().BoolVar(&mkdir, "mkdir", false, "create the home directory tree")
	return cmd
}
