package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestBacklogReadyReportsDistinctReasonsAndIsPure(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
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
	blockReq := taskauthority.CanonicalBlockRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "blocked"),
		Precondition: domain.Of(1, 1),
		Detail:       "dependency",
		Reason:       "seed",
	}
	if _, err := auth.Block(mustCanonicalOp(t, "seed-block", blockReq), blockReq); err != nil {
		t.Fatal(err)
	}
	startReq := taskauthority.CanonicalStartRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "working"),
		Precondition: domain.Of(1, 1),
		Reason:       "seed",
	}
	if _, err := auth.Start(mustCanonicalOp(t, "seed-start", startReq), startReq); err != nil {
		t.Fatal(err)
	}
	doneReq := taskauthority.CanonicalCompleteRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "done"),
		Precondition: domain.Of(1, 1),
		To:           taskauthority.PhaseDone,
		Reason:       "seed",
	}
	if _, err := auth.Complete(mustCanonicalOp(t, "seed-done", doneReq), doneReq); err != nil {
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
			filepath.Join(homeDir, "state", "task-authority", "tasks", id, "current.json"),
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
