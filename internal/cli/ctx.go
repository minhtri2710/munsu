package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
	"github.com/spf13/cobra"
)

// Ctx holds resolved context for a command handler.
type Ctx struct {
	Home string

	// taskAuthority is the concrete Authority composed once for this command
	// context (ADR-0007 §9). It is nil until first requested; commands that
	// never request Task Authority never construct it. Tests inject an
	// Authority backed by an in-memory Store by pre-setting this field.
	taskAuthority *taskauthority.Authority
}

// TaskAuthority returns the concrete Authority for this command context,
// composing the filesystem Store adapter and the Authority once on first
// use. Store construction is side-effect free: it performs no migration and
// no mutation. An injected Authority is returned unchanged.
func (c *Ctx) TaskAuthority() (*taskauthority.Authority, error) {
	if c.taskAuthority != nil {
		return c.taskAuthority, nil
	}
	store, err := taskauthorityfs.NewStore(c.Home)
	if err != nil {
		return nil, err
	}
	c.taskAuthority = taskauthority.New(store)
	return c.taskAuthority, nil
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
