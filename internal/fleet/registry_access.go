package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
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
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	projectOverlay, err := config.LoadProjectOverlay(homeDir, projectName)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	r, err := openRegistry(homeDir)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	project, err := r.GetProject(projectID)
	if err != nil {
		return config.ResolvedSnapshot{}, err
	}
	facts := config.ProjectFacts{
		Name:           project.Name,
		Path:           project.Path,
		Mode:           project.Mode,
		Overlay:        projectOverlay,
		CaptainProfile: config.CaptainProfile{},
	}
	return config.NewResolvedSnapshot(base, facts)
}
