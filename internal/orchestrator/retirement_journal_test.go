package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestVerifyRetirementContinuityBlocksOpenMaterialReport(t *testing.T) {
	h := t.TempDir()
	id := "task"
	if err := home.AppendStatus(h, id, "done [key=terminal]: complete"); err != nil {
		t.Fatal(err)
	}
	if err := InitTaskObligations(h, id, "terminal"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetirementContinuity(h, id); err == nil {
		t.Fatal("expected open report relay to block retirement")
	}
}

func TestPrepareForcedRetirementEvidenceCopiesStatus(t *testing.T) {
	h := t.TempDir()
	id := "task"
	if err := home.AppendStatus(h, id, "done: complete"); err != nil {
		t.Fatal(err)
	}
	steps, err := PrepareForcedRetirementEvidence(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "evidence preserved") {
		t.Fatalf("steps = %v", steps)
	}
	path := filepath.Join(h, "state", ".backup", id, id+".status")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "done: complete\n" {
		t.Fatalf("backup = %q", data)
	}
}

func TestFinalizeRetirementJournalsClosesActivities(t *testing.T) {
	h := t.TempDir()
	id := "task"
	if err := home.AppendStatus(h, id, "working [key=build]: running"); err != nil {
		t.Fatal(err)
	}
	steps, err := FinalizeRetirementJournals(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 2 {
		t.Fatalf("steps = %v", steps)
	}
	lines, err := home.ReadStatus(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines[len(lines)-1]; !strings.Contains(got, "resolved [key=build]") {
		t.Fatalf("last status = %q", got)
	}
}
