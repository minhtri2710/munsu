package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type captainIntegrationAdapter struct{}

func (captainIntegrationAdapter) EnsureCaptain(h string) error {
	_, err := bootstrap.Install(h, h, "pi", bootstrap.ScopeProject, false)
	if err != nil {
		return fmt.Errorf("installing canonical Pi integration: %w", err)
	}
	return nil
}
func requireCaptainIntegration(home, harnessName string) error {
	if harnessName != "pi" {
		return nil
	}
	status, err := (captainIntegrationAdapter{}).Status(home, harnessName)
	if err != nil {
		return fmt.Errorf("checking canonical Pi integration: %w", err)
	}
	if status.State != "installed" {
		return fmt.Errorf("canonical Pi integration is %s: %s; repair with: munsu integrate repair --harness pi --scope project", status.State, status.Message)
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
