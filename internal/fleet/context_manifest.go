// Package fleet implements the Context Manifest — a versioned, revision-bound
// digest manifest binding source file references, SHA-256 digests, git revisions,
// explicit reasons, and category budgets for targeted Soldier implementation context.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextManifestVersion is the current context manifest format version.
const ContextManifestVersion = "context-manifest-v1"

// ContextManifestName is the file name for the context manifest written to the worktree.
const ContextManifestName = ".context-manifest.json"

// ContextManifestEntryCategory describes the role of a referenced source file.
type ContextManifestEntryCategory string

const (
	// ContextCategoryImplementation is a production source file.
	ContextCategoryImplementation ContextManifestEntryCategory = "implementation"
	// ContextCategoryTest is a test source file.
	ContextCategoryTest ContextManifestEntryCategory = "test"
	// ContextCategoryArchitecture is a design or documentation file describing architecture.
	ContextCategoryArchitecture ContextManifestEntryCategory = "architecture"
)

// ContextManifestEntry binds one source file reference with its content digest,
// git revision, category, and an explicit reason for inclusion.
type ContextManifestEntry struct {
	Path     string                       `json:"path"`
	SHA256   string                       `json:"sha256"`
	Revision string                       `json:"revision"`
	Category ContextManifestEntryCategory `json:"category"`
	Reason   string                       `json:"reason"`
	Stale    bool                         `json:"stale,omitempty"`
}

// ContextManifestBudgets constrains the default number of entries per category.
type ContextManifestBudgets struct {
	MaxImplementation int `json:"max_implementation"`
	MaxTest           int `json:"max_test"`
	MaxArchitecture   int `json:"max_architecture"`
}

// DefaultContextManifestBudgets returns the default budgets:
// implementation=5, test=3, architecture=2.
func DefaultContextManifestBudgets() ContextManifestBudgets {
	return ContextManifestBudgets{
		MaxImplementation: 5,
		MaxTest:           3,
		MaxArchitecture:   2,
	}
}

// ContextManifest is a versioned, revision-bound manifest of source file
// references that combines author hints with bounded repository evidence
// and explicit reasons for each reference.
type ContextManifest struct {
	ManifestVersion string                 `json:"manifest_version"`
	Revision        int                    `json:"revision"`
	AuthorHint      string                 `json:"author_hint,omitempty"`
	Budgets         ContextManifestBudgets `json:"budgets"`
	Entries         []ContextManifestEntry `json:"entries"`
	Stale           bool                   `json:"stale,omitempty"`
}

// NewContextManifest creates a new context manifest with the given author hint
// and budgets. Revision starts at 1. Use DefaultContextManifestBudgets() for
// sensible defaults.
func NewContextManifest(authorHint string, budgets ContextManifestBudgets) *ContextManifest {
	return &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		AuthorHint:      authorHint,
		Budgets:         budgets,
		Entries:         nil,
	}
}

// AddEntry appends a source file reference to the manifest if the category
// budget has not been exhausted and the path is not already present.
// Returns an error when the budget for the given category is exceeded.
func (m *ContextManifest) AddEntry(path, sha256, revision string, category ContextManifestEntryCategory, reason string) error {
	if m == nil {
		return fmt.Errorf("context manifest is nil")
	}
	if path == "" {
		return fmt.Errorf("entry path must not be empty")
	}
	if sha256 == "" || !sha256Regex.MatchString(sha256) {
		return fmt.Errorf("invalid SHA-256 digest for %q: %q", path, sha256)
	}
	if revision == "" {
		return fmt.Errorf("entry revision must not be empty for %q", path)
	}
	if reason == "" {
		return fmt.Errorf("entry reason must not be empty for %q", path)
	}

	// Count existing entries in this category.
	count := 0
	for _, e := range m.Entries {
		if e.Category == category {
			count++
		}
	}

	max := 0
	switch category {
	case ContextCategoryImplementation:
		max = m.Budgets.MaxImplementation
	case ContextCategoryTest:
		max = m.Budgets.MaxTest
	case ContextCategoryArchitecture:
		max = m.Budgets.MaxArchitecture
	default:
		return fmt.Errorf("unknown category: %q", category)
	}

	if count >= max {
		return fmt.Errorf("budget exceeded for category %q: max %d", category, max)
	}

	// Check for duplicate path.
	for _, e := range m.Entries {
		if e.Path == path {
			return fmt.Errorf("duplicate entry: %q", path)
		}
	}

	m.Entries = append(m.Entries, ContextManifestEntry{
		Path:     path,
		SHA256:   sha256,
		Revision: revision,
		Category: category,
		Reason:   reason,
	})
	return nil
}

// CheckStale re-checks each entry's SHA-256 digest against the current file
// content on disk. Entries whose digest no longer match are marked stale, and
// the manifest-level Stale flag is set when any entry is stale. Missing files
// are also marked stale.
func (m *ContextManifest) CheckStale(worktreeRoot string) error {
	if m == nil {
		return fmt.Errorf("context manifest is nil")
	}
	anyStale := false
	for i, entry := range m.Entries {
		fullPath := filepath.Join(worktreeRoot, entry.Path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			// File missing or inaccessible = stale.
			m.Entries[i].Stale = true
			anyStale = true
			continue
		}
		currentDigest := sha256Content(data)
		if currentDigest != entry.SHA256 {
			m.Entries[i].Stale = true
			anyStale = true
		}
	}
	m.Stale = anyStale
	return nil
}

