package config

// ResolvedSnapshot freezes one project's resolved configuration for an operation.
type ResolvedSnapshot struct {
	config ResolvedProjectConfig
}

// NewResolvedSnapshot resolves one Project's overlay from Fleet-owned scoped
// facts and the base overlay, freezing the result for an operation. Config
// owns no registry and cannot read or mutate Project/Captain lifecycle.
func NewResolvedSnapshot(base FleetBaseDocument, facts ProjectFacts, overrides BoundaryOverrides) (ResolvedSnapshot, error) {
	resolved, err := ResolveProject(base, facts, overrides)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{config: cloneResolvedProjectConfig(resolved)}, nil
}

func (s ResolvedSnapshot) Config() ResolvedProjectConfig {
	return cloneResolvedProjectConfig(s.config)
}

func cloneResolvedProjectConfig(src ResolvedProjectConfig) ResolvedProjectConfig {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	return result
}
