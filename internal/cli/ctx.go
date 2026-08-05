package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

// Ctx holds resolved context for a command handler.
type Ctx struct {
	Home string

	// taskAuthority is the canonical Task Authority composed once for this
	// command context over the resolved owning Home (ADR-0008 §2). It is nil
	// until first requested; commands that never request Task Authority never
	// construct it. Tests inject a canonical home-backed Authority by
	// pre-setting this field. It is cached only for Ctx.Home.
	taskAuthority *taskauthority.Canonical
}

// TaskAuthority returns the canonical Task Authority for this command
// context, opening the resolved owning Home through the real home API and
// composing the canonical authority once on first use. Ordinary reads fail
// closed for an uninitialized Home rather than silently initializing state;
// only command semantics that explicitly create a Home initialize one. An
// injected Authority is returned unchanged.
func (c *Ctx) TaskAuthority() (*taskauthority.Canonical, error) {
	if c.taskAuthority != nil {
		return c.taskAuthority, nil
	}
	h, err := home.Open(c.Home)
	if err != nil {
		return nil, fmt.Errorf("opening task authority home %s: %w", c.Home, err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return nil, err
	}
	c.taskAuthority = auth
	return auth, nil
}

// TaskAuthorityFor returns a canonical authority for the exact resolved
// owning Home. Cross-home delivery resolves the task home (which may be a
// captain home after handoff) before composing, so the Authority always
// targets the home that owns the task. The requested home is opened through
// the real home API; an uninitialized Home fails closed.
func (c *Ctx) TaskAuthorityFor(homeDir string) (*taskauthority.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, fmt.Errorf("opening task authority home %s: %w", homeDir, err)
	}
	return taskauthority.NewCanonical(h)
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
