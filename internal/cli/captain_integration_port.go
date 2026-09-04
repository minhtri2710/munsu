package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type captainIntegrationAdapter struct{}

func (captainIntegrationAdapter) EnsureCaptain(h, harnessName string) error {
	_, err := bootstrap.Install(h, h, harnessName, bootstrap.ScopeProject, false)
	if err != nil {
		return fmt.Errorf("installing captain %s integration: %w", harnessName, err)
	}
	return nil
}

func (captainIntegrationAdapter) Status(h, n string) (fleet.IntegrationStatus, error) {
	r, e := bootstrap.Status(h, h, n, bootstrap.ScopeProject)
	if e != nil {
		return fleet.IntegrationStatus{}, e
	}
	return fleet.IntegrationStatus{Harness: r.Harness, Scope: string(r.Scope), State: r.State, Message: r.Message}, nil
}
