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
	stem, err := home.DurableKey(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, id, "done: milestone"); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(tmp, id, termKey, "done", "milestone"); err != nil {
		t.Fatal(err)
	}
	if err := InitTaskObligations(tmp, id, termKey); err != nil {
		t.Fatal(err)
	}
	obligationsPath, err := home.DurableFilePath(filepath.Join(tmp, "state", ".obligations"), id, ".obligations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obligationsPath); err != nil {
		t.Fatalf("obligations path %q missing for encoded id: %v", obligationsPath, err)
	}
	receiptPath, err := ReceiptPath(tmp, id, termKey)
	if err != nil {
		t.Fatal(err)
	}
	ackPath, err := AckPath(tmp, id, termKey)
	if err != nil {
		t.Fatal(err)
	}
	wantReceipt := stem + "." + termKey + ".receipt"
	wantAck := stem + "." + termKey + ".ack"
	if filepath.Base(receiptPath) != wantReceipt || filepath.Base(ackPath) != wantAck {
		t.Fatalf("receipt/ack paths = %q, %q; want basenames %q, %q", receiptPath, ackPath, wantReceipt, wantAck)
	}
	pending, err := ListPendingReceipts(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].TaskID != id || pending[0].TermKey != termKey {
		t.Fatalf("pending receipts before ack = %+v, want logical task %q and key %q", pending, id, termKey)
	}
	if err := WriteAck(tmp, id, termKey); err != nil {
		t.Fatal(err)
	}
	pending, err = ListPendingReceipts(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending receipts after ack = %+v, want none", pending)
	}
	if err := MarkActivationSeen(tmp, id, termKey); err != nil {
		t.Fatal(err)
	}
	markerPath, err := ActivationSeenPath(tmp, id, termKey)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(markerPath) != "captain%3Afailed.terminal-1.activation-seen" {
		t.Fatalf("activation marker path = %q", markerPath)
	}
	entries, err := PrepareForcedRetirementEvidence(tmp, id)
	if err != nil {
		t.Fatalf("PrepareForcedRetirementEvidence: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no evidence-preserved entry returned")
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
