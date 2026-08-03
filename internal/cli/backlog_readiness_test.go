package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestBacklogReadyReportsDistinctReasonsAndIsPure(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "queued")
	for _, tc := range []struct {
		id    string
		state string
	}{
		{"blocked", "blocked"},
		{"working", "working"},
		{"done", "done"},
	} {
		seedAuthorityTask(t, auth, tc.id)
	}
	if _, err := auth.Block(taskauthority.BlockRequest{
		OperationID: newTaskAuthorityOperationID("seed-block"),
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      "blocked", ExpectedGeneration: 1,
		Detail: "dependency", Reason: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Start(taskauthority.StartRequest{
		OperationID: newTaskAuthorityOperationID("seed-start"),
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      "working", ExpectedGeneration: 1, Reason: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Complete(taskauthority.CompleteRequest{
		OperationID: newTaskAuthorityOperationID("seed-done"),
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      "done", ExpectedGeneration: 1, To: taskauthority.PhaseDone, Reason: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "queued.meta"), []byte("description=queued\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "queued.status"), []byte("queued: waiting\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "md"), []byte("# Backlog\n\n- [ ] queued: queued\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotReadinessFiles(t, homeDir)
	out, err := runBacklogLifecycleCommand(t, []string{"backlog", "ready", "--home", homeDir, "--output", "json"})
	if err != nil {
		t.Fatalf("ready: %v\n%s", err, out)
	}
	var response struct {
		Data []BacklogReadinessRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("ready output: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, row := range response.Data {
		if len(row.BlockingReasons) > 0 {
			got[row.TaskID] = row.BlockingReasons[0]
		}
	}
	for id, reason := range map[string]string{"blocked": "blocked", "working": "in-flight", "done": "terminal"} {
		if got[id] != reason {
			t.Fatalf("%s reason = %q, want %q; rows=%+v", id, got[id], reason, response.Data)
		}
	}
	if !responseHasReady(response.Data, "queued") {
		t.Fatalf("queued row not ready: %+v", response.Data)
	}
	if after := snapshotReadinessFiles(t, homeDir); after != before {
		t.Fatal("backlog ready changed durable state")
	}
}

func responseHasReady(rows []BacklogReadinessRow, id string) bool {
	for _, row := range rows {
		if row.TaskID == id {
			return row.Ready
		}
	}
	return false
}

func snapshotReadinessFiles(t *testing.T, homeDir string) string {
	t.Helper()
	var data []byte
	paths := []string{
		filepath.Join(homeDir, "state", "queued.meta"),
		filepath.Join(homeDir, "state", "queued.status"),
		filepath.Join(homeDir, "data", "md"),
	}
	for _, id := range []string{"queued", "blocked", "working", "done"} {
		paths = append(paths,
			filepath.Join(homeDir, "state", ".task-authority", "v1", "aggregates", id, "1.json"),
			filepath.Join(homeDir, "state", ".task-authority", "v1", "aggregates", id, "current"),
		)
	}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, fmt.Sprintf("%s=", path)...)
		data = append(data, content...)
		data = append(data, '\n')
	}
	return string(data)
}
