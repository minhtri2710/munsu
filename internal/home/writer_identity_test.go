package home

import (
	"os"
	"path/filepath"
	"testing"
)

func testWriterIdentity(t *testing.T, homeDir string) WriterIdentity {
	t.Helper()
	canonical, err := CanonicalPath(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return WriterIdentity{SchemaVersion: 1, Kind: "watcher", PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical, Endpoint: "pane-1", SessionOwner: "session-1"}
}

func TestWriterIdentityRoundTripAndPrivatePermissions(t *testing.T) {
	h := t.TempDir()
	id := testWriterIdentity(t, h)
	if err := PublishWriterIdentity(h, "watcher", id); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWriterIdentity(h, "watcher")
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got=%+v want=%+v", got, id)
	}
	info, err := os.Stat(WriterIdentityPath(h, "watcher"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(WriterIdentityPath(h, "watcher")))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0700 {
		t.Fatalf("dir mode=%o", dir.Mode().Perm())
	}
}
func TestPublishWriterIdentityRejectsInvalidIdentity(t *testing.T) {
	h := t.TempDir()
	id := testWriterIdentity(t, h)
	id.StartToken = ""
	if err := PublishWriterIdentity(h, "watcher", id); err == nil {
		t.Fatal("expected error")
	}
}
func TestRemoveWriterIdentityIfMatchesPreservesNewGeneration(t *testing.T) {
	h := t.TempDir()
	old := testWriterIdentity(t, h)
	if err := PublishWriterIdentity(h, "watcher", old); err != nil {
		t.Fatal(err)
	}
	current := old
	current.StartToken = "124"
	if err := PublishWriterIdentity(h, "watcher", current); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveWriterIdentityIfMatches(h, "watcher", old)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("removed new generation")
	}
	got, err := ReadWriterIdentity(h, "watcher")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartToken != "124" {
		t.Fatalf("got=%+v", got)
	}
}
func TestRemoveWriterIdentityIfMatchesRemovesExactGeneration(t *testing.T) {
	h := t.TempDir()
	id := testWriterIdentity(t, h)
	if err := PublishWriterIdentity(h, "watcher", id); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveWriterIdentityIfMatches(h, "watcher", id)
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(WriterIdentityPath(h, "watcher")); !os.IsNotExist(err) {
		t.Fatalf("stat err=%v", err)
	}
}
func TestWriterIdentityRejectsUnsafeKind(t *testing.T) {
	h := t.TempDir()
	id := testWriterIdentity(t, h)
	if err := PublishWriterIdentity(h, "../watcher", id); err == nil {
		t.Fatal("expected error")
	}
}
