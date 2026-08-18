package home

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRenameDurableMovesAndReplaces exercises the durable rename contract
// behaviorally: the source is gone, the destination holds the content, and an
// existing destination is replaced. On unix the rename is followed by a
// parent-directory fsync; on windows the move is issued with
// MOVEFILE_WRITE_THROUGH. The same test runs on both halves.
func TestRenameDurableMovesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.tmp")
	to := filepath.Join(dir, "to.txt")
	if err := os.WriteFile(from, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	// Pre-existing destination must be replaced, mirroring os.Rename.
	if err := os.WriteFile(to, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := RenameDurable(from, to); err != nil {
		t.Fatalf("RenameDurable: %v", err)
	}
	_, fromErr := os.Stat(from)
	sourceRemoved := os.IsNotExist(fromErr)
	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	t.Logf("observable post-state: source_removed=%v destination_content=%q", sourceRemoved, got)
	if !sourceRemoved {
		t.Fatalf("source should be gone after rename, stat err = %v", fromErr)
	}
	if string(got) != "hello" {
		t.Fatalf("destination content = %q, want %q", got, "hello")
	}
}

// TestRenameDurableMissingDirError asserts the failure mode: renaming into a
// nonexistent directory must surface an error rather than silently succeeding.
func TestRenameDurableMissingDirError(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.tmp")
	if err := os.WriteFile(from, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, "no-such-dir", "to.txt")
	if err := RenameDurable(from, to); err == nil {
		t.Fatal("RenameDurable into a nonexistent directory should error")
	}
	// Source must be left intact when the rename fails.
	if _, err := os.Stat(from); err != nil {
		t.Fatalf("source should survive a failed rename, stat err = %v", err)
	}
}
