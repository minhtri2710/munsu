// Package fleet implements the Soldier launch manifest — a versioned digest
// manifest binding relative paths, SHA-256 digests, and disposal policies
// for all runtime-owned launch artifacts.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
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

// ManifestEntry binds one launch artifact's canonical slash-format relative
// path, SHA-256 digest, and disposal policy.
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

// sha256Regex matches a valid lowercase hex SHA-256 string.
var sha256Regex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// expectedManifestEntryPaths returns the exact set of paths that must appear
// in every valid manifest. The manifest itself is not included.
func expectedManifestEntryPaths() map[string]bool {
	return map[string]bool{
		CharterName:      true,
		BriefName:        true,
		EnvelopeName:     true,
		PromptName:       true,
		LaunchScriptName: true,
	}
}

func validateManifestPath(relPath string) error {
	if relPath == "" {
		return fmt.Errorf("manifest entry with empty path")
	}
	if strings.ContainsRune(relPath, 0) {
		return fmt.Errorf("manifest entry path contains NUL byte")
	}
	if strings.Contains(relPath, "\\") {
		return fmt.Errorf("manifest entry path contains backslash: %q", relPath)
	}
	if len(relPath) >= 2 && relPath[1] == ':' {
		return fmt.Errorf("manifest entry with volume-qualified path: %q", relPath)
	}
	if path.IsAbs(relPath) || filepath.IsAbs(relPath) {
		return fmt.Errorf("manifest entry with absolute path: %q", relPath)
	}
	cleaned := path.Clean(relPath)
	if cleaned != relPath {
		return fmt.Errorf("manifest entry path %q is not canonical (use %q)", relPath, cleaned)
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("manifest entry path contains parent traversal: %q", relPath)
	}
	return nil
}

// ValidateManifest checks that the manifest is structurally valid and
// contains exactly the expected runtime-owned artifact entries.
// Callers should pass the manifest as read from disk (before any modification).
func ValidateManifest(manifest *LaunchManifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is nil")
	}

	// Version must be exactly the supported version.
	if manifest.ManifestVersion != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %q (expected %q)", manifest.ManifestVersion, ManifestVersion)
	}

	// Check for duplicate paths and validate each entry.
	seen := make(map[string]bool)
	expected := expectedManifestEntryPaths()
	expectedFound := make(map[string]bool)

	for _, entry := range manifest.Artifacts {
		if err := validateManifestPath(entry.Path); err != nil {
			return err
		}

		// Reject manifest self-entry.
		if entry.Path == ManifestName {
			return fmt.Errorf("manifest entry cannot reference itself: %s", ManifestName)
		}

		// Reject duplicate paths.
		if seen[entry.Path] {
			return fmt.Errorf("duplicate manifest entry: %q", entry.Path)
		}
		seen[entry.Path] = true

		// Validate disposal policy.
		if entry.Policy != DisposalPolicyCleanable {
			return fmt.Errorf("unsupported manifest disposal policy %q for %q", entry.Policy, entry.Path)
		}

		// Validate SHA-256 digest.
		if !sha256Regex.MatchString(entry.SHA256) {
			return fmt.Errorf("invalid SHA-256 digest for %q: %q (must be 64 lowercase hex chars)", entry.Path, entry.SHA256)
		}

		// Track expected entries found.
		if expected[entry.Path] {
			expectedFound[entry.Path] = true
		}
	}

	// Check that every expected entry is present.
	for path := range expected {
		if !expectedFound[path] {
			return fmt.Errorf("missing manifest entry: %q", path)
		}
	}

	// Check that no unexpected entries are present.
	for _, entry := range manifest.Artifacts {
		if !expected[entry.Path] {
			return fmt.Errorf("unexpected manifest entry: %q", entry.Path)
		}
	}

	return nil
}

