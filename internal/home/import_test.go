package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func importRequestWithBackup(t *testing.T, homeDir, generation, build string) ImportRequest {
	t.Helper()
	backupDir := filepath.Join(t.TempDir(), "backup")
	manifest, err := CreateBackup(homeDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	return ImportRequest{HomeDir: homeDir, Generation: generation, BuildIdentity: build, BackupDir: backupDir, BackupManifestSHA256: manifest.ManifestSHA256}
}

func TestImportLegacyCreatesFreshGenerationWithoutActivation(t *testing.T) {
	homeDir := t.TempDir()
	if err := EnsureDirTree(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "task.meta"), []byte("kind=ship\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportLegacy(importRequestWithBackup(t, homeDir, "gen-one", "build-one"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedFiles() != 1 || len(result.Quarantined) != 0 {
		t.Fatalf("result = %+v", result)
	}
	root, _ := GenerationRoot(homeDir, "gen-one")
	data, err := os.ReadFile(filepath.Join(root, "state", "task.meta"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "kind=ship\n" {
		t.Fatalf("imported data = %q", data)
	}
	if _, err := ReadActivation(homeDir); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("import must not activate: %v", err)
	}
}

func TestImportLegacyIsIdempotentForSameGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req := importRequestWithBackup(t, homeDir, "gen-one", "build-one")
	first, err := ImportLegacy(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportLegacy(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ManifestSHA256 != second.ManifestSHA256 || !second.AlreadyImported {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestImportLegacyRejectsAfterActivation(t *testing.T) {
	homeDir := t.TempDir()
	root, _ := GenerationRoot(homeDir, "active")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PublishActivation(homeDir, ActivationRecord{SchemaVersion: 1, Generation: "active", BuildIdentity: "build"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLegacy(importRequestWithBackup(t, homeDir, "next", "next")); err == nil {
		t.Fatal("ImportLegacy succeeded after activation")
	}
}

func TestImportLegacyRejectsSourceChangeAfterBackup(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homeDir, "state", "task.meta")
	if err := os.WriteFile(path, []byte("kind=ship\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := importRequestWithBackup(t, homeDir, "gen-one", "build")
	if err := os.WriteFile(path, []byte("kind=scout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLegacy(request); err == nil {
		t.Fatal("ImportLegacy accepted source changed after backup")
	}
}

func TestImportLegacyRejectsUnexpectedTargetEntryOnRerun(t *testing.T) {
	homeDir := t.TempDir()
	request := importRequestWithBackup(t, homeDir, "gen-one", "one")
	if _, err := ImportLegacy(request); err != nil {
		t.Fatal(err)
	}
	root, _ := GenerationRoot(homeDir, "gen-one")
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLegacy(request); err == nil {
		t.Fatal("ImportLegacy accepted unexpected generation entry")
	}
}

func TestImportLegacyRejectsCorruptManifestOnRerun(t *testing.T) {
	homeDir := t.TempDir()
	request := importRequestWithBackup(t, homeDir, "gen-one", "one")
	if _, err := ImportLegacy(request); err != nil {
		t.Fatal(err)
	}
	root, _ := GenerationRoot(homeDir, "gen-one")
	if err := os.WriteFile(filepath.Join(root, ImportManifestName), []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLegacy(request); err == nil {
		t.Fatal("ImportLegacy accepted corrupt manifest")
	}
}

func TestImportLegacyRejectsDifferentRequestForExistingGeneration(t *testing.T) {
	homeDir := t.TempDir()
	first := importRequestWithBackup(t, homeDir, "gen-one", "one")
	if _, err := ImportLegacy(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.BuildIdentity = "two"
	if _, err := ImportLegacy(second); err == nil {
		t.Fatal("ImportLegacy accepted a different build identity for existing generation")
	}
}
