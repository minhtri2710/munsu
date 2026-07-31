package config

import "time"

// ResolvedSnapshot freezes one project's resolved configuration for an operation.
type ResolvedSnapshot struct {
	loadedAt time.Time
	config   ResolvedProjectConfig
}

func LoadResolvedSnapshot(home, project string, overrides BoundaryOverrides) (ResolvedSnapshot, error) {
	base, captains, projects, err := LoadDocuments(home)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	resolved, err := ResolveProject(base, captains, projects, project, overrides)
	if err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{loadedAt: time.Now().UTC(), config: cloneResolvedProjectConfig(resolved)}, nil
}

func (s ResolvedSnapshot) LoadedAt() time.Time { return s.loadedAt }

func (s ResolvedSnapshot) Config() ResolvedProjectConfig {
	return cloneResolvedProjectConfig(s.config)
}

func cloneResolvedProjectConfig(src ResolvedProjectConfig) ResolvedProjectConfig {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	return result
}
