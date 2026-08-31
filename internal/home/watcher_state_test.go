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

func TestDrainWakesAbsentQueueReturnsEmpty(t *testing.T) {
	records, err := DrainWakes(t.TempDir())
	if err != nil {
		t.Fatalf("DrainWakes absent queue: %v", err)
	}
	if records != nil {
		t.Fatalf("DrainWakes absent queue records = %v, want nil", records)
	}
}
