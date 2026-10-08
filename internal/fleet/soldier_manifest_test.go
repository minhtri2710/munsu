//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// launchManifestNames returns the paths covered by a harness manifest for fixtures.
func launchManifestNames(harnessName string) []string {
	names := append([]string{}, CoreLaunchArtifactNames...)
	if adapter, ok := harness.GetAdapter(harnessName); ok {
		names = append(names, adapter.SoldierLaunch.WorktreeFiles...)
	}
	return names
}

// manifestEntryForTestFile builds a fixture entry from a file's exact bytes.
func manifestEntryForTestFile(t *testing.T, root, relPath string, policy DisposalPolicy) ManifestEntry {
	t.Helper()
	if err := validateManifestPath(relPath); err != nil {
		t.Fatalf("unsafe manifest fixture path %q: %v", relPath, err)
	}
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("reading manifest fixture %s: %v", relPath, err)
	}
	return ManifestEntry{Path: relPath, SHA256: sha256Content(data), Policy: policy}
}

func addDeferredGuardFixture(t *testing.T, root, taskID, launchID, generation, endpointFence string) ManifestEntry {
	t.Helper()
	guardDir := ".soldier-launch-guard-" + labelComponent(taskID) + "-" + generation
	guardPath := filepath.Join(root, guardDir, "identity")
	if err := os.MkdirAll(filepath.Join(root, guardDir), 0o755); err != nil {
		t.Fatalf("creating deferred guard fixture directory: %v", err)
	}
	identity := launchID + "|" + generation + "|" + endpointFence
	if err := os.WriteFile(guardPath, []byte(identity), 0o644); err != nil {
		t.Fatalf("writing deferred guard fixture: %v", err)
	}
	return manifestEntryForTestFile(t, root, filepath.ToSlash(filepath.Join(guardDir, "identity")), DisposalPolicyCleanable)
}

// writeTestManifest marshals a valid test manifest and writes its exact bytes.
// Production launch publication uses the canonical anchor and AtomicCreate.
func writeTestManifest(t *testing.T, dir string, entries []ManifestEntry, guardIdentity ...string) string {
	t.Helper()
	if entries == nil {
		entries = make([]ManifestEntry, 0, len(CoreLaunchArtifactNames))
		for _, name := range CoreLaunchArtifactNames {
			entries = append(entries, manifestEntryForTestFile(t, dir, name, DisposalPolicyCleanable))
		}
	}
	guard := guardIdentity
	if len(guard) == 0 {
		guard = []string{"test", "launch-test", "1", "endpoint-fence-test"}
	}
	entries = append(entries, addDeferredGuardFixture(t, dir, guard[0], guard[1], guard[2], guard[3]))
	data, digest, err := MarshalManifest(BuildManifest(entries))
	if err != nil {
		t.Fatalf("marshaling manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0644); err != nil {
		t.Fatalf("writing manifest fixture: %v", err)
	}
	return digest
}

// writeTestManifestRaw writes raw JSON content as the manifest file, bypassing
// MarshalManifest's validation. Useful for testing ReadManifest validation.
func writeTestManifestRaw(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing raw manifest: %v", err)
	}
}

// setupTestLaunchFiles creates all required launch artifact files in dir.
func setupTestLaunchFiles(t *testing.T, dir string) {
	t.Helper()
	charter := DefaultCharter("test", "ship", "direct-PR")
	os.WriteFile(filepath.Join(dir, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(dir, BriefName), []byte("brief"), 0644)
	os.WriteFile(filepath.Join(dir, EnvelopeName), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, PromptName), []byte("prompt"), 0644)
	os.WriteFile(filepath.Join(dir, LaunchScriptName), []byte("#!/bin/bash\necho hi\n"), 0644)
}

// writePiSettingsFixture writes the worktree .pi/settings.json the way a pi
// soldier launch does; the pi manifest binds it as its sixth entry.
func writePiSettingsFixture(t *testing.T, dir string) {
	t.Helper()
	if err := writePiProjectSettings(dir, true); err != nil {
		t.Fatalf("writing %s: %v", PiSettingsName, err)
	}
}

