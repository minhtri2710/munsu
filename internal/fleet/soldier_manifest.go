// Package fleet implements the Soldier launch manifest — a versioned digest
// manifest binding relative paths, SHA-256 digests, and disposal policies
// for all runtime-owned launch artifacts.
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
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

var errDeferredLaunchGuardAbsent = errors.New("deferred launch guard identity is absent; readiness is not proven")

// CoreLaunchArtifactNames lists the runtime-owned launch artifacts, relative to
// the worktree, that every soldier launch manifest binds. The manifest itself
// is not included. A harness adds the worktree files it declares in
// SoldierLaunchContract.WorktreeFiles.
var CoreLaunchArtifactNames = []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName}

// declaredWorktreeFiles returns every worktree file some harness adapter
// declares: the only extra paths a valid manifest may carry.
func declaredWorktreeFiles() map[string]bool {
	declared := map[string]bool{}
	for _, adapter := range harness.Adapters {
		for _, name := range adapter.SoldierLaunch.WorktreeFiles {
			declared[name] = true
		}
	}
	return declared
}

// expectedManifestEntryPaths returns the paths that must appear in every
// valid manifest.
func expectedManifestEntryPaths() map[string]bool {
	expected := make(map[string]bool, len(CoreLaunchArtifactNames))
	for _, name := range CoreLaunchArtifactNames {
		expected[name] = true
	}
	return expected
}

func isDeferredGuardIdentityPath(relPath string) bool {
	const prefix = ".soldier-launch-guard-"
	const suffix = "/identity"
	if !strings.HasPrefix(relPath, prefix) || !strings.HasSuffix(relPath, suffix) {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(relPath, prefix), suffix)
	dash := strings.LastIndexByte(name, '-')
	if dash <= 0 || dash == len(name)-1 {
		return false
	}
	taskLabel, generation := name[:dash], name[dash+1:]
	for _, r := range taskLabel {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	parsed, err := strconv.ParseUint(generation, 10, 64)
	return taskLabel != "" && err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == generation
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
// contains every core launch artifact and no path beyond those and the
// worktree files a harness adapter declares.
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
	declared := declaredWorktreeFiles()
	expectedFound := make(map[string]bool)
	guardFound := false

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
		if isDeferredGuardIdentityPath(entry.Path) {
			if guardFound {
				return fmt.Errorf("manifest contains multiple deferred launch guard identities")
			}
			guardFound = true
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
		if !expected[entry.Path] && !declared[entry.Path] && !isDeferredGuardIdentityPath(entry.Path) {
			return fmt.Errorf("unexpected manifest entry: %q", entry.Path)
		}
	}
	if !guardFound {
		return fmt.Errorf("missing manifest entry: deferred launch guard identity")
	}

	return nil
}

func MarshalManifest(manifest *LaunchManifest) ([]byte, string, error) {
	if manifest == nil {
		return nil, "", fmt.Errorf("launch manifest is nil")
	}
	copy := *manifest
	copy.ManifestVersion = ManifestVersion
	if err := ValidateManifest(&copy); err != nil {
		return nil, "", fmt.Errorf("manifest validation failed: %w", err)
	}
	data, err := json.MarshalIndent(&copy, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("marshaling launch manifest: %w", err)
	}
	data = append(data, '\n')
	return data, sha256Content(data), nil
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
	fullPath := filepath.Join(worktreeRoot, filepath.FromSlash(entry.Path))

	// Refuse symlinked parent components as well as a symlinked leaf.
	parent := worktreeRoot
	parts := strings.Split(entry.Path, "/")
	for _, part := range parts[:len(parts)-1] {
		parent = filepath.Join(parent, filepath.FromSlash(part))
		parentInfo, err := os.Lstat(parent)
		if err != nil {
			return fmt.Errorf("checking path component for %s: %w", entry.Path, err)
		}
		if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			return fmt.Errorf("%s has an unsafe path component %q", entry.Path, part)
		}
	}

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

func verifyDeferredGuardDirectory(worktreePath, guardDir string, allowAbsent bool) error {
	if guardDir == "" {
		return fmt.Errorf("manifest has no deferred launch guard identity")
	}
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return fmt.Errorf("reading worktree launch artifacts: %w", err)
	}
	prefix := ".soldier-launch-guard-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && entry.Name() != filepath.Base(guardDir) {
			return fmt.Errorf("unexpected deferred launch guard directory %q", entry.Name())
		}
	}
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return fmt.Errorf("opening worktree root for guard verification: %w", err)
	}
	defer root.Close()
	fullDir := filepath.FromSlash(guardDir)
	fi, err := root.Lstat(fullDir)
	if os.IsNotExist(err) && allowAbsent {
		return nil
	}
	if os.IsNotExist(err) && !allowAbsent {
		return errDeferredLaunchGuardAbsent
	}
	if err != nil {
		return fmt.Errorf("checking deferred launch guard directory: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("deferred launch guard %s is not a real directory", guardDir)
	}
	guardRoot, err := root.OpenRoot(fullDir)
	if err != nil {
		return fmt.Errorf("opening deferred launch guard %s: %w", guardDir, err)
	}
	defer guardRoot.Close()
	guardDirFile, err := guardRoot.Open(".")
	if err != nil {
		return fmt.Errorf("opening deferred launch guard %s directory: %w", guardDir, err)
	}
	defer guardDirFile.Close()
	children, err := guardDirFile.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("reading deferred launch guard %s: %w", guardDir, err)
	}
	if len(children) != 1 || children[0].Name() != "identity" || children[0].IsDir() {
		return fmt.Errorf("deferred launch guard %s has unexpected contents", guardDir)
	}
	identityInfo, err := guardRoot.Lstat("identity")
	if err != nil {
		return fmt.Errorf("checking deferred launch guard identity: %w", err)
	}
	if identityInfo.Mode()&os.ModeSymlink != 0 || !identityInfo.Mode().IsRegular() {
		return fmt.Errorf("deferred launch guard identity is not a regular file")
	}
	identityBytes, err := guardRoot.ReadFile("identity")
	if err != nil {
		return fmt.Errorf("reading deferred launch guard identity: %w", err)
	}
	manifest, err := ReadManifest(worktreePath)
	if err != nil {
		return err
	}
	for _, artifact := range manifest.Artifacts {
		if isDeferredGuardIdentityPath(artifact.Path) {
			if sha256Content(identityBytes) != artifact.SHA256 {
				return fmt.Errorf("deferred launch guard identity digest mismatch")
			}
		}
	}
	return nil
}

