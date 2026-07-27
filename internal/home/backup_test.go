package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBackupAndRestoreSmoke(t *testing.T) {
	homeDir := t.TempDir()
	if err := EnsureDirTree(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("backlog\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "config", "backend"), []byte("tmux\n"), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	manifest, err := CreateBackup(homeDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 6 {
		t.Fatalf("manifest entries = %d, want 6", len(manifest.Entries))
	}
	if err := VerifyBackup(backupDir); err != nil {
		t.Fatalf("VerifyBackup: %v", err)
	}
	if err := RestoreSmoke(backupDir); err != nil {
		t.Fatalf("RestoreSmoke: %v", err)
	}
}

func TestVerifyBackupDetectsTampering(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "task.meta"), []byte("kind=ship\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "backup")
	if _, err := CreateBackup(homeDir, backupDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, BackupPayloadDir, "state", "task.meta"), []byte("tampered\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(backupDir); err == nil {
		t.Fatal("VerifyBackup accepted tampered payload")
	}
}

func TestCreateBackupRejectsDestinationInsideSource(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateBackup(homeDir, filepath.Join(homeDir, "backups", "bad")); err == nil {
		t.Fatal("CreateBackup accepted destination inside source home")
	}
}

func TestCreateBackupPreservesEmptyDirectories(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state", "empty"), 0711); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "backup")
	manifest, err := CreateBackup(homeDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range manifest.Entries {
		if entry.Path == "state/empty" && entry.Type == "directory" {
			found = true
		}
	}
	if !found {
		t.Fatal("empty directory missing from manifest")
	}
	if err := RestoreSmoke(backupDir); err != nil {
		t.Fatal(err)
	}
}

func TestCreateBackupRejectsExistingDestination(t *testing.T) {
	homeDir := t.TempDir()
	backupDir := t.TempDir()
	if _, err := CreateBackup(homeDir, backupDir); err == nil {
		t.Fatal("CreateBackup accepted existing destination")
	}
}