func TestManifest_MarshalAndRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		harness  string
		settings bool
		want     []string
	}{
		{"claude", "claude", false, []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName}},
		{"pi", "pi", true, []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName, ".pi/settings.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			setupTestLaunchFiles(t, tmp)
			if tc.settings {
				writePiSettingsFixture(t, tmp)
			}
			var entries []ManifestEntry
			for _, name := range launchManifestNames(tc.harness) {
				entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
			}
			digest := writeTestManifest(t, tmp, entries, tc.harness, "launch-test", "1", "endpoint-fence-test")

			got, err := ReadManifest(tmp)
			if err != nil {
				t.Fatal(err)
			}
			if got.ManifestVersion != ManifestVersion {
				t.Errorf("ManifestVersion = %q, want %q", got.ManifestVersion, ManifestVersion)
			}
			var paths []string
			for _, a := range got.Artifacts {
				paths = append(paths, a.Path)
			}
			want := append(append([]string(nil), tc.want...), ".soldier-launch-guard-"+labelComponent(tc.harness)+"-1/identity")
			if strings.Join(paths, ",") != strings.Join(want, ",") {
				t.Fatalf("artifact paths = %v, want %v", paths, want)
			}

			// Verify marshaling returned a valid 64-char hex digest.
			if len(digest) != 64 {
				t.Errorf("manifest digest length = %d, want 64", len(digest))
			}
		})
	}
}

