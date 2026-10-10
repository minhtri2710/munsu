package fleet

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// openRegistry opens (or initializes) the canonical home at homeDir and
// constructs the canonical Fleet Registry over it. The Fleet Registry is the
// sole Project/Captain lifecycle authority; Config no longer reads or stores
// lifecycle registries.
func openRegistry(homeDir string) (*Registry, error) {
	h, err := home.Init(homeDir)
	if err != nil {
		return nil, fmt.Errorf("fleet: opening registry home %s: %w", homeDir, err)
	}
	r, err := NewRegistry(h)
	if err != nil {
		return nil, fmt.Errorf("fleet: constructing registry: %w", err)
	}
	return r, nil
}

// newOperationFor derives a deterministic Operation ID from the typed intent
// digest, so retrying the same intent is idempotent (replay) and a distinct
// intent is a non-retryable conflict. The operation identity is stable across
// the same request.
func newOperationFor(intent domain.Intent) (domain.Operation, error) {
	digest, err := domain.Digest(intent)
	if err != nil {
		return domain.Operation{}, err
	}
	opID, err := domain.NewOperationID(digest)
	if err != nil {
		return domain.Operation{}, err
	}
	return domain.NewOperation(opID, intent)
}

// mustOpFor builds the canonical operation for a typed intent, returning the
// operation or a wrapped error. Keeping call sites concise while preserving
// error propagation.
func mustOpFor(intent domain.Intent) (domain.Operation, error) {
	return newOperationFor(intent)
}

// opFor builds the canonical operation for a typed intent. The intent is
// always well-formed for these internal lifecycle requests, so an encoding
// failure is treated as a programming error.
func opFor(intent domain.Intent) domain.Operation {
	op, err := newOperationFor(intent)
	if err != nil {
		panic(err)
	}
	return op
}

// ResolveProjectSnapshot resolves one Project's frozen snapshot from the
// canonical Fleet Registry facts and the Config-owned base overlay. It is the
// composition helper that maps Fleet's authoritative query facts into Config's
// narrow input at call time.
func ResolveProjectSnapshot(homeDir, projectName string) (config.ResolvedSnapshot, error) {
	base, facts, err := loadProjectFacts(homeDir, projectName)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	return config.NewResolvedSnapshotWithToolProbe(base, facts, configuredToolProbe)
}

// resolveCapturedProjectSnapshot resolves one Project's snapshot against the
// tool steps its task generation captured, without probing the overlay's
// current tool entries.
func resolveCapturedProjectSnapshot(homeDir, projectName string, review, forge config.ResolvedStep) (config.ResolvedSnapshot, error) {
	base, facts, err := loadProjectFacts(homeDir, projectName)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	return config.NewResolvedSnapshotWithCapturedSteps(base, facts, review, forge)
}

func loadProjectFacts(homeDir, projectName string) (config.FleetBaseDocument, config.ProjectFacts, error) {
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		return config.FleetBaseDocument{}, config.ProjectFacts{}, err
	}
	projectOverlay, err := config.LoadProjectOverlay(homeDir, projectName)
	if err != nil {
		return config.FleetBaseDocument{}, config.ProjectFacts{}, err
	}
	r, err := openRegistry(homeDir)
	if err != nil {
		return config.FleetBaseDocument{}, config.ProjectFacts{}, err
	}
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		return config.FleetBaseDocument{}, config.ProjectFacts{}, err
	}
	project, err := r.GetProject(projectID)
	if err != nil {
		return config.FleetBaseDocument{}, config.ProjectFacts{}, err
	}
	facts := config.ProjectFacts{
		Name:    project.Name,
		Path:    project.Path,
		Overlay: projectOverlay,
	}
	return base, facts, nil
}

// toolEntryOf returns the configured tool a captured delivery step names.
func toolEntryOf(step taskauthority.DeliveryStep) config.ToolEntry {
	return config.ToolEntry{Adapter: step.Adapter, Path: step.Path, Args: append([]string(nil), step.Args...)}
}

func probeConfiguredForge(entry config.ToolEntry) backend.State {
	switch entry.Adapter {
	case "github":
		return ProbeGitHubDeliveryCapability()
	case "gitlab":
		return probeGlabCapability(glabRunnerFor(entry))
	default:
		return backend.Unsupported
	}
}
func configuredToolProbe(step string, entry config.ToolEntry) config.ResolvedStep {
	switch {
	case step == "review" && entry.Adapter == "no-mistakes":
		probe := ProbeNoMistakesTool(entry)
		return config.ResolvedStep{Path: probe.Path, ProbeState: probe.State.String(), Version: probe.Version, Reason: probe.Detail}
	case step == "forge" && entry.Adapter == "gitlab":
		runner := glabRunnerFor(entry)
		state := probeGlabCapability(runner)
		path, _ := runner.LookPath()
		reason := "glab executable and API/auth capabilities are Ready"
		if state != backend.Ready {
			reason = fmt.Sprintf("glab probe state: %s", state)
		}
		return config.ResolvedStep{Path: path, ProbeState: strings.ToLower(state.String()), Reason: reason}
	case step == "forge" && entry.Adapter == "github":
		state := ProbeGitHubDeliveryCapability()
		path, _ := ghAxiLookPath()
		reason := fmt.Sprintf("gh-axi and gh probe state: %s", state)
		return config.ResolvedStep{Path: path, ProbeState: strings.ToLower(state.String()), Reason: reason}
	default:
		return config.ResolvedStep{ProbeState: "failed", Reason: "unsupported configured tool"}
	}
}
