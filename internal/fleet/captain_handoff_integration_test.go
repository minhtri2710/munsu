//go:build integration

package fleet

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tauth "github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestHandoffOwnershipConflictLeavesBacklogUnchanged proves a handoff that
// fails during key resolution (the same task is owned by both the source and
// a captain under its captains/ tree, so the key is ambiguous) never mutates
// the source or destination backlog: the tasks-axi move runs only after the
// authoritative transfer gate passes.
func TestHandoffOwnershipConflictLeavesBacklogUnchanged(t *testing.T) {
	path, err := exec.LookPath("tasks-axi")
	if err != nil {
		t.Skip("tasks-axi not found")
	}

	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "handoff-sm")
	if _, err := mhome.Init(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := mhome.Init(captainHome); err != nil {
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
	// Both the source and the destination captain own blocker-a.
	writeIntegrationHandoffAuthority(t, parent, "blocker-a", "Blocker")
	writeIntegrationHandoffAuthority(t, parent, "dependent-b", "Dependent")
	writeIntegrationHandoffAuthority(t, captainHome, "blocker-a", "Captain Blocker")
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
		t.Fatal("expected ambiguous ownership refusal")
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
		t.Fatal("source backlog changed after failed handoff")
	}
	if !bytes.Equal(destinationAfter, destinationBefore) {
		t.Fatal("destination backlog changed after failed handoff")
	}
}

// writeIntegrationHandoffAuthority creates one canonical ship task through the
// canonical surface and its projections.
func writeIntegrationHandoffAuthority(t *testing.T, homeDir, taskID, description string) {
	t.Helper()
	c := mustCanonical(t, homeDir)
	req := tauth.CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      mustTaskID(t, taskID),
		Owner:       "general",
		Description: description,
		Kind:        "ship",
		Project:     mustProjectID(t, "munsu"),
		Reason:      "seed",
	}
	if _, err := c.Create(mustOp(t, "integration-seed-"+taskID, req), req); err != nil {
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
