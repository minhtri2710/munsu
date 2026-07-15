package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Detect and manage agent harness adapters",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "detect",
		Short: "Detect the running agent harness",
		Long:  `Detect the running agent harness using env markers first, then process ancestry.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := harness.Detect()
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "crew",
		Short: "Resolve crewmate harness",
		Long:  `Resolve the crewmate harness. Fallback chain: crew-dispatch.json default > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			h, err := harness.Crew(homeDir)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "secondmate",
		Short: "Resolve secondmate harness",
		Long:  `Resolve the secondmate harness. Fallback chain: config/secondmate-harness > config/crew-harness > detected harness.`,
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}
			h, err := harness.Secondmate(homeDir)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	})

	return cmd
}