// VerifyLaunchArtifacts performs a comprehensive check of all launch artifacts
// against the independently anchored manifest digest. It verifies the manifest
// file is safe and untracked, compares its exact bytes to the expected digest,
// then verifies every declared artifact is safe, untracked, and digest-matching.
// It returns all artifact failures (including their underlying causes), or nil.
type launchArtifactVerificationError struct {
	failures []error
}

func (e *launchArtifactVerificationError) Error() string {
	messages := make([]string, len(e.failures))
	for i, failure := range e.failures {
		messages[i] = failure.Error()
	}
	return fmt.Sprintf("launch artifact verification failed:\n  %s", strings.Join(messages, "\n  "))
}

func (e *launchArtifactVerificationError) Unwrap() []error { return e.failures }

func artifactVerificationFailures(err error) []error {
	var verification *launchArtifactVerificationError
	if errors.As(err, &verification) {
		return verification.failures
	}
	return nil
}

func VerifyLaunchArtifacts(worktreePath, expectedManifestSHA string) error {
	return verifyLaunchArtifacts(worktreePath, expectedManifestSHA, false)
}

// VerifyPreparedLaunchArtifacts verifies anchored launch files before submission.
// The exact deferred guard may not exist until the submitted script atomically
// creates it; if present, it must already match the canonical identity.
func VerifyPreparedLaunchArtifacts(worktreePath, expectedManifestSHA string) error {
	return verifyLaunchArtifacts(worktreePath, expectedManifestSHA, true)
}

func verifyLaunchArtifacts(worktreePath, expectedManifestSHA string, allowAbsentGuard bool) error {
	if _, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("checking worktree %s: %w", worktreePath, err)
	}
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

	// The guard is one exact directory: no alternate generation's guard or
	// additional children are accepted. Prepared state may omit it until the
	// submitted script reaches its atomic mkdir.
	guardPath := ""
	for _, entry := range manifest.Artifacts {
		if isDeferredGuardIdentityPath(entry.Path) {
			guardPath = filepath.ToSlash(filepath.Dir(entry.Path))
			break
		}
	}
	var failures []error
	if err := verifyDeferredGuardDirectory(worktreePath, guardPath, allowAbsentGuard); err != nil {
		failures = append(failures, err)
	}
	if err := verifyManifestEntries(worktreePath, manifest); err != nil {
		failures = append(failures, artifactVerificationFailures(err)...)
	}
	if len(failures) > 0 {
		return &launchArtifactVerificationError{failures: failures}
	}
	return nil
}

func verifyManifestEntries(worktreePath string, manifest *LaunchManifest) error {
	guardPath := ""
	for _, entry := range manifest.Artifacts {
		if isDeferredGuardIdentityPath(entry.Path) {
			guardPath = filepath.ToSlash(filepath.Dir(entry.Path))
			break
		}
	}
	// Verify each manifest entry.
	var failures []error
	for _, entry := range manifest.Artifacts {
		if isDeferredGuardIdentityPath(entry.Path) {
			guardDirPath := filepath.Join(worktreePath, filepath.FromSlash(guardPath))
			if _, err := os.Lstat(guardDirPath); os.IsNotExist(err) {
				continue
			}
		}
		if err := verifyManifestEntry(worktreePath, &entry); err != nil {
			failures = append(failures, err)
		}
	}

	if len(failures) > 0 {
		return &launchArtifactVerificationError{failures: failures}
	}

	return nil
}