// Expand increments the manifest revision. When newBudgets is non-nil, the
// budgets are replaced with the supplied values. Callers should call
// WriteContextManifest after Expand to persist the new revision.
func (m *ContextManifest) Expand(newBudgets *ContextManifestBudgets) {
	if m == nil {
		return
	}
	m.Revision++
	if newBudgets != nil {
		m.Budgets = *newBudgets
	}
	// After explicit expansion, stale flag is preserved for caller to re-check.
}

// WriteContextManifest writes the context manifest to the worktree as
// .context-manifest.json. Returns the SHA-256 digest of the exact bytes written.
func WriteContextManifest(worktreePath string, manifest *ContextManifest) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("context manifest is nil")
	}
	manifest.ManifestVersion = ContextManifestVersion

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling context manifest: %w", err)
	}
	data = append(data, '\n')

	digest := sha256Content(data)
	manifestPath := filepath.Join(worktreePath, ContextManifestName)
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", ContextManifestName, err)
	}
	return digest, nil
}

// ReadContextManifest reads and validates the context manifest from the
// worktree. Returns the parsed manifest or an error.
func ReadContextManifest(worktreePath string) (*ContextManifest, error) {
	manifestPath := filepath.Join(worktreePath, ContextManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ContextManifestName, err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()

	var manifest ContextManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ContextManifestName, err)
	}

	if decoder.More() {
		return nil, fmt.Errorf("%s: trailing data after JSON object", ContextManifestName)
	}

	if err := ValidateContextManifest(&manifest); err != nil {
		return nil, fmt.Errorf("validating %s: %w", ContextManifestName, err)
	}

	return &manifest, nil
}

// ValidateContextManifest checks the structural validity of a context manifest.
func ValidateContextManifest(manifest *ContextManifest) error {
	if manifest == nil {
		return fmt.Errorf("context manifest is nil")
	}
	if manifest.ManifestVersion != ContextManifestVersion {
		return fmt.Errorf("unsupported version: %q", manifest.ManifestVersion)
	}
	if manifest.Revision < 1 {
		return fmt.Errorf("revision must be >= 1, got %d", manifest.Revision)
	}
	if manifest.Budgets.MaxImplementation < 1 {
		return fmt.Errorf("max_implementation must be >= 1, got %d", manifest.Budgets.MaxImplementation)
	}
	if manifest.Budgets.MaxTest < 0 {
		return fmt.Errorf("max_test must be >= 0, got %d", manifest.Budgets.MaxTest)
	}
	if manifest.Budgets.MaxArchitecture < 0 {
		return fmt.Errorf("max_architecture must be >= 0, got %d", manifest.Budgets.MaxArchitecture)
	}

	seen := make(map[string]bool)
	for _, entry := range manifest.Entries {
		if entry.Path == "" {
			return fmt.Errorf("entry with empty path")
		}
		if !sha256Regex.MatchString(entry.SHA256) {
			return fmt.Errorf("invalid SHA-256 digest for %q: %q", entry.Path, entry.SHA256)
		}
		if entry.Revision == "" {
			return fmt.Errorf("entry %q has empty revision", entry.Path)
		}
		if entry.Reason == "" {
			return fmt.Errorf("entry %q has empty reason", entry.Path)
		}
		switch entry.Category {
		case ContextCategoryImplementation, ContextCategoryTest, ContextCategoryArchitecture:
			// valid
		default:
			return fmt.Errorf("entry %q has unknown category: %q", entry.Path, entry.Category)
		}
		if seen[entry.Path] {
			return fmt.Errorf("duplicate entry: %q", entry.Path)
		}
		seen[entry.Path] = true
	}

	// Verify category budget counts.
	counts := map[ContextManifestEntryCategory]int{
		ContextCategoryImplementation: 0,
		ContextCategoryTest:           0,
		ContextCategoryArchitecture:   0,
	}
	for _, entry := range manifest.Entries {
		counts[entry.Category]++
	}
	if counts[ContextCategoryImplementation] > manifest.Budgets.MaxImplementation {
		return fmt.Errorf("implementation entries %d exceeds budget %d", counts[ContextCategoryImplementation], manifest.Budgets.MaxImplementation)
	}
	if counts[ContextCategoryTest] > manifest.Budgets.MaxTest {
		return fmt.Errorf("test entries %d exceeds budget %d", counts[ContextCategoryTest], manifest.Budgets.MaxTest)
	}
	if counts[ContextCategoryArchitecture] > manifest.Budgets.MaxArchitecture {
		return fmt.Errorf("architecture entries %d exceeds budget %d", counts[ContextCategoryArchitecture], manifest.Budgets.MaxArchitecture)
	}

	return nil
}
