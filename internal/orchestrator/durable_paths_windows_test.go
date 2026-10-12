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
	stem, err := home.DurableKey(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, id, "done: milestone"); err != nil {
		t.Fatal(err)
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
	old := time.Now().Add(-testSeatLiveness.SilentAfter - time.Hour)
	if err := os.Chtimes(sp, old, old); err != nil {
		t.Fatalf("Chtimes on %q: %v", sp, err)
	}
	if !isPausedBeyondResurface(tmp, id, testSeatLiveness.SilentAfter) {
		t.Fatalf("isPausedBeyondResurface(%q) = false, want true (status at %q older than threshold)", id, sp)
	}
}
