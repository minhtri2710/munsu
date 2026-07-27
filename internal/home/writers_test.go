package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyWriterPreflightEmptyHomeStillRequiresProcessProof(t *testing.T) {
	inventory, err := (LegacyWriterPreflight{}).Inspect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.VerifiedQuiescent || len(inventory.Writers) != 0 {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestLegacyWriterPreflightRejectsWatcherIdentity(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".watcher-identity"), []byte("watcher evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := (LegacyWriterPreflight{}).Inspect(homeDir)
	if err == nil || inventory.VerifiedQuiescent {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
}

func TestLegacyWriterPreflightRejectsTaskEndpoint(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "task-one.meta"), []byte("kind=ship\nwindow=@1\nbackend=tmux\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := (LegacyWriterPreflight{}).Inspect(homeDir)
	if err == nil || inventory.VerifiedQuiescent || len(inventory.Writers) != 1 {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
}

func TestLegacyWriterPreflightRejectsAFKAndSessionLocks(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".lock", ".watch.lock", ".afk.lock"} {
		if err := os.WriteFile(filepath.Join(homeDir, "state", name), []byte("123\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := (LegacyWriterPreflight{}).Inspect(homeDir)
	if err == nil || inventory.VerifiedQuiescent || len(inventory.Writers) != 3 {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
}
