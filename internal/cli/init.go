package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create home and seed orchestrator operating manual",
		Long: `Initialize the munsu home directory tree.

Creates the directory structure: {state, data, config, projects}.
Writes starter configuration files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if err := home.EnsureDirTree(homeDir); err != nil {
				return fmt.Errorf("creating home tree: %w", err)
			}
			// Write starter config
			if err := config.Set(homeDir, "backend", "tmux"); err != nil {
				return fmt.Errorf("writing starter config: %w", err)
			}
			fmt.Printf("Initialized munsu home at %s\n", homeDir)
			return nil
		},
	}
}
