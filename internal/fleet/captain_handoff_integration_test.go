//go:build integration

package fleet

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestHandoffTasksAxiFailureIsAtomic(t *testing.T) {
	path, err := exec.LookPath("tasks-axi")
	if err != nil {
		t.Skip("tasks-axi not found")
	}

	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "handoff-sm")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captainHome, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "handoff-sm"); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(parent, "data", "backlog.md")
	destination := filepath.Join(captainHome, "data", "backlog.md")
	runTasksAxi := func(args ...string) {
		t.Helper()
		cmd := exec.Command(path, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tasks-axi %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(source, []byte("# Backlog\n\n## Queued\n\n## Done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTasksAxi("add", "blocker-a", "Blocker", "--file", source)
	runTasksAxi("add", "dependent-b", "Dependent", "--blocked-by", "blocker-a", "--file", source)
	writeIntegrationHandoffAuthority(t, parent, "blocker-a", "Blocker")
	writeIntegrationHandoffAuthority(t, parent, "dependent-b", "Dependent")
	if err := os.WriteFile(destination, []byte("# Backlog\n\n## Queued\n\n## Done\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sourceBefore, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationBefore, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}

	err = Handoff(parent, captainHome, []string{"blocker-a"})
	if err == nil {
		t.Fatal("expected dependency-stranding handoff refusal")
	}

	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationAfter, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceAfter, sourceBefore) {
		t.Fatal("source backlog changed after failed atomic handoff")
	}
	if !bytes.Equal(destinationAfter, destinationBefore) {
		t.Fatal("destination backlog changed after failed atomic handoff")
	}
}

func writeIntegrationHandoffAuthority(t *testing.T, homeDir, taskID, description string) {
	t.Helper()
	if err := mhome.WriteTaskAggregate(homeDir, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: taskID, Generation: "1", Current: true, Owner: "general", Definition: description, State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(homeDir, taskID, map[string]string{"description": description, "kind": "ship", "project": "munsu", "generation": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(homeDir, taskID, "queued: ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(homeDir, taskID)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(homeDir, taskID), []byte("# "+description+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}
