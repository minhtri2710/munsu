package fleet

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// forgeClient is the provider client one captured forge step resolves to. The
// step's adapter sets exactly one field.
type forgeClient struct {
	github *ghAxiClient
	gitlab *glabClient
}

// forgeClientFor resolves a task generation's captured forge step to its
// provider client. The step's adapter names the provider, and its captured
// path and args drive the glab runner. Every call re-probes the configured tool
// and refuses unless it is Ready, so no provider call runs against an absent or
// failed forge. Nothing here selects a provider from PATH.
func forgeClientFor(step taskauthority.DeliveryStep) (forgeClient, error) {
	if step.Baseline {
		return forgeClient{}, fmt.Errorf("the task has no configured forge tool; delivery needs a Ready forge step")
	}
	entry := toolEntryOf(step)
	if state := probeConfiguredForge(entry); state != backend.Ready {
		return forgeClient{}, fmt.Errorf("forge adapter %s probe %s: configured forge is not Ready", step.Adapter, strings.ToLower(state.String()))
	}
	switch step.Adapter {
	case "github":
		return forgeClient{github: &ghAxiClient{}}, nil
	case "gitlab":
		return forgeClient{gitlab: &glabClient{runner: glabRunnerFor(entry)}}, nil
	}
	return forgeClient{}, fmt.Errorf("forge adapter %q is not compiled in", step.Adapter)
}

// forgeClientForIdentity resolves the captured forge step to the client for one
// delivery identity. The identity's provider must be the step's adapter, so a
// GitLab identity never reaches a GitHub forge and the reverse.
func forgeClientForIdentity(step taskauthority.DeliveryStep, ident domain.DeliveryIdentity) (forgeClient, error) {
	if ident.Provider != "github" && ident.Provider != "gitlab" {
		return forgeClient{}, fmt.Errorf("unsupported delivery provider %q", ident.Provider)
	}
	if step.Adapter != ident.Provider {
		return forgeClient{}, fmt.Errorf("delivery identity provider %q does not match the task's configured forge adapter %q", ident.Provider, step.Adapter)
	}
	return forgeClientFor(step)
}

// capturedForgeStep returns the forge step the task's current delivery contract
// captured. A task with no contract has no captured step and fails closed.
func capturedForgeStep(c *taskauthority.Canonical, taskID string) (taskauthority.DeliveryStep, error) {
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return taskauthority.DeliveryStep{}, err
	}
	agg, err := c.Get(tid)
	if err != nil {
		return taskauthority.DeliveryStep{}, fmt.Errorf("resolving task %s: %w", taskID, err)
	}
	if agg.DeliveryContract == nil {
		return taskauthority.DeliveryStep{}, fmt.Errorf("task %s has no captured delivery contract; spawn it before delivery", taskID)
	}
	return agg.DeliveryContract.Forge, nil
}

// taskForgeStep reads the captured forge step for a task from the home's task
// authority, for read-only provider queries.
func taskForgeStep(homeDir, taskID string) (taskauthority.DeliveryStep, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return taskauthority.DeliveryStep{}, fmt.Errorf("opening task authority home %s: %w", homeDir, err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return taskauthority.DeliveryStep{}, fmt.Errorf("composing task authority: %w", err)
	}
	return capturedForgeStep(c, taskID)
}