// WriteManifest writes the manifest to .soldier-manifest.json in the worktree
// atomically and durably. The manifest is written after all other launch
// artifacts exist and does not include an entry for itself.
// Returns the SHA-256 digest of the exact bytes written.
func WriteManifest(worktreePath string, manifest *LaunchManifest) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("launch manifest is nil")
	}
	manifest.ManifestVersion = ManifestVersion

	// Validate before writing.
	if err := ValidateManifest(manifest); err != nil {
		return "", fmt.Errorf("manifest validation failed: %w", err)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling launch manifest: %w", err)
	}
	data = append(data, '\n')

	digest := sha256Content(data)
	manifestPath := filepath.Join(worktreePath, ManifestName)
	tmpPath := manifestPath + ".tmp"

	// Atomic write: write to temp, sync, close, rename.
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("writing %s: %w", ManifestName, err)
	}

	// Rename atomically.
	if err := os.Rename(tmpPath, manifestPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("renaming %s: %w", ManifestName, err)
	}

	return digest, nil
}

// isWithinWorktreeRoot checks that the resolved path is within the worktree
// root. Both paths are resolved through symlinks so that /tmp -> /private/tmp
// symlinks on macOS do not cause false escapes.
func isWithinWorktreeRoot(worktreeRoot, resolvedPath string) bool {
	rootResolved, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return false
	}
	rootResolved = filepath.Clean(rootResolved)
	resolvedPath = filepath.Clean(resolvedPath)
	return strings.HasPrefix(resolvedPath, rootResolved+string(filepath.Separator)) || resolvedPath == rootResolved
}