func TestManifest_MarshalReturnsDigest(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest1 := writeTestManifest(t, tmp, nil)

	// Write again with same content - digest should be the same.
	digest2 := writeTestManifest(t, tmp, nil)

	if digest1 != digest2 {
		t.Errorf("deterministic write should produce same digest: %s != %s", digest1, digest2)
	}

	// Verify the digest matches the actual file.
	manifestBytes, err := os.ReadFile(filepath.Join(tmp, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Content(manifestBytes) != digest1 {
		t.Error("marshaled digest does not match written file")
	}
}

func TestManifest_Lookup(t *testing.T) {
	manifest := BuildManifest([]ManifestEntry{
		{Path: ".soldier-brief.md", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable},
		{Path: ".soldier-charter.md", SHA256: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", Policy: DisposalPolicyCleanable},
	})
	entry := manifest.Lookup(".soldier-brief.md")
	if entry == nil {
		t.Fatal("expected to find .soldier-brief.md")
	}
	if entry.Policy != DisposalPolicyCleanable {
		t.Errorf("Policy = %q, want %q", entry.Policy, DisposalPolicyCleanable)
	}
	if manifest.Lookup("unknown.txt") != nil {
		t.Error("expected nil for unknown path")
	}
}

func TestManifest_NilLookup(t *testing.T) {
	var m *LaunchManifest
	if m.Lookup("anything") != nil {
		t.Error("nil manifest Lookup should return nil")
	}
}

func TestManifest_NilArtifactPaths(t *testing.T) {
	var m *LaunchManifest
	if m.ArtifactPaths() != nil {
		t.Error("nil manifest ArtifactPaths should return nil")
	}
}

func TestManifest_ArtifactPaths(t *testing.T) {
	manifest := BuildManifest([]ManifestEntry{
		{Path: ".soldier-brief.md", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable},
		{Path: ".soldier-charter.md", SHA256: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", Policy: DisposalPolicyCleanable},
	})
	paths := manifest.ArtifactPaths()
	if !paths[".soldier-brief.md"] {
		t.Error("expected .soldier-brief.md in paths")
	}
	if !paths[".soldier-charter.md"] {
		t.Error("expected .soldier-charter.md in paths")
	}
	if paths["unknown.txt"] {
		t.Error("unexpected unknown.txt in paths")
	}
}

func TestManifestEntryForTestFile(t *testing.T) {
	tmp := t.TempDir()
	content := []byte("test content")
	relPath := ".soldier-brief.md"
	if err := os.WriteFile(filepath.Join(tmp, relPath), content, 0644); err != nil {
		t.Fatal(err)
	}
	entry := manifestEntryForTestFile(t, tmp, relPath, DisposalPolicyCleanable)
	if entry.Path != relPath {
		t.Errorf("Path = %q, want %q", entry.Path, relPath)
	}
	if entry.SHA256 != sha256Content(content) {
		t.Errorf("SHA256 = %q, want %q", entry.SHA256, sha256Content(content))
	}
	if entry.Policy != DisposalPolicyCleanable {
		t.Errorf("Policy = %q, want %q", entry.Policy, DisposalPolicyCleanable)
	}
}

func TestManifest_EntryForFile_UnsafePath(t *testing.T) {
	for _, test := range []struct {
		relPath string
		want    string
	}{
		{relPath: "../etc/passwd", want: "parent traversal"},
		{relPath: "/etc/passwd", want: "manifest entry with absolute path"},
		{relPath: "./foo", want: "not canonical"},
		{relPath: "dir\\file", want: "backslash"},
		{relPath: "C:foo", want: "manifest entry with volume-qualified path"},
		{relPath: "C:/foo", want: "manifest entry with volume-qualified path"},
	} {
		err := validateManifestPath(test.relPath)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("validateManifestPath(%q) error = %v, want substring %q", test.relPath, err, test.want)
		}
	}
}

func TestManifestEntryForTestFileMissingFile(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(t.TempDir(), ".soldier-nonexistent.md")); err == nil {
		t.Fatal("expected missing fixture file")
	}
}

func TestManifest_ReadMissing(t *testing.T) {
	tmp := t.TempDir()
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error reading missing manifest")
	}
}

func TestManifest_MarshalNil(t *testing.T) {
	if _, _, err := MarshalManifest(nil); err == nil {
		t.Error("expected error marshaling nil manifest")
	}
}

func TestManifest_IntegrityCheck(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest := writeTestManifest(t, tmp, nil)

	// Verify the manifest file exists.
	manifestPath := filepath.Join(tmp, ManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}

	// Read back and verify entries.
	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != len(CoreLaunchArtifactNames)+1 {
		t.Fatalf("expected %d artifacts, got %d", len(CoreLaunchArtifactNames)+1, len(got.Artifacts))
	}

	// Verify each entry's digest matches the actual file.
	for _, entry := range got.Artifacts {
		data, err := os.ReadFile(filepath.Join(tmp, entry.Path))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Path, err)
		}
		if sha256Content(data) != entry.SHA256 {
			t.Errorf("%s digest mismatch", entry.Path)
		}
	}

	// Verify the returned digest matches the file.
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if sha256Content(manifestBytes) != digest {
		t.Error("marshaled digest does not match file")
	}
}

// =============================================================================
// Validation tests
// =============================================================================

func TestManifest_Validation_MissingEntries(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	// Manifest with only 4 of the required entries should fail.
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName} {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for missing LaunchScriptName entry")
	}
	if !strings.Contains(err.Error(), "missing manifest entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_RequiresOneDeferredGuardIdentity(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	entries := make([]ManifestEntry, 0, len(CoreLaunchArtifactNames))
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	if _, _, err := MarshalManifest(BuildManifest(entries)); err == nil || !strings.Contains(err.Error(), "missing manifest entry: deferred launch guard identity") {
		t.Fatalf("MarshalManifest error = %v, want missing deferred guard refusal", err)
	}

	entries = append(entries,
		ManifestEntry{Path: ".soldier-launch-guard-test-1/identity", SHA256: strings.Repeat("a", 64), Policy: DisposalPolicyCleanable},
		ManifestEntry{Path: ".soldier-launch-guard-other-1/identity", SHA256: strings.Repeat("b", 64), Policy: DisposalPolicyCleanable},
	)
	if err := ValidateManifest(BuildManifest(entries)); err == nil || !strings.Contains(err.Error(), "multiple deferred launch guard identities") {
		t.Fatalf("ValidateManifest error = %v, want duplicate deferred guard refusal", err)
	}
}

