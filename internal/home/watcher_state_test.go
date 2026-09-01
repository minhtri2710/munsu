package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDrainWakesRemovalFailureRetainsQueueForRetry(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	queuePath := WakeQueuePath(home)
	original := []byte("100\tseq-1\tsignal\ttask-1\tpayload-1\n200\tseq-2\tsignal\ttask-2\tpayload-2\n")
	if err := os.WriteFile(queuePath, original, 0600); err != nil {
		t.Fatal(err)
	}

	removeErr := errors.New("remove wake queue: injected failure")
	originalRemove := removeWakeQueueFile
	removeWakeQueueFile = func(string) error { return removeErr }
	t.Cleanup(func() { removeWakeQueueFile = originalRemove })

	records, err := DrainWakes(home)
	if !errors.Is(err, removeErr) {
		t.Fatalf("DrainWakes removal failure = %v, want %v", err, removeErr)
	}
	if records != nil {
		t.Fatalf("DrainWakes records on removal failure = %v, want nil", records)
	}
	if got, err := os.ReadFile(queuePath); err != nil {
		t.Fatalf("read queue after failed removal: %v", err)
	} else if string(got) != string(original) {
		t.Fatalf("queue after failed removal = %q, want original %q", got, original)
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".wake-mutation.json")); !os.IsNotExist(err) {
		t.Fatalf("wake mutation journal after failed removal: %v, want absent", err)
	}

	removeWakeQueueFile = os.Remove
	records, err = DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes retry: %v", err)
	}
	if len(records) != 2 || records[0].Key != "task-1" || records[1].Key != "task-2" {
		t.Fatalf("DrainWakes retry records = %v, want both original records", records)
	}
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Fatalf("queue after successful retry: %v, want absent", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".wake-mutation.json")); !os.IsNotExist(err) {
		t.Fatalf("wake mutation journal after successful drain: %v, want absent", err)
	}
}

func TestDrainWakesReleaseErrorKeepsDrainedRecords(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	queuePath := WakeQueuePath(home)
	original := []byte("100\tseq-1\tsignal\ttask-1\tpayload-1\n200\tseq-2\tsignal\ttask-2\tpayload-2\n")
	if err := os.WriteFile(queuePath, original, 0600); err != nil {
		t.Fatal(err)
	}

	releaseErr := errors.New("release wake lock: injected failure")
	originalRelease := releaseWakeLock
	releaseWakeLock = func(*os.File) error { return releaseErr }
	t.Cleanup(func() { releaseWakeLock = originalRelease })

	records, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes with release error = %v, want nil", err)
	}
	if len(records) != 2 || records[0].Key != "task-1" || records[1].Key != "task-2" {
		t.Fatalf("DrainWakes records with release error = %v, want both original records", records)
	}
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Fatalf("queue after drain with release error: %v, want absent", err)
	}
}

func TestDrainWakesAbsentQueueReturnsEmpty(t *testing.T) {
	records, err := DrainWakes(t.TempDir())
	if err != nil {
		t.Fatalf("DrainWakes absent queue: %v", err)
	}
	if records != nil {
		t.Fatalf("DrainWakes absent queue records = %v, want nil", records)
	}
}

func writeWakeQueueForTest(t *testing.T, home string, lines string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte(lines), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDrainWakesOfKindTakesOnlyThatKindAndKeepsOrder(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueForTest(t, home, "100\t1\tsignal\ta\tpa\n200\t2\t"+ProcessEventWakeKind+"\tev-1\tpe1\n300\t3\tcheck\tb\tpb\n400\t4\t"+ProcessEventWakeKind+"\tev-2\tpe2\n")

	got, err := DrainWakesOfKind(home, ProcessEventWakeKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "ev-1" || got[1].Key != "ev-2" {
		t.Fatalf("drained = %#v, want the two process-event wakes in order", got)
	}
	rest, err := readWakeQueue(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 || rest[0].Key != "a" || rest[0].Kind != "signal" || rest[1].Key != "b" || rest[1].Kind != "check" {
		t.Fatalf("remaining queue = %#v, want signal a then check b", rest)
	}

	again, err := DrainWakesOfKind(home, ProcessEventWakeKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second drain re-delivered %#v", again)
	}
	if rest, err := readWakeQueue(home); err != nil || len(rest) != 2 {
		t.Fatalf("no-op drain changed the queue: %#v, %v", rest, err)
	}
}

func TestDrainWakesExcludingKindLeavesThatKindQueued(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueForTest(t, home, "100\t1\tsignal\ta\tpa\n200\t2\t"+ProcessEventWakeKind+"\tev-1\tpe1\n300\t3\tcheck\tb\tpb\n")

	got, err := DrainWakesExcludingKind(home, ProcessEventWakeKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("drained = %#v, want signal a and check b", got)
	}
	rest, err := readWakeQueue(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Kind != ProcessEventWakeKind || rest[0].Key != "ev-1" {
		t.Fatalf("remaining queue = %#v, want only the process-event wake", rest)
	}
}

func TestDrainWakesOfKindRemovesQueueFileWhenNothingRemains(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueForTest(t, home, "200\t2\t"+ProcessEventWakeKind+"\tev-1\tpe1\n")
	if got, err := DrainWakesOfKind(home, ProcessEventWakeKind); err != nil || len(got) != 1 {
		t.Fatalf("drain = %#v, %v", got, err)
	}
	if _, err := os.Stat(WakeQueuePath(home)); !os.IsNotExist(err) {
		t.Fatalf("queue file after draining the last record: %v, want absent", err)
	}
	if got, err := DrainWakesOfKind(home, ProcessEventWakeKind); err != nil || got != nil {
		t.Fatalf("drain on absent queue = %#v, %v; want nil, nil", got, err)
	}
}

func TestHasQueuedWakesIgnoresProcessEventWakes(t *testing.T) {
	home := t.TempDir()
	if err := EnqueueWake(home, ProcessEventWakeKind, "ev-1", "pe1"); err != nil {
		t.Fatal(err)
	}
	if HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes true with only a process-event wake queued")
	}
	if err := EnqueueWake(home, "signal", "task-1", "done"); err != nil {
		t.Fatal(err)
	}
	if !HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes false with a signal wake queued behind a process-event wake")
	}
}

func TestHasQueuedWakesFailsClosedOnUnreadableQueue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(WakeQueuePath(home), 0755); err != nil {
		t.Fatal(err)
	}
	if !HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes false when the queue path is a directory")
	}
}
