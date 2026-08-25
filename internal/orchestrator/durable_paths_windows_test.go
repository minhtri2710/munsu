//go:build windows

package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
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
	obligationsPath := filepath.Join(tmp, "state", ".obligations", stem+".obligations")
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
	wantMarker := stem + "." + termKey + activationSeenSuffix
	if filepath.Base(markerPath) != wantMarker {
		t.Fatalf("activation marker path = %q; want basename %q", markerPath, wantMarker)
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

type recordingActivationTransport struct {
	payloads []string
}

func (r *recordingActivationTransport) Attempt(_ string, _ TargetResult, payload string) ActivationAttempt {
	r.payloads = append(r.payloads, payload)
	return ActivationAttempt{Acknowledged: true, SubmitStatus: "submitted"}
}

func TestActivateOnReceiptDecodesDurableTaskStem(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parentHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, ProvenanceMarkerName), []byte("munsu-v2\napi\n"+captainHome+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	taskID, termKey := "captain:api", "terminal-1"
	metaPath, err := home.MetaFilePath(parentHome, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, []byte("kind=captain\nherdr_pane_id=p1\nherdr_session=w1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(captainHome, taskID, termKey, "done", "complete"); err != nil {
		t.Fatal(err)
	}

	transport := &recordingActivationTransport{}
	if got := ActivateOnReceiptWithTransport(captainHome, parentHome, transport); got != 1 {
		t.Fatalf("activation count = %d, want 1", got)
	}
	if len(transport.payloads) != 1 || !strings.Contains(transport.payloads[0], "soldier "+taskID) {
		t.Fatalf("activation payloads = %q, want logical task id", transport.payloads)
	}
	if !IsActivationSeen(captainHome, taskID, termKey) {
		t.Fatal("logical receipt was not marked activation-seen")
	}
	if got := ActivateOnReceiptWithTransport(captainHome, parentHome, transport); got != 0 {
		t.Fatalf("second activation count = %d, want 0", got)
	}
	if len(transport.payloads) != 1 {
		t.Fatalf("second activation duplicated payload: %q", transport.payloads)
	}
}

func TestListAllReceiptsRejectsMalformedDurableStem(t *testing.T) {
	tmp := t.TempDir()
	receiptDir := ReceiptDir(tmp)
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptDir, "captain%ZZ.terminal.receipt"), []byte("state=done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := listAllReceipts(tmp); err == nil {
		t.Fatal("listAllReceipts accepted malformed durable stem")
	}
}

func TestDiscoverPerTaskChecksDecodesDurableTaskStem(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	taskID := "captain:api"
	checkPath, err := home.DurableFilePath(stateDir, taskID, ".check")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}

	plugins, err := DiscoverPerTaskChecks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Label != taskID || plugins[0].Path != checkPath {
		t.Fatalf("plugins = %+v, want logical label and durable path %q", plugins, checkPath)
	}
}

func TestDiscoverPerTaskChecksRejectsMalformedDurableStem(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "captain%ZZ.check"), []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverPerTaskChecks(tmp); err == nil {
		t.Fatal("DiscoverPerTaskChecks accepted malformed durable stem")
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
