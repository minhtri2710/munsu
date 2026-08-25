//go:build windows

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestPrepareForcedRetirementEvidenceEncodedID(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	id := "captain:failed"
	termKey := "terminal-1"
	if err := home.AppendStatus(tmp, id, "done: milestone"); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(tmp, id, termKey, "done", "milestone"); err != nil {
		t.Fatal(err)
	}
	entries, err := PrepareForcedRetirementEvidence(tmp, id)
	if err != nil {
		t.Fatalf("PrepareForcedRetirementEvidence: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no evidence-preserved entry returned")
	}
	stem, err := home.DurableKey(id)
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(tmp, "state", ".backup", stem)
	statusBackup := filepath.Join(backupDir, stem+".status")
	data, err := os.ReadFile(statusBackup)
	if err != nil {
		t.Fatalf("evidence backup %q missing for encoded id: %v", statusBackup, err)
	}
	if string(data) != "done: milestone\n" {
		t.Fatalf("evidence backup %q = %q, want %q", statusBackup, data, "done: milestone\n")
	}
	receiptPath, err := ReceiptPath(tmp, id, termKey)
	if err != nil {
		t.Fatal(err)
	}
	receiptBackup := filepath.Join(backupDir, filepath.Base(receiptPath))
	if _, err := os.Stat(receiptBackup); err != nil {
		t.Fatalf("receipt backup %q missing for encoded id: %v", receiptBackup, err)
	}
}

func TestPausedResurfaceEncodedID(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	id := "rel.1"
	if err := home.AppendStatus(tmp, id, "paused: waiting on external"); err != nil {
		t.Fatal(err)
	}
	sp, err := home.StatusFilePath(tmp, id)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-pauseResurfaceThreshold - time.Hour)
	if err := os.Chtimes(sp, old, old); err != nil {
		t.Fatalf("Chtimes on %q: %v", sp, err)
	}
	if !isPausedBeyondResurface(tmp, id) {
		t.Fatalf("isPausedBeyondResurface(%q) = false, want true (status at %q older than threshold)", id, sp)
	}
}
