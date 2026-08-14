//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestManifest writes a minimal valid manifest with all required entries
// to the given directory. Returns the manifest digest.
func writeTestManifest(t *testing.T, dir string, entries []ManifestEntry, policy *LegacyBriefMigrationPolicy) string {
	t.Helper()
	if entries == nil {
		// Create default entries from actual files.
		entries = []ManifestEntry{}
		for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
			entry, err := ManifestEntryForFile(dir, name, DisposalPolicyCleanable)
			if err != nil {
				t.Fatalf("creating entry for %s: %v", name, err)
			}
			entries = append(entries, entry)
		}
	}
	manifest := BuildManifest(entries)
	manifest.LegacyBriefMigration = policy
	digest, err := WriteManifest(dir, manifest)
	if err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	return digest
}

// writeTestManifestRaw writes raw JSON content as the manifest file, bypassing
// WriteManifest's validation. Useful for testing ReadManifest validation.
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

func TestManifest_WriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest := writeTestManifest(t, tmp, nil, nil)

	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got.ManifestVersion != ManifestVersion {
		t.Errorf("ManifestVersion = %q, want %q", got.ManifestVersion, ManifestVersion)
	}
	if len(got.Artifacts) != 5 {
		t.Fatalf("expected 5 artifacts, got %d", len(got.Artifacts))
	}
	if got.LegacyBriefMigration != nil {
		t.Error("expected no legacy brief migration policy")
	}

	// Verify WriteManifest returned a valid 64-char hex digest.
	if len(digest) != 64 {
		t.Errorf("WriteManifest digest length = %d, want 64", len(digest))
	}
}

func TestManifest_WriteAndRead_WithLegacyPolicy(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	policy := LegacyBriefMatchCanonicalV1
	writeTestManifest(t, tmp, nil, &policy)

	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got.LegacyBriefMigration == nil {
		t.Fatal("expected legacy brief migration policy")
	}
	if *got.LegacyBriefMigration != LegacyBriefMatchCanonicalV1 {
		t.Errorf("policy = %q, want %q", *got.LegacyBriefMigration, LegacyBriefMatchCanonicalV1)
	}
}

