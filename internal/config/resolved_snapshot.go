package config

import "fmt"

// ResolvedSnapshot freezes one project's resolved configuration for an operation.
type ResolvedSnapshot struct {
	config ResolvedProjectConfig
}

// NewResolvedSnapshotWithToolProbe resolves one Project's overlay from Fleet-owned scoped
// facts and the base overlay, freezing the result for an operation. Config
// owns no registry and cannot read or mutate Project/Captain lifecycle.
func NewResolvedSnapshotWithToolProbe(base FleetBaseDocument, facts ProjectFacts, probe func(ToolEntry) ResolvedStep) (ResolvedSnapshot, error) {
	resolved, err := ResolveProjectWithToolProbe(base, facts, probe)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{config: cloneResolvedProjectConfig(resolved)}, nil
}

// NewResolvedSnapshotWithCapturedSteps resolves a Project against the forge
// step its task generation captured. The overlay's current forge entry is
// replaced by the captured one, so later overlay drift neither refuses the
// launch nor changes it; the captured tool is re-probed at preflight. The
// worktree entry is not captured and is probed now.
func NewResolvedSnapshotWithCapturedSteps(base FleetBaseDocument, facts ProjectFacts, forge ResolvedStep, probe func(ToolEntry) ResolvedStep) (ResolvedSnapshot, error) {
	facts.Overlay.Forge = toolEntryFromStep(forge)
	resolved, err := ResolveProjectWithSteps(base, facts, forge, resolveToolStep(facts.Overlay.Worktree, probe))
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{config: cloneResolvedProjectConfig(resolved)}, nil
}

// WithSteps returns a snapshot rebased on the delivery step captured by a task
// contract. The captured step is authoritative even when the project overlay
// has changed since the task's generation was first launched.
func (s ResolvedSnapshot) WithSteps(forge ResolvedStep) (ResolvedSnapshot, error) {
	resolved := cloneResolvedProjectConfig(s.config)
	if resolved.Project == "" {
		return ResolvedSnapshot{}, fmt.Errorf("resolved snapshot is empty")
	}
	if err := validateResolvedStep("forge", forgeAdapters, forge); err != nil {
		return ResolvedSnapshot{}, err
	}
	resolved.ForgeStep = cloneResolvedStep(forge)
	config := ProjectOverlay{
		SoldierHarness: resolved.SoldierHarness, Model: resolved.Model,
		DispatchAutonomy: resolved.DispatchAutonomy, Backend: resolved.Backend,
		TamperCheck:        resolved.TamperCheck,
		DispatchProfiles:   cloneProfiles(resolved.DispatchProfiles),
		Forge:              toolEntryFromStep(forge),
		Worktree:           toolEntryFromStep(resolved.WorktreeStep),
		FenceWritableRoots: resolved.FenceWritableRoots, FenceUnsupported: resolved.FenceUnsupported,
	}
	digest, err := projectDigest(config, forge, resolved.WorktreeStep)
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
	result.ForgeStep = cloneResolvedStep(src.ForgeStep)
	result.WorktreeStep = cloneResolvedStep(src.WorktreeStep)
	result.FenceWritableRoots = append([]string(nil), src.FenceWritableRoots...)
	return result
}
