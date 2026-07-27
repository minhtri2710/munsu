package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type testWriterFence struct {
	inventory WriterInventory
	err       error
	called    bool
}

func (f *testWriterFence) FenceWriters(string) (WriterInventory, error) {
	f.called = true
	return f.inventory, f.err
}

func TestMigrateAndActivatePublishesActivationLast(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "task.meta"), []byte("kind=ship\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fence := &testWriterFence{inventory: WriterInventory{VerifiedQuiescent: true}}
	result, err := MigrateAndActivate(MigrationRequest{HomeDir: homeDir, BackupDir: filepath.Join(t.TempDir(), "backup"), Generation: "gen-one", BuildIdentity: "build-one", WriterFence: fence})
	if err != nil {
		t.Fatal(err)
	}
	if !fence.called || result.Activation.Generation != "gen-one" {
		t.Fatalf("result=%+v fence=%+v", result, fence)
	}
	if _, err := ResolveActiveRoot(homeDir); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAndActivateDoesNotActivateWhenFenceFails(t *testing.T) {
	homeDir := t.TempDir()
	fence := &testWriterFence{err: errors.New("writer still alive")}
	_, err := MigrateAndActivate(MigrationRequest{HomeDir: homeDir, BackupDir: filepath.Join(t.TempDir(), "backup"), Generation: "gen-one", BuildIdentity: "build-one", WriterFence: fence})
	if err == nil {
		t.Fatal("migration succeeded with failed writer fence")
	}
	if _, err := ReadActivation(homeDir); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("activation was published after fence failure: %v", err)
	}
}

func TestMigrateAndActivateRequiresVerifiedQuiescence(t *testing.T) {
	homeDir := t.TempDir()
	fence := &testWriterFence{inventory: WriterInventory{VerifiedQuiescent: false}}
	_, err := MigrateAndActivate(MigrationRequest{HomeDir: homeDir, BackupDir: filepath.Join(t.TempDir(), "backup"), Generation: "gen-one", BuildIdentity: "build-one", WriterFence: fence})
	if err == nil {
		t.Fatal("migration accepted unverified writer inventory")
	}
	if _, err := ReadActivation(homeDir); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("activation was published without quiescence: %v", err)
	}
}
