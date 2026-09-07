package home

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

	before, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta before update: %v", readErr)
	}
	beforeHash := sha256.Sum256(before)
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
	afterHash := sha256.Sum256(raw)
	t.Logf("unreadable update error: %v; meta SHA-256 before=%x after=%x", err, beforeHash, afterHash)
	if !strings.Contains(string(raw), "project=existing-project") {
		t.Error("pre-existing project key was erased by a refused update")
	}
	if !strings.Contains(string(raw), "worktree=/tmp/wt") {
		t.Error("pre-existing worktree key was erased by a refused update")
	}
	if strings.Contains(string(raw), "attestation_generation") {
		t.Error("refused update wrote its own key anyway")
	}
	if string(before) != string(raw) || beforeHash != afterHash {
		t.Fatalf("refused update changed meta: before=%x after=%x", beforeHash, afterHash)
	}
	t.Logf("attempted projection key absent: attestation_generation")
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
	t.Logf("merged persisted state: project=%q, attestation_generation=%q, worktree absent", meta["project"], meta["attestation_generation"])
}

// TestUpdateMetaSerializesConcurrentUpdates proves the advisory lock covers
// the read, callback, and atomic replacement as one cycle.
func TestUpdateMetaSerializesConcurrentUpdates(t *testing.T) {
	homeDir := t.TempDir()
	id := "concurrent-updates"
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := UpdateMeta(homeDir, id, func(meta map[string]string) {
				time.Sleep(2 * time.Millisecond)
				meta[fmt.Sprintf("writer_%02d", i)] = "persisted"
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent UpdateMeta: %v", err)
	}
	meta, err := ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	persisted := 0
	for i := 0; i < writers; i++ {
		if meta[fmt.Sprintf("writer_%02d", i)] == "persisted" {
			persisted++
		}
	}
	t.Logf("final concurrent writer-key count: %d/%d", persisted, writers)
	if persisted != writers {
		t.Fatalf("persisted writer keys = %d, want %d; meta=%v", persisted, writers, meta)
	}
}
