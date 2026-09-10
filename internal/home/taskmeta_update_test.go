package home

import (
	"crypto/sha256"
	"errors"
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
	err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["attestation_generation"] = "1"
		return nil
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

// TestReadMetaFileToleratesOversizedLine pins the deliberate contract split
// between the two readers: the same file whose oversized line makes ReadMeta
// (the read-modify-write reader) fail closed is read to completion by
// ReadMetaFile (the read-only directory-scan reader), so prune's live-workspace
// sweep never drops a still-referenced meta over a line it could not size.
func TestReadMetaFileToleratesOversizedLine(t *testing.T) {
	homeDir := t.TempDir()
	p := writeUnreadableMeta(t, homeDir, "tolerant-scan")

	if _, err := ReadMeta(homeDir, "tolerant-scan"); err == nil {
		t.Fatal("ReadMeta accepted an oversized line; the fail-closed contract is gone")
	}

	meta, err := ReadMetaFile(p)
	if err != nil {
		t.Fatalf("ReadMetaFile rejected an oversized line: %v", err)
	}
	if meta["project"] != "existing-project" || meta["worktree"] != "/tmp/wt" {
		t.Fatalf("ReadMetaFile lost keys around the oversized line: %v", meta)
	}
}

// TestUpdateMetaAbsentMetaIsFirstWrite proves absence is the empty map, so the
// first projection for a task creates the file.
func TestUpdateMetaAbsentMetaIsFirstWrite(t *testing.T) {
	homeDir := t.TempDir()
	id := "absent-first-write"

	if err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["project"] = "munsu"
		return nil
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
	if err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["attestation_generation"] = "1"
		delete(meta, "worktree")
		return nil
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
			err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
				time.Sleep(2 * time.Millisecond)
				meta[fmt.Sprintf("writer_%02d", i)] = "persisted"
				return nil
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

// TestUpdateMetaUnchangedAbandonsWrite proves ErrMetaUnchanged abandons the
// update without writing and without an error, so a callback that finds
// nothing to do leaves an absent meta absent rather than creating one.
func TestUpdateMetaUnchangedAbandonsWrite(t *testing.T) {
	homeDir := t.TempDir()
	id := "unchanged-abandons"
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}

	if err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["project"] = "munsu"
		return ErrMetaUnchanged
	}); err != nil {
		t.Fatalf("UpdateMeta returned %v for an abandoned update, want nil", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat(%s) = %v, want not-exist: an abandoned update created a meta", p, statErr)
	}
	t.Logf("abandoned update wrote nothing: %s absent", p)
}

// TestUpdateMetaUnchangedLeavesExistingFileByte proves an abandoned update does
// not rewrite an existing meta, so the mutations the callback made before
// abandoning never reach the file.
func TestUpdateMetaUnchangedLeavesExistingFileByte(t *testing.T) {
	homeDir := t.TempDir()
	id := "unchanged-existing"
	if err := WriteMeta(homeDir, id, map[string]string{"project": "munsu"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta before update: %v", err)
	}

	if err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["attestation_generation"] = "1"
		return ErrMetaUnchanged
	}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta back: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("abandoned update changed meta: before=%q after=%q", before, after)
	}
	if strings.Contains(string(after), "attestation_generation") {
		t.Error("abandoned update persisted the key the callback set before abandoning")
	}
}

// TestUpdateMetaCallbackErrorRefusesWrite proves a callback error other than
// ErrMetaUnchanged refuses the write and propagates the reason, so a callback
// that finds a precondition broken cannot have its partial mutation persisted.
func TestUpdateMetaCallbackErrorRefusesWrite(t *testing.T) {
	homeDir := t.TempDir()
	id := "callback-refuses"
	if err := WriteMeta(homeDir, id, map[string]string{"project": "munsu"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	sentinel := errors.New("precondition not met")
	err := UpdateMeta(homeDir, id, func(meta map[string]string) error {
		meta["attestation_generation"] = "1"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("UpdateMeta error = %v, want it to wrap %v", err, sentinel)
	}

	meta, readErr := ReadMeta(homeDir, id)
	if readErr != nil {
		t.Fatalf("ReadMeta: %v", readErr)
	}
	if _, ok := meta["attestation_generation"]; ok {
		t.Error("a refused update persisted the key the callback set before refusing")
	}
	if meta["project"] != "munsu" {
		t.Errorf("project = %q, want %q", meta["project"], "munsu")
	}
}
