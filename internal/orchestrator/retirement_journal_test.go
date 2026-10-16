package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

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
	stem, err := home.DurableKey(id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h, "state", ".backup", stem, stem+".status")
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
	if err := home.AppendStatus(h, id, "working: running [key=build]"); err != nil {
		t.Fatal(err)
	}
	steps, err := FinalizeRetirementJournals(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0] != "closed keyed phase [key=build]" {
		t.Fatalf("steps = %v, want the one closed phase", steps)
	}
	lines, err := home.ReadStatus(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines[len(lines)-1]; got != "resolved: soldier torn down [key=build]" {
		t.Fatalf("last status = %q", got)
	}
}
