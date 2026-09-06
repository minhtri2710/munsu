package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUnreadableMeta writes a .meta file whose first lines are valid keys and
// whose last line exceeds bufio.Scanner's token limit, so ReadMeta fails for a
// reason that is not absence while the file stays a readable regular file an
// atomic rename can replace.
func writeUnreadableMeta(t *testing.T, homeDir, id string) string {
	t.Helper()
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatalf("creating state directory: %v", err)
	}
	body := "project=existing-project\nworktree=/tmp/wt\n" + strings.Repeat("x", 128*1024) + "\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatalf("writing unreadable meta: %v", err)
	}
	return p
}

// TestUpdateMetaRefusesUnreadableMeta proves an unreadable existing meta refuses
// the write instead of replacing the file with the mutation's keys alone.
func TestUpdateMetaRefusesUnreadableMeta(t *testing.T) {
	homeDir := t.TempDir()
	id := "refuse-unreadable"
	p := writeUnreadableMeta(t, homeDir, id)

	err := UpdateMeta(homeDir, id, func(meta map[string]string) {
		meta["attestation_generation"] = "1"
	})
	if err == nil {
		t.Fatal("UpdateMeta returned nil for an unreadable meta")
	}

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta back: %v", readErr)
	}
	if !strings.Contains(string(raw), "project=existing-project") {
		t.Error("pre-existing project key was erased by a refused update")
	}
	if !strings.Contains(string(raw), "worktree=/tmp/wt") {
		t.Error("pre-existing worktree key was erased by a refused update")
	}
	if strings.Contains(string(raw), "attestation_generation") {
		t.Error("refused update wrote its own key anyway")
	}
}

// TestUpdateMetaAbsentMetaIsFirstWrite proves absence is the empty map, so the
// first projection for a task creates the file.
func TestUpdateMetaAbsentMetaIsFirstWrite(t *testing.T) {
	homeDir := t.TempDir()
	id := "absent-first-write"

	if err := UpdateMeta(homeDir, id, func(meta map[string]string) {
		meta["project"] = "munsu"
	}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	meta, err := ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["project"] != "munsu" {
		t.Errorf("project = %q, want %q", meta["project"], "munsu")
	}
}

// TestUpdateMetaPreservesUnmutatedKeys proves the update merges onto the current
// file rather than replacing it with the mutation's keys.
func TestUpdateMetaPreservesUnmutatedKeys(t *testing.T) {
	homeDir := t.TempDir()
	id := "merge-onto-existing"

	if err := WriteMeta(homeDir, id, map[string]string{
		"project":  "munsu",
		"worktree": "/tmp/wt",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := UpdateMeta(homeDir, id, func(meta map[string]string) {
		meta["attestation_generation"] = "1"
		delete(meta, "worktree")
	}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	meta, err := ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["project"] != "munsu" {
		t.Errorf("project = %q, want %q", meta["project"], "munsu")
	}
	if meta["attestation_generation"] != "1" {
		t.Errorf("attestation_generation = %q, want %q", meta["attestation_generation"], "1")
	}
	if _, ok := meta["worktree"]; ok {
		t.Error("a key the mutation deleted survived the update")
	}
}
