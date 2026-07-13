package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

//go:embed seed_orchestrator_manual.md
var orchestratorManual string

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create home and seed orchestrator operating manual",
		Long: `Initialize the munsu home directory tree.

Creates the directory structure: {state, data, config, projects}.
Writes starter configuration files and the orchestrator operating manual (AGENTS.md).`,
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

			// Write orchestrator AGENTS.md
			agentsPath := filepath.Join(homeDir, "AGENTS.md")
			if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
				if err := os.WriteFile(agentsPath, []byte(orchestratorManual), 0644); err != nil {
					return fmt.Errorf("writing orchestrator manual: %w", err)
				}
				fmt.Printf("Wrote orchestrator manual to %s\n", agentsPath)
			} else {
				fmt.Printf("AGENTS.md already exists at %s (skipped)\n", agentsPath)
			}

			fmt.Printf("Initialized munsu home at %s\n", homeDir)
			return nil
		},
	}
}