func TestManifest_Validation_UnexpectedEntry(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	// Add an unexpected entry.
	entries = append(entries, ManifestEntry{Path: "rogue.txt", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable})
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for unexpected entry")
	}
	if !strings.Contains(err.Error(), "unexpected manifest entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_DuplicateEntry(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	// Add a duplicate entry.
	entries = append(entries, ManifestEntry{Path: BriefName, SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable})
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for duplicate entry")
	}
	if !strings.Contains(err.Error(), "duplicate manifest entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_SelfEntry(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	// Replace one entry with manifest self-reference.
	entries[4] = ManifestEntry{Path: ManifestName, SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable}
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for manifest self-entry")
	}
	if !strings.Contains(err.Error(), "cannot reference itself") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_InvalidDigest(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	// Corrupt the digest.
	entries[0].SHA256 = "not-a-hex-digest"
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for invalid digest")
	}
	if !strings.Contains(err.Error(), "invalid SHA-256 digest") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_UnsupportedPolicy(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	entries[0].Policy = "delete-whenever"
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for unsupported policy")
	}
	if !strings.Contains(err.Error(), "unsupported manifest disposal policy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_TraversalPath(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	entries[0].Path = "../etc/passwd"
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for traversal path")
	}
	if !strings.Contains(err.Error(), "parent traversal") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_AbsolutePath(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test"))
	entries[0].Path = "/etc/passwd"
	manifest := BuildManifest(entries)
	_, _, err := MarshalManifest(manifest)
	if err == nil {
		t.Error("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Read_UnknownVersion(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v2","artifacts":[]}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for unknown manifest version")
	}
	if !strings.Contains(err.Error(), "unsupported manifest version") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Read_CorruptJSON(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{invalid json}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
}

func TestManifest_Read_TrailingData(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v1","artifacts":[]}{"extra":"data"}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for trailing data")
	}
	if !strings.Contains(err.Error(), "trailing data") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Read_UnknownFields(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v1","artifacts":[],"unknown_field":"value"}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for unknown fields")
	}
}

func TestManifest_Read_BackslashPath(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v1","artifacts":[{"path":"a\\b","sha256":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789","policy":"cleanable"}]}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for backslash path")
	}
	if !strings.Contains(err.Error(), "backslash") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Read_EmptyPath(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v1","artifacts":[{"path":"","sha256":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789","policy":"cleanable"}]}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestManifest_Read_VolumeQualifiedPath(t *testing.T) {
	tmp := t.TempDir()
	writeTestManifestRaw(t, tmp, `{"manifest_version":"soldier-manifest-v1","artifacts":[{"path":"C:foo","sha256":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789","policy":"cleanable"}]}`)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for volume-qualified path")
	}
	if !strings.Contains(err.Error(), "volume") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_NotInItsOwnEntries(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest := writeTestManifest(t, tmp, nil)

	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	if got.Lookup(ManifestName) != nil {
		t.Error("manifest must not include itself in its own entries")
	}

	// Verify the digest is valid.
	if len(digest) != 64 {
		t.Errorf("manifest digest length = %d, want 64", len(digest))
	}
}

// =============================================================================
// VerifyLaunchArtifacts tests
// =============================================================================

func TestVerifyLaunchArtifacts_Canonical(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	digest := writeTestManifest(t, tmp, nil)

	err := VerifyLaunchArtifacts(tmp, digest)
	if err != nil {
		t.Fatalf("canonical launch artifacts should verify: %v", err)
	}
}

