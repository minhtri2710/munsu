package config

import "fmt"

type ResolvedSnapshot struct {
	config ResolvedProjectConfig
}

// NewResolvedSnapshotWithToolProbe resolves one Project's overlay from Fleet-owned scoped
// facts and the base overlay, freezing the result for an operation. Config
// owns no registry and cannot read or mutate Project/Captain lifecycle.
func NewResolvedSnapshotWithToolProbe(base FleetBaseDocument, facts ProjectFacts, probe func(string, ToolEntry) ResolvedStep) (ResolvedSnapshot, error) {
	resolved, err := ResolveProjectWithToolProbe(base, facts, probe)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{config: cloneResolvedProjectConfig(resolved)}, nil
}

// NewResolvedSnapshotWithCapturedSteps resolves a Project against the review
// and forge steps its task generation captured. The overlay's current tool
// entries are replaced by the captured ones, so later overlay drift neither
// refuses the launch nor changes it; the captured tools are re-probed at
// preflight.
func NewResolvedSnapshotWithCapturedSteps(base FleetBaseDocument, facts ProjectFacts, review, forge ResolvedStep) (ResolvedSnapshot, error) {
	facts.Overlay.Review = toolEntryFromStep(review)
	facts.Overlay.Forge = toolEntryFromStep(forge)
	resolved, err := ResolveProjectWithSteps(base, facts, review, forge)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{config: cloneResolvedProjectConfig(resolved)}, nil
}

// WithSteps returns a snapshot rebased on the delivery steps captured by a task
// contract. The captured steps are authoritative even when the project overlay
// has changed since the task's generation was first launched.
func (s ResolvedSnapshot) WithSteps(review, forge ResolvedStep) (ResolvedSnapshot, error) {
	resolved := cloneResolvedProjectConfig(s.config)
	if resolved.Project == "" {
		return ResolvedSnapshot{}, fmt.Errorf("resolved snapshot is empty")
	}
	if err := validateResolvedStep("review", review); err != nil {
		return ResolvedSnapshot{}, err
	}
	if err := validateResolvedStep("forge", forge); err != nil {
		return ResolvedSnapshot{}, err
	}
	if !review.Baseline && review.Adapter == "no-mistakes" && forge.Baseline {
		return ResolvedSnapshot{}, fmt.Errorf("review adapter no-mistakes requires a configured forge tool")
	}
	resolved.ReviewStep = cloneResolvedStep(review)
	resolved.ForgeStep = cloneResolvedStep(forge)
	config := ProjectOverlay{
		SoldierHarness: resolved.SoldierHarness, Model: resolved.Model,
		DispatchAutonomy: resolved.DispatchAutonomy, Backend: resolved.Backend,
		TamperCheck:      resolved.TamperCheck,
		DispatchProfiles: cloneProfiles(resolved.DispatchProfiles),
		Review:           toolEntryFromStep(review), Forge: toolEntryFromStep(forge),
	}
	digest, err := projectDigest(config, review, forge)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	resolved.Digest = digest
	return ResolvedSnapshot{config: resolved}, nil
}

func (s ResolvedSnapshot) Config() ResolvedProjectConfig {
	return cloneResolvedProjectConfig(s.config)
}

func cloneResolvedProjectConfig(src ResolvedProjectConfig) ResolvedProjectConfig {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	result.ReviewStep = cloneResolvedStep(src.ReviewStep)
	result.ForgeStep = cloneResolvedStep(src.ForgeStep)
	return result
}
