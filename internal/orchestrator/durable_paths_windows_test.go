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
	id := "rel.1"
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
	backup := filepath.Join(tmp, "state", ".backup", id, id+".status")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("evidence backup %q missing for encoded id: %v", backup, err)
	}
	if string(data) != "done: milestone\n" {
		t.Fatalf("evidence backup %q = %q, want %q", backup, data, "done: milestone\n")
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