func TestVerifyPreparedLaunchArtifactsAllowsOnlyAbsentOrExactGuard(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	guardEntry := addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test")
	guardDir := filepath.Dir(filepath.Join(tmp, filepath.FromSlash(guardEntry.Path)))
	if err := os.Remove(filepath.Join(tmp, filepath.FromSlash(guardEntry.Path))); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(guardDir); err != nil {
		t.Fatal(err)
	}
	entries := make([]ManifestEntry, 0, len(CoreLaunchArtifactNames)+1)
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
	}
	entries = append(entries, guardEntry)
	manifestBytes, digest, err := MarshalManifest(BuildManifest(entries))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ManifestName), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPreparedLaunchArtifacts(tmp, digest); err != nil {
		t.Fatalf("prepared verification should allow absent guard: %v", err)
	}
	if err := VerifyLaunchArtifacts(tmp, digest); err == nil {
		t.Fatal("strict verification accepted absent guard")
	}

	if err := os.Mkdir(guardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guardDir, "identity"), []byte("launch-test|1|endpoint-fence-test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLaunchArtifacts(tmp, digest); err != nil {
		t.Fatalf("strict verification rejected exact guard: %v", err)
	}
	identityPath := filepath.Join(guardDir, "identity")
	if err := os.WriteFile(identityPath, []byte("tampered|1|endpoint-fence-test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPreparedLaunchArtifacts(tmp, digest); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("prepared verification accepted a foreign guard identity: %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("launch-test|1|endpoint-fence-test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guardDir, "extra"), []byte("unowned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPreparedLaunchArtifacts(tmp, digest); err == nil || !strings.Contains(err.Error(), "unexpected contents") {
		t.Fatalf("prepared verification error = %v, want extra guard child refusal", err)
	}
}

func TestVerifyLaunchArtifactsRejectsGuardSymlinkComponents(t *testing.T) {
	for _, tc := range []string{"directory", "identity"} {
		t.Run(tc, func(t *testing.T) {
			tmp := t.TempDir()
			setupTestLaunchFiles(t, tmp)
			guardEntry := addDeferredGuardFixture(t, tmp, "test", "launch-test", "1", "endpoint-fence-test")
			guardDir := filepath.Dir(filepath.Join(tmp, filepath.FromSlash(guardEntry.Path)))
			entries := make([]ManifestEntry, 0, len(CoreLaunchArtifactNames)+1)
			for _, name := range CoreLaunchArtifactNames {
				entries = append(entries, manifestEntryForTestFile(t, tmp, name, DisposalPolicyCleanable))
			}
			entries = append(entries, guardEntry)
			digest := writeManifestEntries(t, tmp, entries)
			outside := t.TempDir()
			if tc == "directory" {
				if err := os.Remove(filepath.Join(guardDir, "identity")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(guardDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, guardDir); err != nil {
					t.Skipf("directory symlinks unavailable: %v", err)
				}
			} else {
				if err := os.Remove(filepath.Join(guardDir, "identity")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "identity"), filepath.Join(guardDir, "identity")); err != nil {
					t.Skipf("file symlinks unavailable: %v", err)
				}
			}
			if err := VerifyPreparedLaunchArtifacts(tmp, digest); err == nil {
				t.Fatal("prepared verification accepted a symlinked guard component")
			}
		})
	}
}

func writeManifestEntries(t *testing.T, dir string, entries []ManifestEntry) string {
	t.Helper()
	data, digest, err := MarshalManifest(BuildManifest(entries))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestVerifyLaunchArtifacts_EmptyExpectedSHA(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil)

	err := VerifyLaunchArtifacts(tmp, "")
	if err == nil {
		t.Error("expected error for empty expected SHA")
	}
}

func TestVerifyLaunchArtifacts_WrongExpectedSHA(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil)

	err := VerifyLaunchArtifacts(tmp, "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	if err == nil {
		t.Error("expected error for wrong expected SHA")
	}
	if !strings.Contains(err.Error(), "SHA-256 digest mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyLaunchArtifacts_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	digest := writeTestManifest(t, tmp, nil)

	// Remove the brief file.
	os.Remove(filepath.Join(tmp, BriefName))

	err := VerifyLaunchArtifacts(tmp, digest)
	if err == nil {
		t.Error("expected error for missing brief file")
	}
}
