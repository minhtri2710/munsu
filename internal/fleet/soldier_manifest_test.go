//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifest_WriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	entries := []ManifestEntry{
		{Path: ".soldier-brief.md", SHA256: "abc123", Policy: DisposalPolicyCleanable},
		{Path: ".soldier-charter.md", SHA256: "def456", Policy: DisposalPolicyCleanable},
	}
	manifest := BuildManifest(entries)
	if err := WriteManifest(tmp, manifest); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got.ManifestVersion != ManifestVersion {
		t.Errorf("ManifestVersion = %q, want %q", got.ManifestVersion, ManifestVersion)
	}
	if len(got.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(got.Artifacts))
	}
	if got.Artifacts[0].Path != ".soldier-brief.md" {
		t.Errorf("artifact[0].Path = %q, want %q", got.Artifacts[0].Path, ".soldier-brief.md")
	}
}

func TestManifest_Lookup(t *testing.T) {
	manifest := BuildManifest([]ManifestEntry{
		{Path: ".soldier-brief.md", SHA256: "abc", Policy: DisposalPolicyCleanable},
		{Path: ".soldier-charter.md", SHA256: "def", Policy: DisposalPolicyCleanable},
	})
	entry := manifest.Lookup(".soldier-brief.md")
	if entry == nil {
		t.Fatal("expected to find .soldier-brief.md")
	}
	if entry.SHA256 != "abc" {
		t.Errorf("SHA256 = %q, want %q", entry.SHA256, "abc")
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
		{Path: ".soldier-brief.md", SHA256: "a", Policy: DisposalPolicyCleanable},
		{Path: ".soldier-charter.md", SHA256: "b", Policy: DisposalPolicyCleanable},
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
	err := WriteManifest(t.TempDir(), nil)
	if err == nil {
		t.Error("expected error writing nil manifest")
	}
}

func TestManifest_IntegrityCheck(t *testing.T) {
	tmp := t.TempDir()

	// Write actual launch files.
	charter := DefaultCharter("manifest-test", "ship", "direct-PR")
	brief := []byte("# Task brief\n\nVerify manifest integrity.\n")
	prompt := "complete prompt text"
	launchScript := "#!/usr/bin/env bash\necho hi\n"

	os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(tmp, BriefName), brief, 0644)
	os.WriteFile(filepath.Join(tmp, PromptName), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(tmp, LaunchScriptName), []byte(launchScript), 0644)

	// Build manifest entries computing actual digests.
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}

	manifest := BuildManifest(entries)
	if err := WriteManifest(tmp, manifest); err != nil {
		t.Fatal(err)
	}

	// Verify the manifest file exists and is readable.
	manifestPath := filepath.Join(tmp, ManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}

	// Read back and verify entries.
	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 4 {
		t.Fatalf("expected 4 artifacts, got %d", len(got.Artifacts))
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
}

// TestManifest_NotInItsOwnEntries verifies the manifest does not include itself.
func TestManifest_NotInItsOwnEntries(t *testing.T) {
	tmp := t.TempDir()

	charter := DefaultCharter("self-test", "ship", "direct-PR")
	brief := []byte("brief")
	os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(tmp, BriefName), brief, 0644)

	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName} {
		entry, err := ManifestEntryForFile(tmp, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}

	manifest := BuildManifest(entries)
	if err := WriteManifest(tmp, manifest); err != nil {
		t.Fatal(err)
	}

	got, err := ReadManifest(tmp)
	if err != nil {
		t.Fatal(err)
	}

	if got.Lookup(ManifestName) != nil {
		t.Error("manifest must not include itself in its own entries")
	}
}

// TestManifest_EmptyVersion fails on empty manifest version.
func TestManifest_EmptyVersion(t *testing.T) {
	tmp := t.TempDir()
	manifestPath := filepath.Join(tmp, ManifestName)
	// Write an invalid manifest with empty version.
	content := `{"manifest_version":"","artifacts":[]}`
	os.WriteFile(manifestPath, []byte(content), 0644)
	_, err := ReadManifest(tmp)
	if err == nil {
		t.Error("expected error for empty manifest version")
	}
	if !strings.Contains(err.Error(), "version is empty") {
		t.Errorf("unexpected error: %v", err)
	}
}