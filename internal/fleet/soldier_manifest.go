// Package fleet implements the Soldier launch manifest — a versioned digest
// manifest binding relative paths, SHA-256 digests, and disposal policies
// for all runtime-owned launch artifacts.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManifestVersion is the current manifest format version.
const ManifestVersion = "soldier-manifest-v1"

// DisposalPolicy describes how a manifest artifact may be treated during
// normal (non-force) retirement.
type DisposalPolicy string

const (
	// DisposalPolicyCleanable allows the artifact to be removed or ignored
	// during normal retirement when its digest matches.
	DisposalPolicyCleanable DisposalPolicy = "cleanable"
)

// ManifestEntry binds one launch artifact's relative path, SHA-256 digest,
// and disposal policy.
type ManifestEntry struct {
	Path   string         `json:"path"`
	SHA256 string         `json:"sha256"`
	Policy DisposalPolicy `json:"policy"`
}

// LaunchManifest is the versioned ownership manifest for Soldier launch
// artifacts.
type LaunchManifest struct {
	ManifestVersion string          `json:"manifest_version"`
	Artifacts       []ManifestEntry `json:"artifacts"`
}

// WriteManifest writes the manifest to .soldier-manifest.json in the worktree.
// The manifest is written after all other launch artifacts exist and does not
// include an entry for itself.
func WriteManifest(worktreePath string, manifest *LaunchManifest) error {
	if manifest == nil {
		return fmt.Errorf("launch manifest is nil")
	}
	manifest.ManifestVersion = ManifestVersion
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling launch manifest: %w", err)
	}
	data = append(data, '\n')
	manifestPath := filepath.Join(worktreePath, ManifestName)
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", ManifestName, err)
	}
	return nil
}

// ReadManifest reads and parses the launch manifest from the worktree.
func ReadManifest(worktreePath string) (*LaunchManifest, error) {
	manifestPath := filepath.Join(worktreePath, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ManifestName, err)
	}
	var manifest LaunchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestName, err)
	}
	if manifest.ManifestVersion == "" {
		return nil, fmt.Errorf("%s: manifest version is empty", ManifestName)
	}
	return &manifest, nil
}

// Lookup returns the manifest entry for the given relative path, or nil if
// the path is not in the manifest.
func (m *LaunchManifest) Lookup(path string) *ManifestEntry {
	if m == nil {
		return nil
	}
	for _, entry := range m.Artifacts {
		if entry.Path == path {
			return &entry
		}
	}
	return nil
}

// ArtifactPaths returns the set of relative paths declared in the manifest.
func (m *LaunchManifest) ArtifactPaths() map[string]bool {
	if m == nil {
		return nil
	}
	paths := make(map[string]bool, len(m.Artifacts))
	for _, entry := range m.Artifacts {
		paths[entry.Path] = true
	}
	return paths
}

// BuildManifest constructs a LaunchManifest from the given artifact entries.
// Callers must provide the path, SHA-256 digest, and policy for each artifact.
func BuildManifest(entries []ManifestEntry) *LaunchManifest {
	return &LaunchManifest{
		ManifestVersion: ManifestVersion,
		Artifacts:       entries,
	}
}

// ManifestEntryForFile builds a ManifestEntry for a file at the given path
// relative to worktreeRoot, computing its SHA-256 digest.
func ManifestEntryForFile(worktreeRoot, relPath string, policy DisposalPolicy) (ManifestEntry, error) {
	if strings.Contains(relPath, "..") || filepath.IsAbs(relPath) {
		return ManifestEntry{}, fmt.Errorf("unsafe manifest path: %s", relPath)
	}
	fullPath := filepath.Join(worktreeRoot, relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("reading %s for manifest: %w", relPath, err)
	}
	return ManifestEntry{
		Path:   relPath,
		SHA256: sha256Content(data),
		Policy: policy,
	}, nil
}