func TestManifest_WriteReturnsDigest(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest1 := writeTestManifest(t, tmp, nil, nil)

	// Write again with same content - digest should be the same.
	digest2 := writeTestManifest(t, tmp, nil, nil)

	if digest1 != digest2 {
		t.Errorf("deterministic write should produce same digest: %s != %s", digest1, digest2)
	}

	// Verify the digest matches the actual file.
	manifestBytes, err := os.ReadFile(filepath.Join(tmp, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Content(manifestBytes) != digest1 {
		t.Error("returned digest does not match written file")
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

func TestManifest_EntryForFile(t *testing.T) {
	tmp := t.TempDir()
	content := []byte("test content")
	relPath := ".soldier-brief.md"
	if err := os.WriteFile(filepath.Join(tmp, relPath), content, 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := ManifestEntryForFile(tmp, relPath, DisposalPolicyCleanable)
	if err != nil {
		t.Fatal(err)
	}
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
	tmp := t.TempDir()
	_, err := ManifestEntryForFile(tmp, "../etc/passwd", DisposalPolicyCleanable)
	if err == nil {
		t.Error("expected error for unsafe path with ..")
	}
	_, err = ManifestEntryForFile(tmp, "/etc/passwd", DisposalPolicyCleanable)
	if err == nil {
		t.Error("expected error for absolute path")
	}
	_, err = ManifestEntryForFile(tmp, "./foo", DisposalPolicyCleanable)
	if err == nil {
		t.Error("expected error for non-canonical path ./foo")
	}
}

func TestManifest_EntryForFile_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	_, err := ManifestEntryForFile(tmp, ".soldier-nonexistent.md", DisposalPolicyCleanable)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestManifest_ReadMissing(t *testing.T) {
	tmp := t.TempDir()
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error reading missing manifest")
	}
}

func TestManifest_WriteManifestNil(t *testing.T) {
	_, err := WriteManifest(t.TempDir(), nil)
	if err == nil {
		t.Error("expected error writing nil manifest")
	}
}

func TestManifest_IntegrityCheck(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	digest := writeTestManifest(t, tmp, nil, nil)

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
	if len(got.Artifacts) != 5 {
		t.Fatalf("expected 5 artifacts, got %d", len(got.Artifacts))
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
		t.Error("WriteManifest digest does not match file")
	}
}

// =============================================================================
// Validation tests
// =============================================================================

func TestManifest_Validation_MissingEntries(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	// Manifest with only 4 of 5 required entries should fail.
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
	if err == nil {
		t.Error("expected error for missing LaunchScriptName entry")
	}
	if !strings.Contains(err.Error(), "missing manifest entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifest_Validation_UnexpectedEntry(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	// Add an unexpected entry.
	entries = append(entries, ManifestEntry{Path: "rogue.txt", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable})
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	// Add a duplicate entry.
	entries = append(entries, ManifestEntry{Path: BriefName, SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable})
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	// Replace one entry with manifest self-reference.
	entries[4] = ManifestEntry{Path: ManifestName, SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Policy: DisposalPolicyCleanable}
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	// Corrupt the digest.
	entries[0].SHA256 = "not-a-hex-digest"
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	entries[0].Policy = "delete-whenever"
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	entries[0].Path = "../etc/passwd"
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	entries[0].Path = "/etc/passwd"
	manifest := BuildManifest(entries)
	_, err := WriteManifest(tmp, manifest)
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

	digest := writeTestManifest(t, tmp, nil, nil)

	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	if got.Lookup(ManifestName) != nil {
		t.Error("manifest must not include itself in its own entries")
	}

	// Verify the digest is valid.
	if len(digest) != 64 {
		t.Errorf("WriteManifest digest length = %d, want 64", len(digest))
	}
}

// =============================================================================
// VerifyLaunchArtifacts tests
// =============================================================================

func TestVerifyLaunchArtifacts_Canonical(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	digest := writeTestManifest(t, tmp, nil, nil)

	err := VerifyLaunchArtifacts(tmp, digest)
	if err != nil {
		t.Fatalf("canonical launch artifacts should verify: %v", err)
	}
}

func TestVerifyLaunchArtifacts_EmptyExpectedSHA(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil, nil)

	err := VerifyLaunchArtifacts(tmp, "")
	if err == nil {
		t.Error("expected error for empty expected SHA")
	}
}

func TestVerifyLaunchArtifacts_WrongExpectedSHA(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil, nil)

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
	digest := writeTestManifest(t, tmp, nil, nil)

	// Remove the brief file.
	os.Remove(filepath.Join(tmp, BriefName))

	err := VerifyLaunchArtifacts(tmp, digest)
	if err == nil {
		t.Error("expected error for missing brief file")
	}
}

// =============================================================================
// CheckLegacyBriefMigration tests
// =============================================================================

func TestCheckLegacyBriefMigration_Match(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	policy := LegacyBriefMatchCanonicalV1
	digest := writeTestManifest(t, tmp, nil, &policy)

	// Write legacy .soldier-md with the same content as the brief.
	briefContent, _ := os.ReadFile(filepath.Join(tmp, BriefName))
	os.WriteFile(filepath.Join(tmp, ".soldier-md"), briefContent, 0644)

	manifest, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the manifest first.
	if err := VerifyLaunchArtifacts(tmp, digest); err != nil {
		t.Fatal(err)
	}

	err = CheckLegacyBriefMigration(tmp, manifest)
	if err != nil {
		t.Fatalf("legacy migration should pass: %v", err)
	}
}

func TestCheckLegacyBriefMigration_Mismatch(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	policy := LegacyBriefMatchCanonicalV1
	writeTestManifest(t, tmp, nil, &policy)

	// Write legacy .soldier-md with DIFFERENT content.
	os.WriteFile(filepath.Join(tmp, ".soldier-md"), []byte("different content"), 0644)

	manifest, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	err = CheckLegacyBriefMigration(tmp, manifest)
	if err == nil {
		t.Error("expected error for legacy content mismatch")
	}
	if !strings.Contains(err.Error(), "digest does not match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckLegacyBriefMigration_NoPolicy(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil, nil)

	manifest, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	err = CheckLegacyBriefMigration(tmp, manifest)
	if err == nil {
		t.Error("expected error when no legacy migration policy set")
	}
}

func TestCheckLegacyBriefMigration_MissingBrief(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)

	policy := LegacyBriefMatchCanonicalV1
	writeTestManifest(t, tmp, nil, &policy)

	// Remove the canonical brief.
	os.Remove(filepath.Join(tmp, BriefName))

	manifest, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	err = CheckLegacyBriefMigration(tmp, manifest)
	if err == nil {
		t.Error("expected error when canonical brief is missing")
	}
}
