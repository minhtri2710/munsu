package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDigesterFlushAccumulatesAcrossWindowsAndDrains(t *testing.T) {
	home := t.TempDir()
	d := NewDigester(home)
	firstFlush := time.Now().Add(defaultWindow + time.Second)

	d.Feed(&Digest{Routines: []WakeDigest{{Kind: "status", Key: "first", Payload: "first window"}}})
	if err := d.Flush(firstFlush); err != nil {
		t.Fatalf("first Flush: %v", err)
	}

	d.Feed(&Digest{Escalated: []WakeDigest{{Kind: "signal", Key: "second", Payload: "PR merged"}}})
	if err := d.Flush(firstFlush.Add(defaultWindow + time.Second)); err != nil {
		t.Fatalf("second Flush: %v", err)
	}

	be, err := drainDigest(home)
	if err != nil {
		t.Fatalf("drainDigest: %v", err)
	}
	if be == nil {
		t.Fatal("drainDigest returned nil after two successful flushes")
	}
	if len(be.Entries) != 2 {
		t.Fatalf("drained entries = %d, want 2: %+v", len(be.Entries), be.Entries)
	}
	if be.Entries[0].Key != "first" || be.Entries[1].Key != "second" {
		t.Fatalf("drained keys = %q, %q; want first, second", be.Entries[0].Key, be.Entries[1].Key)
	}
	if be.RoutineCount != 1 || be.EscalatedCount != 1 {
		t.Fatalf("drained counts = routine %d, escalated %d; want 1, 1", be.RoutineCount, be.EscalatedCount)
	}
	if _, err := os.Stat(filepath.Join(home, digestFile)); !os.IsNotExist(err) {
		t.Fatalf("digest file remains after drain: %v", err)
	}
}

func TestDigesterFlushRejectsCorruptExistingDigestAndRetainsEntries(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	digestPath := filepath.Join(stateDir, ".afk-digest")
	corrupt := []byte("not json")
	if err := os.WriteFile(digestPath, corrupt, 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDigester(home)
	d.Feed(&Digest{Routines: []WakeDigest{{Kind: "status", Key: "retained", Payload: "keep me"}}})
	if err := d.Flush(time.Now().Add(defaultWindow + time.Second)); err == nil {
		t.Fatal("Flush with corrupt existing digest succeeded")
	}

	data, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("read corrupt digest after rejected flush: %v", err)
	}
	if string(data) != string(corrupt) {
		t.Fatalf("corrupt digest was overwritten: got %q, want %q", data, corrupt)
	}

	if err := os.Remove(digestPath); err != nil {
		t.Fatal(err)
	}
	if err := d.Flush(time.Now().Add(2*defaultWindow + time.Second)); err != nil {
		t.Fatalf("Flush after corrupt digest removal: %v", err)
	}
	data, err = os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("read retained digest: %v", err)
	}
	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		t.Fatalf("unmarshal retained digest: %v", err)
	}
	if len(be.Entries) != 1 || be.Entries[0].Key != "retained" {
		t.Fatalf("retained entries = %+v, want one entry with key retained", be.Entries)
	}
}

func TestDigesterFlushRetainsEntriesAfterPersistenceFailure(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, "state")
	if err := os.WriteFile(statePath, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}

	d := NewDigester(home)
	d.Feed(&Digest{Routines: []WakeDigest{{Kind: "status", Key: "retained", Payload: "keep me"}}})
	if err := d.Flush(time.Now().Add(defaultWindow + time.Second)); err == nil {
		t.Fatal("Flush with unusable state path succeeded")
	}

	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := d.Flush(time.Now().Add(2*defaultWindow + time.Second)); err != nil {
		t.Fatalf("Flush after persistence failure: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, digestFile))
	if err != nil {
		t.Fatalf("read retained digest: %v", err)
	}
	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		t.Fatalf("unmarshal retained digest: %v", err)
	}
	if len(be.Entries) != 1 || be.Entries[0].Key != "retained" {
		t.Fatalf("retained entries = %+v, want one entry with key retained", be.Entries)
	}
}
