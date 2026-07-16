package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

// Ctx holds resolved context for a command handler.
type Ctx struct {
	Home string
}

// withHome wraps a cobra RunE so the handler receives a resolved Ctx instead
// of calling home.Resolve(homeOverride) itself.
func withHome(fn func(cmd *cobra.Command, args []string, ctx Ctx) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		homeDir, err := home.Resolve(homeOverride)
		if err != nil {
			return fmt.Errorf("resolving home: %w", err)
		}
		return fn(cmd, args, Ctx{Home: homeDir})
	}
}