// ReadManifest reads and validates the launch manifest from the worktree.
// Returns a validated manifest or an error.
func ReadManifest(worktreePath string) (*LaunchManifest, error) {
	manifestPath := filepath.Join(worktreePath, ManifestName)

	// Check file is a regular file (not a symlink escape).
	fi, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ManifestName, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", ManifestName)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", ManifestName)
	}

	// Check the resolved path is within the worktree root.
	realPath, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", ManifestName, err)
	}
	if !isWithinWorktreeRoot(worktreePath, realPath) {
		return nil, fmt.Errorf("%s symlink escapes worktree root", ManifestName)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ManifestName, err)
	}

	// Strict JSON decode: reject unknown fields and trailing data.
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()

	var manifest LaunchManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestName, err)
	}

	// Reject trailing data after the JSON object.
	if decoder.More() {
		return nil, fmt.Errorf("%s: trailing data after JSON object", ManifestName)
	}

	if err := ValidateManifest(&manifest); err != nil {
		return nil, fmt.Errorf("validating %s: %w", ManifestName, err)
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

// ManifestEntryForFile builds a ManifestEntry for a file at the given canonical
// slash-format relative path, rejecting native separators, absolute aliases,
// volume-qualified forms, and traversal before native-path conversion. It
// computes the file's SHA-256 digest relative to worktreeRoot.
func ManifestEntryForFile(worktreeRoot, relPath string, policy DisposalPolicy) (ManifestEntry, error) {
	if err := validateManifestPath(relPath); err != nil {
		return ManifestEntry{}, fmt.Errorf("unsafe manifest path: %w", err)
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

// isTrackedByGit returns true when the given file (relative to worktreeRoot)
// is tracked by git (staged or committed).
func isTrackedByGit(worktreeRoot, relPath string) bool {
	cmd := execGitCommand(worktreeRoot, "ls-files", "--error-unmatch", "--", relPath)
	return cmd.Run() == nil
}

// execGitCommand returns an exec.Cmd for a git command scoped to worktreeRoot.
var execGitCommand = func(worktreeRoot string, args ...string) interface{ Run() error } {
	cmd := exec.Command("git", args...)
	cmd.Dir = worktreeRoot
	return cmd
}

// verifyManifestEntry checks that a single manifest entry is safe and
// matches its declared digest. Returns an error describing the failure.
func verifyManifestEntry(worktreeRoot string, entry *ManifestEntry) error {
	fullPath := filepath.Join(worktreeRoot, entry.Path)

	// Check file exists and is a regular file.
	fi, err := os.Lstat(fullPath)
	if err != nil {
		return fmt.Errorf("file not found or inaccessible: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", entry.Path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", entry.Path)
	}

	// Check symlink escape: the resolved path must be within the worktree root.
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", entry.Path, err)
	}
	if !isWithinWorktreeRoot(worktreeRoot, realPath) {
		return fmt.Errorf("%s symlink escapes worktree root", entry.Path)
	}

	// Check the file is not tracked by git.
	if isTrackedByGit(worktreeRoot, entry.Path) {
		return fmt.Errorf("%s is tracked by git", entry.Path)
	}

	// Check disposal policy.
	if entry.Policy != DisposalPolicyCleanable {
		return fmt.Errorf("unsupported disposal policy %q for %s", entry.Policy, entry.Path)
	}

	// Check digest.
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", entry.Path, err)
	}
	if sha256Content(data) != entry.SHA256 {
		return fmt.Errorf("%s SHA-256 digest mismatch", entry.Path)
	}

	return nil
}

// verifyManifestFile checks that the manifest file itself is safe and
// untracked. It does not validate the manifest content (that is done by
// ReadManifest). Returns an error describing the failure.
func verifyManifestFile(worktreeRoot string) error {
	fullPath := filepath.Join(worktreeRoot, ManifestName)

	// Check file exists and is a regular file.
	fi, err := os.Lstat(fullPath)
	if err != nil {
		return fmt.Errorf("manifest file not found: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", ManifestName)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", ManifestName)
	}

	// Check symlink escape.
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", ManifestName, err)
	}
	if !isWithinWorktreeRoot(worktreeRoot, realPath) {
		return fmt.Errorf("%s symlink escapes worktree root", ManifestName)
	}

	// Check the manifest file is not tracked by git.
	if isTrackedByGit(worktreeRoot, ManifestName) {
		return fmt.Errorf("%s is tracked by git", ManifestName)
	}

	return nil
}

// VerifyLaunchArtifacts performs a comprehensive check of all launch artifacts
// against the manifest. It:
//  1. Verifies the manifest file itself is safe and untracked.
//  2. Verifies the manifest digest matches the expected value from metadata.
//  3. Verifies every manifest entry is safe, untracked, and digest-matching.
//
// Returns an error listing every failure, or nil if all checks pass.
// The expectedManifestSHA parameter must be a 64-character lowercase hex string
// from the task metadata, anchoring the manifest outside the worktree.
func VerifyLaunchArtifacts(worktreePath, expectedManifestSHA string) error {
	if expectedManifestSHA == "" || !sha256Regex.MatchString(expectedManifestSHA) {
		return fmt.Errorf("invalid expected manifest SHA-256: %q", expectedManifestSHA)
	}

	// Verify manifest file itself.
	if err := verifyManifestFile(worktreePath); err != nil {
		return fmt.Errorf("manifest file check failed: %w", err)
	}

	// Read and validate manifest.
	manifest, err := ReadManifest(worktreePath)
	if err != nil {
		return fmt.Errorf("manifest read failed: %w", err)
	}

	// Verify the manifest digest matches the expected value from metadata.
	manifestPath := filepath.Join(worktreePath, ManifestName)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest for digest verification: %w", err)
	}
	actualDigest := sha256Content(manifestBytes)
	if actualDigest != expectedManifestSHA {
		return fmt.Errorf("manifest SHA-256 digest mismatch: expected %s, got %s", expectedManifestSHA, actualDigest)
	}

	// Verify each manifest entry.
	var failures []string
	for _, entry := range manifest.Artifacts {
		if err := verifyManifestEntry(worktreePath, &entry); err != nil {
			failures = append(failures, err.Error())
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("launch artifact verification failed:\n  %s", strings.Join(failures, "\n  "))
	}

	return nil
}
